// Copyright 2024 Dolthub, Inc.
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

package admin

import (
	"context"
	"io"

	"github.com/dolthub/dolt/go/cmd/dolt/cli"
	"github.com/dolthub/dolt/go/libraries/doltcore/diff"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb/durable"
	"github.com/dolthub/dolt/go/libraries/doltcore/env"
	"github.com/dolthub/dolt/go/libraries/doltcore/env/actions/commitwalk"
	"github.com/dolthub/dolt/go/libraries/utils/argparser"
	"github.com/dolthub/dolt/go/store/datas"
	"github.com/dolthub/dolt/go/store/hash"
	"github.com/dolthub/dolt/go/store/nbs"
	"github.com/dolthub/dolt/go/store/prolly/tree"
	"github.com/pkg/errors"
)

type ArchiveCmd struct {
}

func (cmd ArchiveCmd) Name() string {
	return "archive"
}

var docs = cli.CommandDocumentationContent{
	ShortDesc: "Create archive files using native or cgo compression, then verify.",
	LongDesc:  `Run this command on a dolt database only after running 'dolt gc'. This command will create an archive file to the CWD. Suffix: .darc. After the new file is generated, it will read every chunk from the new file and verify that the chunk hashes to the correct addr.`,

	Synopsis: []string{
		`--no-group`,
	},
}

// Description returns a description of the command
func (cmd ArchiveCmd) Description() string {
	return "Hidden command to kick the tires with the new archive format."
}
func (cmd ArchiveCmd) RequiresRepo() bool {
	return true
}
func (cmd ArchiveCmd) Docs() *cli.CommandDocumentation {
	ap := cmd.ArgParser()
	return cli.NewCommandDocumentation(docs, ap)
}

func (cmd ArchiveCmd) ArgParser() *argparser.ArgParser {
	ap := argparser.NewArgParserWithMaxArgs(cmd.Name(), 0)
	/* TODO: Implement these flags
	ap.SupportsFlag("raw", "", "Create an archive file with 0 compression")
	ap.SupportsFlag("no-manifest", "", "Do not alter the manifest file. Generate the archive file only")
	ap.SupportsFlag("no-grouping", "", "Do not attempt to group chunks. Default dictionary will be used for all chunks")
	ap.SupportsFlag("verify-existing", "", "Skip generation altogether and just verify the existing archive file.")
	*/
	return ap
}
func (cmd ArchiveCmd) Hidden() bool {
	return true
}

func (cmd ArchiveCmd) Exec(ctx context.Context, commandStr string, args []string, dEnv *env.DoltEnv, cliCtx cli.CliContext) int {
	ap := cmd.ArgParser()
	help, _ := cli.HelpAndUsagePrinters(cli.CommandDocsForCommandString(commandStr, docs, ap))
	_ = cli.ParseArgsOrDie(ap, args, help)

	db := doltdb.HackDatasDatabaseFromDoltDB(dEnv.DoltDB)
	cs := datas.ChunkStoreFromDatabase(db)
	if _, ok := cs.(*nbs.GenerationalNBS); !ok {
		cli.PrintErrln("archive command requires a GenerationalNBS")
		return 1
	}

	datasets, err := db.Datasets(ctx)
	if err != nil {
		cli.PrintErrln(err)
		return 1
	}

	hs := hash.NewHashSet()
	err = datasets.IterAll(ctx, func(id string, hash hash.Hash) error {
		hs.Insert(hash)
		return nil
	})

	groupings := nbs.NewChunkRelations()
	err = historicalFuzzyMatching(ctx, hs, &groupings, dEnv.DoltDB)
	if err != nil {
		cli.PrintErrln(err)
		return 1
	}
	cli.Printf("Found %d possible relations by walking history\n", groupings.Count())

	err = nbs.BuildArchive(ctx, cs, &groupings)
	if err != nil {
		cli.PrintErrln(err)
		return 1
	}

	return 0
}

func historicalFuzzyMatching(ctx context.Context, heads hash.HashSet, groupings *nbs.ChunkRelations, db *doltdb.DoltDB) error {
	hs := []hash.Hash{}
	for h := range heads {
		_, err := db.ReadCommit(ctx, h)
		if err != nil {
			continue
		}
		hs = append(hs, h)
	}

	iterator, err := commitwalk.GetTopologicalOrderIterator(ctx, db, hs, func(cmt *doltdb.OptionalCommit) (bool, error) {
		return true, nil
	})
	if err != nil {
		return err
	}
	for {
		h, _, err := iterator.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		err = relateCommitToParentChunks(ctx, h, groupings, db)
		if err != nil {
			return err
		}
	}

	return nil
}

var ErrNoShallowClones = errors.New("building archives only allowed for full clones")

func relateCommitToParentChunks(ctx context.Context, commit hash.Hash, groupings *nbs.ChunkRelations, db *doltdb.DoltDB) error {
	oCmt, err := db.ReadCommit(ctx, commit)
	if err != nil {
		return nil // Only want commits. Skip others.
	}
	cmt, ok := oCmt.ToCommit()
	if !ok {
		return ErrNoShallowClones
	}
	cmtRv, err := cmt.GetRootValue(ctx)
	if err != nil {
		return err
	}

	// Dolt supports only 1 or 2 parents, but the logic is the same for each. And if there are no parents, no op.
	for i := 0; i < cmt.NumParents(); i++ {
		oCmt, err = cmt.GetParent(ctx, i)
		if err != nil {
			return err
		}
		parent, exists := oCmt.ToCommit()
		if !exists {
			return ErrNoShallowClones
		}

		parentRv, err := parent.GetRootValue(ctx)
		if err != nil {
			return err
		}

		deltas, err := diff.GetTableDeltas(ctx, cmtRv, parentRv)
		if err != nil {
			return err
		}

		for _, delta := range deltas {
			schChg, err := delta.HasSchemaChanged(ctx)
			if err != nil {
				return err
			}
			if schChg {
				continue
			}
			if delta.HasPrimaryKeySetChanged() {
				continue
			}

			changed, err := delta.HasDataChanged(ctx)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}

			from, to, err := delta.GetRowData(ctx)

			f := durable.ProllyMapFromIndex(from)
			t := durable.ProllyMapFromIndex(to)

			if f.Node().Level() != t.Node().Level() {
				continue
			}
			err = tree.ChunkAddressDiffOrderedTrees(ctx, f.Tuples(), t.Tuples(), func(ctx context.Context, diff tree.AddrDiff) error {
				groupings.Add(diff.From, diff.To)
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}
