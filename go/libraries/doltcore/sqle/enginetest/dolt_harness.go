// Copyright 2020 Dolthub, Inc.
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
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	gms "github.com/dolthub/go-mysql-server"
	"github.com/dolthub/go-mysql-server/enginetest"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/libraries/doltcore/dtestutils"
	"github.com/dolthub/dolt/go/libraries/doltcore/env"
	"github.com/dolthub/dolt/go/libraries/doltcore/sqle"
	"github.com/dolthub/dolt/go/libraries/doltcore/sqle/dsess"
	"github.com/dolthub/dolt/go/libraries/doltcore/sqle/globalstate"
	"github.com/dolthub/dolt/go/libraries/doltcore/table/editor"
	"github.com/dolthub/dolt/go/store/datas"
	"github.com/dolthub/dolt/go/store/types"
)

const (
	user  = "test"
	email = "email@test.com"
)

type DoltHarness struct {
	t                    *testing.T
	env                  *env.DoltEnv
	session              *dsess.DoltSession
	databases            []sqle.Database
	databaseGlobalStates []globalstate.GlobalState
	hashes               []string
	parallelism          int
	skippedQueries       []string
}

var _ enginetest.Harness = (*DoltHarness)(nil)
var _ enginetest.SkippingHarness = (*DoltHarness)(nil)
var _ enginetest.ClientHarness = (*DoltHarness)(nil)
var _ enginetest.IndexHarness = (*DoltHarness)(nil)
var _ enginetest.VersionedDBHarness = (*DoltHarness)(nil)
var _ enginetest.ForeignKeyHarness = (*DoltHarness)(nil)
var _ enginetest.KeylessTableHarness = (*DoltHarness)(nil)
var _ enginetest.ReadOnlyDatabaseHarness = (*DoltHarness)(nil)

func newDoltHarness(t *testing.T) *DoltHarness {
	dEnv := dtestutils.CreateTestEnv()
	mrEnv, err := env.DoltEnvAsMultiEnv(context.Background(), dEnv)
	require.NoError(t, err)
	b := env.GetDefaultInitBranch(dEnv.Config)
	pro := sqle.NewDoltDatabaseProvider(b, mrEnv.FileSystem())
	require.NoError(t, err)
	pro = pro.WithDbFactoryUrl(doltdb.InMemDoltDB)

	localConfig := dEnv.Config.WriteableConfig()

	session, err := dsess.NewDoltSession(sql.NewEmptyContext(), enginetest.NewBaseSession(), pro, localConfig)
	require.NoError(t, err)
	dh := &DoltHarness{
		t:              t,
		session:        session,
		skippedQueries: defaultSkippedQueries,
	}

	if types.IsFormat_DOLT_1(dEnv.DoltDB.Format()) {
		dh = dh.WithSkippedQueries([]string{
			"show",        // todo(andy): "show_create_table_t2"
			"foreign key", //TODO: Daylon
			"no_primary",
		})
	}

	return dh
}

var defaultSkippedQueries = []string{
	"show variables",             // we set extra variables
	"show create table fk_tbl",   // we create an extra key for the FK that vanilla gms does not
	"show indexes from",          // we create / expose extra indexes (for foreign keys)
	"typestable",                 // Bit type isn't working?
	"show global variables like", // we set extra variables
}

func (d *DoltHarness) NewEngine(ctx *sql.Context, t *testing.T) *gms.Engine {
	dbs := enginetest.CreateTestData(t, d)
	engine := enginetest.NewEngineWithDbs(t, d, dbs)
	ctx = d.NewContext()
	engine, err := d.checkpointWs(ctx, engine)
	if err != nil {
		panic(err)
	}
	return engine
}

func (d *DoltHarness) checkpointWs(ctx *sql.Context, e *gms.Engine) (*gms.Engine, error) {
	d.hashes = make([]string, len(d.databases))
	for i := range d.databases {
		db := d.databases[i].Name()
		mustCall(ctx, e, fmt.Sprintf("use %s", db))
		res := mustCall(ctx, e, fmt.Sprintf("call dolt_commit('--allow-empty', '-am', 'checkpoint enginetest database %s')", db))
		d.hashes[i] = res[0][0].(string)
	}
	return e, nil
}

