package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"nuvio/internal/model"
)

func rawSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	return db
}

func schemaVersionOf(t *testing.T, db *sql.DB) int {
	t.Helper()
	v, err := readSchemaVersion(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFreshDatabaseStampsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	if got := schemaVersionOf(t, s.db); got != latestVersion() {
		t.Fatalf("fresh schema version = %d, want %d", got, latestVersion())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must neither re-stamp nor apply anything.
	next := mustOpen(t, dir)
	defer next.Close()
	if got := schemaVersionOf(t, next.db); got != latestVersion() {
		t.Fatalf("schema version after reopen = %d, want %d", got, latestVersion())
	}
}

// Databases written before user_version existed report 0 while already holding
// the baseline schema and user data. They must be stamped and keep everything.
func TestPreVersionedDatabaseIsUpgradedWithoutDataLoss(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	u := mustUser(t, s, "alice")
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceItems([]*model.Item{{
		ID: "m1", Type: model.TypeMovie, Name: "Heat", SortName: "Heat",
		LibraryID: lib.ID, ParentID: lib.ID,
	}}); err != nil {
		t.Fatal(err)
	}
	s.SetProgress(u.ID, "m1", 42, false)
	s.SetFavorite(u.ID, "m1", true)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db := rawSQLite(t, filepath.Join(dir, "nuvio.db"))
	if _, err := db.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	next := mustOpen(t, dir)
	defer next.Close()
	if got := schemaVersionOf(t, next.db); got != latestVersion() {
		t.Fatalf("upgraded schema version = %d, want %d", got, latestVersion())
	}
	if _, ok := next.FindUser(u.ID); !ok {
		t.Fatal("user lost during version stamping")
	}
	if ud := next.GetUserData(u.ID, "m1"); ud.PositionTicks != 42 || !ud.Favorite {
		t.Fatalf("user data lost during version stamping: %+v", ud)
	}
	items, err := next.Items()
	if err != nil || len(items) != 1 || items[0].ID != "m1" {
		t.Fatalf("items after upgrade: %v %+v", err, items)
	}
}

// A database from a newer build may rely on columns this binary cannot read;
// startup must fail instead of running old code against it.
func TestNewerDatabaseSchemaIsRejected(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db := rawSQLite(t, filepath.Join(dir, "nuvio.db"))
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", latestVersion()+1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir, "Nuvio")
	if err == nil {
		s.Close()
		t.Fatal("opening a database from a newer build should fail")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyPendingMigrationsInOrder(t *testing.T) {
	db := rawSQLite(t, filepath.Join(t.TempDir(), "scratch.db"))
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	steps := []migration{
		{version: 2, name: "create scratch", stmts: []string{"CREATE TABLE scratch(id TEXT)"}},
		{version: 3, name: "add note", stmts: []string{"ALTER TABLE scratch ADD COLUMN note TEXT NOT NULL DEFAULT ''"}},
	}
	if err := applyPendingMigrations(db, 1, steps); err != nil {
		t.Fatal(err)
	}
	if got := schemaVersionOf(t, db); got != 3 {
		t.Fatalf("version = %d, want 3", got)
	}
	if _, err := db.Exec("INSERT INTO scratch(id, note) VALUES('a', 'b')"); err != nil {
		t.Fatalf("migrated table is not usable: %v", err)
	}

	// A stale caller replaying the same list must skip the applied steps
	// instead of failing on duplicate DDL.
	if err := applyPendingMigrations(db, 1, steps); err != nil {
		t.Fatalf("replaying an applied migration: %v", err)
	}
	if got := schemaVersionOf(t, db); got != 3 {
		t.Fatalf("version after replay = %d, want 3", got)
	}
}

// A step with several statements is atomic: the first statement must not
// survive when a later one fails.
func TestFailedMigrationRollsBack(t *testing.T) {
	db := rawSQLite(t, filepath.Join(t.TempDir(), "scratch.db"))
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	steps := []migration{{
		version: 2,
		name:    "broken",
		stmts: []string{
			"CREATE TABLE half_done(a TEXT)",
			"ALTER TABLE does_not_exist ADD COLUMN x TEXT",
		},
	}}
	if err := applyPendingMigrations(db, 1, steps); err == nil {
		t.Fatal("expected the migration to fail")
	}
	if got := schemaVersionOf(t, db); got != 1 {
		t.Fatalf("version after failed migration = %d, want 1", got)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'half_done'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a failed migration left partial DDL behind")
	}
}

// Versions must be contiguous; silently skipping a step would apply later DDL
// to a schema it was not written for.
func TestMigrationGapIsRejected(t *testing.T) {
	db := rawSQLite(t, filepath.Join(t.TempDir(), "scratch.db"))
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	steps := []migration{{version: 3, name: "skips 2", stmts: []string{"CREATE TABLE skipped(a TEXT)"}}}
	err := applyPendingMigrations(db, 1, steps)
	if err == nil || !strings.Contains(err.Error(), "expects version 2") {
		t.Fatalf("error = %v", err)
	}
	if got := schemaVersionOf(t, db); got != 1 {
		t.Fatalf("version = %d, want 1", got)
	}
}

// End-to-end check of the registry: both a fresh database and an existing one
// must land on the same version and schema after a step is registered.
func TestSetupSchemaAppliesRegisteredMigrations(t *testing.T) {
	orig := migrations
	migrations = []migration{{
		version: 2,
		name:    "create marker",
		stmts:   []string{"CREATE TABLE marker(id TEXT PRIMARY KEY)"},
	}}
	defer func() { migrations = orig }()

	dir := t.TempDir()
	first := mustOpen(t, dir)
	if got := schemaVersionOf(t, first.db); got != 2 {
		t.Fatalf("fresh database version = %d, want 2", got)
	}
	if !hasTable(t, first.db, "marker") {
		t.Fatal("migration did not run on a fresh database")
	}
	u := mustUser(t, first, "alice")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := mustOpen(t, dir)
	defer second.Close()
	if got := schemaVersionOf(t, second.db); got != 2 {
		t.Fatalf("reopened database version = %d, want 2", got)
	}
	if _, ok := second.FindUser(u.ID); !ok {
		t.Fatal("migration lost existing data")
	}
}

// An existing v1 database must run the same step even though the fresh path
// already has the table.
func TestRegisteredMigrationUpgradesExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir)
	u := mustUser(t, s, "alice")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	orig := migrations
	migrations = []migration{{
		version: 2,
		name:    "add marker and user column",
		stmts: []string{
			"CREATE TABLE marker(id TEXT PRIMARY KEY)",
			"ALTER TABLE users ADD COLUMN preferred_language TEXT NOT NULL DEFAULT ''",
		},
	}}
	defer func() { migrations = orig }()

	next := mustOpen(t, dir)
	defer next.Close()
	if got := schemaVersionOf(t, next.db); got != 2 {
		t.Fatalf("upgraded database version = %d, want 2", got)
	}
	if !hasTable(t, next.db, "marker") {
		t.Fatal("migration did not run on an existing database")
	}
	if !hasColumn(t, next.db, "users", "preferred_language") {
		t.Fatal("column migration did not run against the populated table")
	}
	if _, ok := next.FindUser(u.ID); !ok {
		t.Fatal("existing data lost during upgrade")
	}
}

func hasTable(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}
