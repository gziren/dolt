// Copyright 2022 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package commands

import (
	"context"
	"fmt"
	"gopkg.in/src-d/go-errors.v1"

	"github.com/dolthub/dolt/go/cmd/dolt/cli"
	"github.com/dolthub/dolt/go/cmd/dolt/errhand"
	eventsapi "github.com/dolthub/dolt/go/gen/proto/dolt/services/eventsapi/v1alpha1"
	"github.com/dolthub/dolt/go/libraries/doltcore/branch_control"
	"github.com/dolthub/dolt/go/libraries/doltcore/env"
	"github.com/dolthub/dolt/go/libraries/utils/argparser"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

var cherryPickDocs = cli.CommandDocumentationContent{
	ShortDesc: `Apply the changes introduced by an existing commit.`,
	LongDesc: `
Applies the changes from an existing commit and creates a new commit from the current HEAD. This requires your working tree to be clean (no modifications from the HEAD commit).

Cherry-picking merge commits or commits with schema changes or rename or drop tables is not currently supported. Row data changes are allowed as long as the two table schemas are exactly identical.

If applying the row data changes from the cherry-picked commit results in a data conflict, the cherry-pick operation is aborted and no changes are made to the working tree or committed.
`,
	Synopsis: []string{
		`{{.LessThan}}commit{{.GreaterThan}}`,
	},
}

var ErrCherryPickConflictsOrViolations = errors.NewKind("error: Unable to apply commit cleanly due to conflicts " +
	"or constraint violations. Please resolve the conflicts and/or constraint violations, then use `dolt add` " +
	"to add the tables to the staged set, and `dolt commit` to commit the changes and finish cherry-picking. \n" +
	"To undo all changes from this cherry-pick operation, use `dolt cherry-pick --abort`.\n" +
	"For more information on handling conflicts, see: https://docs.dolthub.com/concepts/dolt/git/conflicts")

type CherryPickCmd struct{}

// Name returns the name of the Dolt cli command. This is what is used on the command line to invoke the command.
func (cmd CherryPickCmd) Name() string {
	return "cherry-pick"
}

// Description returns a description of the command.
func (cmd CherryPickCmd) Description() string {
	return "Apply the changes introduced by an existing commit."
}

func (cmd CherryPickCmd) Docs() *cli.CommandDocumentation {
	ap := cli.CreateCherryPickArgParser()
	return cli.NewCommandDocumentation(cherryPickDocs, ap)
}

func (cmd CherryPickCmd) ArgParser() *argparser.ArgParser {
	return cli.CreateCherryPickArgParser()
}

// EventType returns the type of the event to log.
func (cmd CherryPickCmd) EventType() eventsapi.ClientEventType {
	return eventsapi.ClientEventType_CHERRY_PICK
}

// Exec executes the command.
func (cmd CherryPickCmd) Exec(ctx context.Context, commandStr string, args []string, dEnv *env.DoltEnv, cliCtx cli.CliContext) int {
	ap := cli.CreateCherryPickArgParser()
	help, usage := cli.HelpAndUsagePrinters(cli.CommandDocsForCommandString(commandStr, cherryPickDocs, ap))
	apr := cli.ParseArgsOrDie(ap, args, help)

	queryist, sqlCtx, closeFunc, err := cliCtx.QueryEngine(ctx)
	if err != nil {
		return HandleVErrAndExitCode(errhand.VerboseErrorFromError(err), usage)
	}
	if closeFunc != nil {
		defer closeFunc()
	}

	// This command creates a commit, so we need user identity
	err = branch_control.CheckAccess(sqlCtx, branch_control.Permissions_Write)
	if err != nil {
		cli.Println(err.Error())
		return 1
	}

	if apr.Contains(cli.AbortParam) {
		err = cherryPickAbort(queryist, sqlCtx)
		return HandleVErrAndExitCode(errhand.VerboseErrorFromError(err), usage)
	}

	// TODO : support single commit cherry-pick only for now
	if apr.NArg() == 0 {
		usage()
		return 1
	} else if apr.NArg() > 1 {
		return HandleVErrAndExitCode(errhand.BuildDError("cherry-picking multiple commits is not supported yet").SetPrintUsage().Build(), usage)
	}

	err = cherryPick(queryist, sqlCtx, apr)
	return HandleVErrAndExitCode(errhand.VerboseErrorFromError(err), usage)
}

func cherryPick(queryist cli.Queryist, sqlCtx *sql.Context, apr *argparser.ArgParseResults) error {
	cherryStr := apr.Arg(0)
	if len(cherryStr) == 0 {
		return fmt.Errorf("error: cannot cherry-pick empty string")
	}

	hasStagedChanges, hasUnstagedChanges, err := getDoltStatus(queryist, sqlCtx)
	if err != nil {
		return fmt.Errorf("error: failed to get dolt status: %w", err)
	}
	if hasStagedChanges {
		return fmt.Errorf("Please commit your staged changes before using cherry-pick.")
	}
	if hasUnstagedChanges {
		return fmt.Errorf("error: your local changes would be overwritten by cherry-pick.\nhint: commit your changes (dolt commit -am \"<message>\") or reset them (dolt reset --hard) to proceed.")
	}

	_, err = getRowsForSql(queryist, sqlCtx, "set @@dolt_allow_commit_conflicts = 1")
	if err != nil {
		return fmt.Errorf("error: failed to set @@dolt_allow_commit_conflicts: %w", err)
	}

	q, err := dbr.InterpolateForDialect("call dolt_cherry_pick(?)", []interface{}{cherryStr}, dialect.MySQL)
	if err != nil {
		return fmt.Errorf("error: failed to interpolate query: %w", err)
	}
	_, err = getRowsForSql(queryist, sqlCtx, q)
	if err != nil {
		return err
	}
	return nil
}

func cherryPickAbort(queryist cli.Queryist, sqlCtx *sql.Context) error {
	q := "call dolt_merge('--abort')"
	_, err := getRowsForSql(queryist, sqlCtx, q)
	if err != nil {
		errorText := err.Error()
		switch errorText {
		case "fatal: There is no merge to abort":
			return fmt.Errorf("error: There is no cherry-pick merge to abort")
		default:
			return err
		}
	}
	return nil
}

func getDoltStatus(queryist cli.Queryist, sqlCtx *sql.Context) (hasStagedChanges bool, hasUnstagedChanges bool, err error) {
	hasStagedChanges = false
	hasUnstagedChanges = false
	err = nil

	var statusRows []sql.Row
	statusRows, err = getRowsForSql(queryist, sqlCtx, "select * from dolt_status;")
	if err != nil {
		return
	}
	if len(statusRows) == 0 {
		return
	}

	for _, row := range statusRows {
		staged := row[1]
		var isStaged bool
		isStaged, err = getTinyIntColAsBool(staged)
		if err != nil {
			return
		}
		if isStaged {
			hasStagedChanges = true
		} else {
			hasUnstagedChanges = true
		}
	}

	return
}
