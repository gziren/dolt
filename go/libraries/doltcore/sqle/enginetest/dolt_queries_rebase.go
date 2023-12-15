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

package enginetest

import (
	"github.com/dolthub/go-mysql-server/enginetest/queries"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/plan"
	gmstypes "github.com/dolthub/go-mysql-server/sql/types"

	"github.com/dolthub/dolt/go/libraries/doltcore/rebase"
)

var DoltRebaseScriptTests = []queries.ScriptTest{
	{
		Name:        "dolt_rebase errors: basic errors",
		SetUpScript: []string{},
		Assertions: []queries.ScriptTestAssertion{
			{
				Query:          "call dolt_rebase('--abort');",
				ExpectedErrStr: "no rebase in progress",
			}, {
				Query:          "call dolt_rebase('--continue');",
				ExpectedErrStr: "no rebase in progress",
			},
		},
	},
	{
		/*
		   TODO: Error cases:
		        - already in a rebase or a merge/cherry-pick/etc
		        - working set not clean
		        - wrong number of args
		        - invalid args
		        - no database selected
		        - invalid rebase plan:
		            - first commit is a squash
		            - new commit hashes added
		            - other commit hashes removed?
		            - NULLs in fields? (should be impossible if we defined the schema correctly though...)
		        - merge commits
		        - conflicts!
		*/
		Name: "dolt_rebase errors: no database selected",
		SetUpScript: []string{
			"create database temp;",
			"use temp;",
			"drop database temp;",
		},
		Assertions: []queries.ScriptTestAssertion{
			{
				Query:          "call dolt_rebase('main');",
				ExpectedErrStr: "no database selected", // TODO: dolt_rebase proc is probably not available
			},
		},
	},

	{
		// TODO:
		Name:        "dolt_rebase errors: active cherry-pick or merge",
		SetUpScript: []string{},
		Assertions: []queries.ScriptTestAssertion{
			{
				Query:          "call dolt_rebase('main');",
				ExpectedErrStr: "no database selected", // TODO: dolt_rebase proc is probably not available
			},
		},
	},

	{
		Name: "dolt_rebase errors: invalid rebase plans",
		SetUpScript: []string{
			// TODO: consider sharing setupscripts for rebase test setups?
			"create table t (pk int primary key);",
			"call dolt_commit('-Am', 'creating table t');",
			"call dolt_branch('branch1');",

			"insert into t values (0);",
			"call dolt_commit('-am', 'inserting row 0');",

			"call dolt_checkout('branch1');",
			"insert into t values (1);",
			"call dolt_commit('-am', 'inserting row 1');",
			"insert into t values (10);",
			"call dolt_commit('-am', 'inserting row 10');",
		},
		Assertions: []queries.ScriptTestAssertion{
			// duplicate rebase_order (this is a PK though... so shouldn't be possible?)
			// TODO: Test that deleting a row from the rebase plan is equivalent to marking it as a drop
			{
				Query:    "call dolt_rebase('main');",
				Expected: []sql.Row{{0}},
			},
			{
				Query: "update dolt_rebase set action='squash';",
				Expected: []sql.Row{{gmstypes.OkResult{
					RowsAffected: 2,
					InsertID:     0,
					Info: plan.UpdateInfo{
						Matched:  2,
						Updated:  2,
						Warnings: 0,
					},
				}}},
			},
			{
				Query:          "call dolt_rebase('--continue');",
				ExpectedErrStr: rebase.ErrInvalidRebasePlanSquashFixupWithoutPick.Error(),
			},
			{
				Query: "update dolt_rebase set action='drop' where rebase_order=1;",
				Expected: []sql.Row{{gmstypes.OkResult{
					RowsAffected: 1,
					InsertID:     0,
					Info: plan.UpdateInfo{
						Matched:  1,
						Updated:  1,
						Warnings: 0,
					},
				}}},
			},
			{
				Query:          "call dolt_rebase('--continue');",
				ExpectedErrStr: rebase.ErrInvalidRebasePlanSquashFixupWithoutPick.Error(),
			},
		},
	},

	/*
		TODO: Other cases?
		    - dolt status should show that we're in a rebase
	*/
	{
		Name: "dolt_rebase: basic case",
		SetUpScript: []string{
			"create table t (pk int primary key);",
			"call dolt_commit('-Am', 'creating table t');",
			"call dolt_branch('branch1');",

			"insert into t values (0);",
			"call dolt_commit('-am', 'inserting row 0');",

			"call dolt_checkout('branch1');",
			"insert into t values (1);",
			"call dolt_commit('-am', 'inserting row 1');",
			"insert into t values (10);",
			"call dolt_commit('-am', 'inserting row 10');",
			"insert into t values (100);",
			"call dolt_commit('-am', 'inserting row 100');",
			"insert into t values (1000);",
			"call dolt_commit('-am', 'inserting row 1000');",
			"insert into t values (10000);",
			"call dolt_commit('-am', 'inserting row 10000');",
			"insert into t values (100000);",
			"call dolt_commit('-am', 'inserting row 100000');",
		},
		Assertions: []queries.ScriptTestAssertion{
			{
				Query: "call dolt_rebase('main');",
				// TODO: Add human readable status message: "rebase started"
				Expected: []sql.Row{{0}},
			},
			{
				Query: "select * from dolt_rebase order by rebase_order ASC;",
				Expected: []sql.Row{
					{uint(1), uint(2), doltCommit, "inserting row 1"},
					{uint(2), uint(2), doltCommit, "inserting row 10"},
					{uint(3), uint(2), doltCommit, "inserting row 100"},
					{uint(4), uint(2), doltCommit, "inserting row 1000"},
					{uint(5), uint(2), doltCommit, "inserting row 10000"},
					{uint(6), uint(2), doltCommit, "inserting row 100000"},
				},
			},
			{
				// TODO: Hard to adjust rebase order, when there are conflicts.
				//       Perhaps this should be a DECIMAL(6,2) field?
				Query: "update dolt_rebase set rebase_order=7 where rebase_order=6;",
				Expected: []sql.Row{{gmstypes.OkResult{RowsAffected: uint64(1), Info: plan.UpdateInfo{
					Matched: 1,
					Updated: 1,
				}}}},
			},
			{
				Query: "update dolt_rebase set action='squash' where rebase_order in (2, 3);",
				Expected: []sql.Row{{gmstypes.OkResult{RowsAffected: uint64(2), Info: plan.UpdateInfo{
					Matched: 2,
					Updated: 2,
				}}}},
			},
			{
				Query: "update dolt_rebase set action='drop' where rebase_order = 4;",
				Expected: []sql.Row{{gmstypes.OkResult{RowsAffected: uint64(1), Info: plan.UpdateInfo{
					Matched: 1,
					Updated: 1,
				}}}},
			},
			{
				Query: "update dolt_rebase set action='reword', commit_message='reworded!' where rebase_order = 5;",
				Expected: []sql.Row{{gmstypes.OkResult{RowsAffected: uint64(1), Info: plan.UpdateInfo{
					Matched: 1,
					Updated: 1,
				}}}},
			},
			{
				Query: "update dolt_rebase set action='fixup' where rebase_order = 7;",
				Expected: []sql.Row{{gmstypes.OkResult{RowsAffected: uint64(1), Info: plan.UpdateInfo{
					Matched: 1,
					Updated: 1,
				}}}},
			},
			{
				Query: "call dolt_rebase('--continue');",
				// TODO: Return a human readable status (e.g. rebase completed successfully)
				Expected: []sql.Row{{0}},
			},
			{
				// When rebase completes, rebase status should be cleared and the dolt_rebase table should be removed
				Query:          "call dolt_rebase('--continue');",
				ExpectedErrStr: "no rebase in progress",
			},
			{
				// The dolt_rebase table is gone after rebasing completes
				Query:          "select * from dolt_rebase;",
				ExpectedErrStr: "table not found: dolt_rebase",
			},
			{
				// Assert that the commit history is now composed of different commits
				Query: "select message from dolt_log order by date desc;",
				Expected: []sql.Row{
					{"reworded!"},
					{"inserting row 1\n\ninserting row 10\n\ninserting row 100"},
					{"inserting row 0"},
					{"creating table t"},
					{"Initialize data repository"}},
			},
			{
				Query:    "select * from t;",
				Expected: []sql.Row{{0}, {1}, {10}, {100}, {10000}, {100000}},
			},
		},
	},
}