func mustCall(ctx *sql.Context, e *gms.Engine, q string) []sql.Row {
	sch, iter, err := e.Query(ctx, q)
	if err != nil {
		panic(err)
	}
	rows, err := sql.RowIterToRows(ctx, sch, iter)
	if err != nil {
		panic(err)
	}
	return rows
}

//func (d *DoltHarness) checkpointWs2(ctx *sql.Context, dbs []sql.Database) error {
//	hashes := make([]*doltdb.Commit, len(dbs))
//	for i := range dbs {
//		db := dbs[i].(sqle.Database)
//		dSess := dsess.DSessFromSess(ctx.Session)
//		roots, ok := dSess.GetRoots(ctx, db.Name())
//		ws, err := dSess.WorkingSet(ctx, db.Name())
//		wsFromHash, err := ws.HashOf()
//		if !ok {
//			panic("database not found")
//		}
//
//		roots, err = actions.StageAllTables(ctx, roots, nil)
//		if err != nil {
//			return err
//		}
//		//err = db.DbData().Rsw.UpdateStagedRoot(ctx, roots.Staged)
//		//err = db.DbData().Rsw.UpdateWorkingRoot(ctx, roots.Working)
//		ws = ws.WithWorkingRoot(roots.Working).WithStagedRoot(roots.Staged)
//		err = db.GetDoltDB().UpdateWorkingSet(ctx, ws.Ref(), ws, wsFromHash, doltdb.TodoWorkingSetMeta())
//
//		//ws, err := dSess.WorkingSet(ctx, db.Name())
//		//ws, err := db.GetWorkingSet(ctx)
//		//prevHash, err := ws.HashOf()
//		txBeginWs, err := db.GetDoltDB().ResolveWorkingSet(ctx, ws.Ref())
//		if err != nil {
//			panic("couldn't get working set: " + err.Error())
//		}
//		prevHash, err := txBeginWs.HashOf()
//		if err != nil {
//			panic(err)
//		}
//
//		var mergeParentCommits []*doltdb.Commit
//		if ws.MergeActive() {
//			mergeParentCommits = []*doltdb.Commit{ws.MergeState().Commit()}
//		}
//
//		t := datas.CommitNowFunc()
//		if err != nil {
//			panic("couldn't get roots: " + err.Error())
//		}
//		pendingCommit, err := actions.GetCommitStaged(ctx, roots, ws.MergeActive(), mergeParentCommits, db.DbData(), actions.CommitStagedProps{
//			Message:    "auto commit",
//			Date:       t,
//			AllowEmpty: true,
//			Force:      false,
//			Name:       user,
//			Email:      email,
//		})
//		if err != nil {
//			panic("pending commit error: " + err.Error())
//		}
//
//		commit, err := db.GetDoltDB().CommitWithWorkingSet(
//			ctx,
//			db.DbData().Rsr.CWBHeadRef(),
//			ws.Ref(),
//			pendingCommit,
//			ws.WithStagedRoot(pendingCommit.Roots.Staged).WithWorkingRoot(pendingCommit.Roots.Working).ClearMerge(),
//			prevHash,
//			doltdb.TodoWorkingSetMeta(),
//		)
//		if err != nil {
//			return err
//		}
//		hashes[i] = commit
//	}
//	d.hashes = hashes
//	return nil
//
//}

func checkpointWs(ctx *sql.Context, dbs []sql.Database) error {
	hashes := make([]*doltdb.Commit, len(dbs))
	for i := range dbs {
		db := dbs[i].(sqle.Database)
		dSess := dsess.DSessFromSess(ctx.Session)
		roots, ok := dSess.GetRoots(ctx, db.Name())
		if !ok {
			panic("database not found")
		}
		tableNames, err := db.GetTableNames(ctx)
		cmRoot, err := roots.Working.UpdateSuperSchemasFromOther(ctx, tableNames, roots.Working)
		err = db.DbData().Rsw.UpdateWorkingRoot(ctx, cmRoot)
		if err != nil {
			return err
		}
		err = db.DbData().Rsw.UpdateStagedRoot(ctx, cmRoot)
		if err != nil {
			return err
		}
		_, h, err := db.GetDoltDB().WriteRootValue(ctx, cmRoot)
		meta, err := datas.NewCommitMeta("max max", "max@max.max", "Setup data")
		cm, err := db.GetDoltDB().CommitWithParentCommits(ctx, h, db.DbData().Rsr.CWBHeadRef(), nil, meta)
		if err != nil {
			return err
		}
		hashes[i] = cm
	}
	//d.hashes = hashes
	return nil
}

func (d *DoltHarness) RestoreCheckpoint(ctx *sql.Context, t *testing.T, e *gms.Engine) *gms.Engine {
	for i := range d.databases {
		db := d.databases[i].Name()
		e.Query(ctx, fmt.Sprintf("use %s", db))
		e.Query(ctx, fmt.Sprintf("call dolt_reset('--hard', '%s')", d.hashes[i]))
	}
	return e
}

//func (d *DoltHarness) RestoreCheckpoint2(ctx *sql.Context, t *testing.T, e *gms.Engine) *gms.Engine {
//	// reset database workingsets
//	for i := range d.databases {
//		db := d.databases[i]
//
//		// ctx isn't used by the nomsBlockStore to get DS
//		ddb := db.GetDoltDB()
//		dSess := dsess.DSessFromSess(ctx.Session)
//		roots, ok := dSess.GetRoots(ctx, db.Name())
//		if !ok {
//			panic("database not found")
//		}
//		h, err := d.hashes[i].HashOf()
//		//cm, roots, err := actions.ResetHardTables(ctx, db.DbData(), h.String(), roots)
//		//err := db.DbData().Rsw.UpdateWorkingRoot(ctx, roots.Head)
//		//if err != nil {
//		//	panic("database not found")
//		//}
//		//err = db.DbData().Rsw.UpdateStagedRoot(ctx, roots.Head)
//		//if err != nil {
//		//	panic("database not found")
//		//}
//		ws, err := dSess.WorkingSet(ctx, db.Name())
//		txBeginWs, err := db.GetDoltDB().ResolveWorkingSet(ctx, ws.Ref())
//		if err != nil {
//			panic("couldn't get working set: " + err.Error())
//		}
//		wsFromHash, err := txBeginWs.HashOf()
//		if err != nil {
//			panic(err)
//		}
//
//		_, newRoots, err := actions.ResetHardTables(ctx, db.DbData(), h.String(), roots)
//
//		//if err != nil {
//		//	panic(err)
//		//}
//		err = ddb.SetHeadToCommit(ctx, db.DbData().Rsr.CWBHeadRef(), d.hashes[i])
//		//err := ddb.SetHead(ctx, ref.NewBranchRef("main"), d.hashes[i])
//		if err != nil {
//			panic(err)
//		}
//		err = db.GetDoltDB().UpdateWorkingSet(ctx, ws.Ref(), ws.WithStagedRoot(newRoots.Staged).WithWorkingRoot(newRoots.Working), wsFromHash, doltdb.TodoWorkingSetMeta())
//
//		//ws, err := dSess.WorkingSet(ctx, db.Name())
//		if err != nil {
//			panic(err)
//		}
//		//err = dSess.SetWorkingSet(ctx, db.Name(), ws.WithWorkingRoot(roots.Working).WithStagedRoot(roots.Staged))
//		//if err != nil {
//		//	panic(err)
//		//}
//
//		//err := db.SetRoot(ctx, d.hashes[i])
//		//d.databases[i] = sqle.NewDatabase(db.Name(), env.DbData{
//		//	Ddb: ddb,
//		//	Rsw: db.DbData().Rsw,
//		//	Rsr: db.DbData().Rsr,
//		//	Drw: db.DbData().Drw,
//		//}, db.EditOptions())
//	}
//	// throw away session
//	//d.session = d.newSessionWithClient(sql.Client{Address: "localhost", User: "root"})
//	//d.session.SetCurrentDatabase("mydb")
//	// the enginetest has to create the new contetx
//	return enginetest.NewEngineWithDbs(t, d, dsqleDBsAsSqlDBs(d.databases))
//}

// WithParallelism returns a copy of the harness with parallelism set to the given number of threads. A value of 0 or
// less means to use the system parallelism settings.
func (d *DoltHarness) WithParallelism(parallelism int) *DoltHarness {
	nd := *d
	nd.parallelism = parallelism
	return &nd
}

// WithSkippedQueries returns a copy of the harness with the given queries skipped
func (d *DoltHarness) WithSkippedQueries(queries []string) *DoltHarness {
	nd := *d
	nd.skippedQueries = append(d.skippedQueries, queries...)
	return &nd
}

// SkipQueryTest returns whether to skip a query
func (d *DoltHarness) SkipQueryTest(query string) bool {
	lowerQuery := strings.ToLower(query)
	for _, skipped := range d.skippedQueries {
		if strings.Contains(lowerQuery, strings.ToLower(skipped)) {
			return true
		}
	}

	return false
}

func (d *DoltHarness) Parallelism() int {
	if d.parallelism <= 0 {

		// always test with some parallelism
		parallelism := runtime.NumCPU()

		if parallelism <= 1 {
			parallelism = 2
		}

		return parallelism
	}

	return d.parallelism
}

func (d *DoltHarness) NewContext() *sql.Context {
	return sql.NewContext(context.Background(), sql.WithSession(d.session))
}

func (d *DoltHarness) NewContextWithClient(client sql.Client) *sql.Context {
	return sql.NewContext(context.Background(), sql.WithSession(d.newSessionWithClient(client)))
}

func (d *DoltHarness) NewSession() *sql.Context {
	d.session = d.newSessionWithClient(sql.Client{Address: "localhost", User: "root"})
	return d.NewContext()
}

func (d *DoltHarness) newSessionWithClient(client sql.Client) *dsess.DoltSession {
	states := make([]dsess.InitialDbState, len(d.databases))
	for i, db := range d.databases {
		states[i] = getDbState(d.t, db, d.env)
	}
	dbs := dsqleDBsAsSqlDBs(d.databases)
	pro := d.NewDatabaseProvider(dbs...)
	localConfig := d.env.Config.WriteableConfig()

	dSession, err := dsess.NewDoltSession(
		enginetest.NewContext(d),
		sql.NewBaseSessionWithClientServer("address", client, 1),
		pro.(dsess.RevisionDatabaseProvider),
		localConfig,
		states...,
	)
	require.NoError(d.t, err)
	return dSession
}

func (d *DoltHarness) SupportsNativeIndexCreation() bool {
	return true
}

func (d *DoltHarness) SupportsForeignKeys() bool {
	if types.IsFormat_DOLT_1(d.env.DoltDB.Format()) {
		return false
	}
	return true
}

func (d *DoltHarness) SupportsKeylessTables() bool {
	return true
}

func (d *DoltHarness) NewDatabase(name string) sql.Database {
	return d.NewDatabases(name)[0]
}

func (d *DoltHarness) NewDatabases(names ...string) []sql.Database {
	dEnv := dtestutils.CreateTestEnv()
	d.env = dEnv

	d.databases = nil
	d.databaseGlobalStates = nil
	for _, name := range names {
		opts := editor.Options{Deaf: dEnv.DbEaFactory(), Tempdir: dEnv.TempTableFilesDir()}
		db := sqle.NewDatabase(name, dEnv.DbData(), opts)
		d.databases = append(d.databases, db)

		globalState := globalstate.NewGlobalStateStore()
		d.databaseGlobalStates = append(d.databaseGlobalStates, globalState)
	}

	// TODO(zachmu): it should be safe to reuse a session with a new database, but it isn't in all cases. Particularly, if you
	//  have a database that only ever receives read queries, and then you re-use its session for a new database with
	//  the same name, the first write query will panic on dangling references in the noms layer. Not sure why this is
	//  happening, but it only happens as a result of this test setup.
	_ = d.NewSession()

	return dsqleDBsAsSqlDBs(d.databases)
}

func (d *DoltHarness) NewReadOnlyDatabases(names ...string) (dbs []sql.ReadOnlyDatabase) {
	for _, db := range d.NewDatabases(names...) {
		dbs = append(dbs, sqle.ReadOnlyDatabase{Database: db.(sqle.Database)})
	}
	return
}

func (d *DoltHarness) NewDatabaseProvider(dbs ...sql.Database) sql.MutableDatabaseProvider {
	if d.env == nil {
		d.env = dtestutils.CreateTestEnv()
	}
	mrEnv, err := env.DoltEnvAsMultiEnv(context.Background(), d.env)
	require.NoError(d.t, err)
	b := env.GetDefaultInitBranch(d.env.Config)
	pro := sqle.NewDoltDatabaseProvider(b, mrEnv.FileSystem(), dbs...)
	return pro.WithDbFactoryUrl(doltdb.InMemDoltDB)
}

func getDbState(t *testing.T, db sqle.Database, dEnv *env.DoltEnv) dsess.InitialDbState {
	ctx := context.Background()

	head := dEnv.RepoStateReader().CWBHeadSpec()
	headCommit, err := dEnv.DoltDB.Resolve(ctx, head, dEnv.RepoStateReader().CWBHeadRef())
	require.NoError(t, err)

	ws, err := dEnv.WorkingSet(ctx)
	require.NoError(t, err)

	return dsess.InitialDbState{
		Db:         db,
		HeadCommit: headCommit,
		WorkingSet: ws,
		DbData:     dEnv.DbData(),
		Remotes:    dEnv.RepoState.Remotes,
	}
}

func (d *DoltHarness) NewTable(db sql.Database, name string, schema sql.PrimaryKeySchema) (sql.Table, error) {
	var err error
	if ro, ok := db.(sqle.ReadOnlyDatabase); ok {
		err = ro.CreateTable(enginetest.NewContext(d).WithCurrentDB(db.Name()), name, schema)
	} else {
		err = db.(sqle.Database).CreateTable(enginetest.NewContext(d).WithCurrentDB(db.Name()), name, schema)
	}
	if err != nil {
		return nil, err
	}

	table, ok, err := db.GetTableInsensitive(enginetest.NewContext(d).WithCurrentDB(db.Name()), name)
	require.NoError(d.t, err)
	require.True(d.t, ok, "table %s not found after creation", name)
	return table, nil
}

// Dolt doesn't version tables per se, just the entire database. So ignore the name and schema and just create a new
// branch with the given name.
func (d *DoltHarness) NewTableAsOf(db sql.VersionedDatabase, name string, schema sql.PrimaryKeySchema, asOf interface{}) sql.Table {
	table, err := d.NewTable(db, name, schema)
	if err != nil {
		require.True(d.t, sql.ErrTableAlreadyExists.Is(err))
	}

	table, ok, err := db.GetTableInsensitive(enginetest.NewContext(d), name)
	require.NoError(d.t, err)
	require.True(d.t, ok)

	return table
}

// Dolt doesn't version tables per se, just the entire database. So ignore the name and schema and just create a new
// branch with the given name.
func (d *DoltHarness) SnapshotTable(db sql.VersionedDatabase, name string, asOf interface{}) error {
	switch db.(type) {
	case sqle.ReadOnlyDatabase:
		// TODO: insert query to dolt_branches table (below)
		// can't be performed against ReadOnlyDatabase
		d.t.Skip("can't create SnaphotTables for ReadOnlyDatabases")
	case sqle.Database:
	default:
		panic("not a Dolt SQL Database")
	}

	e := enginetest.NewEngineWithDbs(d.t, d, []sql.Database{db})

	asOfString, ok := asOf.(string)
	require.True(d.t, ok)

	ctx := enginetest.NewContext(d)
	_, iter, err := e.Query(ctx,
		"SELECT COMMIT('-am', 'test commit');")
	require.NoError(d.t, err)
	_, err = sql.RowIterToRows(ctx, nil, iter)
	require.NoError(d.t, err)

	headHash, err := ctx.GetSessionVariable(ctx, dsess.HeadKey(db.Name()))
	require.NoError(d.t, err)

	ctx = enginetest.NewContext(d)
	// TODO: there's a bug in test setup with transactions, where the HEAD session var gets overwritten on transaction
	//  start, so we quote it here instead
	// query := "insert into dolt_branches (name, hash) values ('" + asOfString + "', @@" + dsess.HeadKey(ddb.Name()) + ")"
	query := "insert into dolt_branches (name, hash) values ('" + asOfString + "', '" + headHash.(string) + "')"

	_, iter, err = e.Query(ctx,
		query)
	require.NoError(d.t, err)
	_, err = sql.RowIterToRows(ctx, nil, iter)
	require.NoError(d.t, err)

	return nil
}

func dsqleDBsAsSqlDBs(dbs []sqle.Database) []sql.Database {
	sqlDbs := make([]sql.Database, 0, len(dbs))
	for _, db := range dbs {
		sqlDbs = append(sqlDbs, db)
	}
	return sqlDbs
}
