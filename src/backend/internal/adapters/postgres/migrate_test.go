package postgres

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
)

// asApp prepares the role of the application on a migrated database and returns how to connect with it.
func asApp(ctx context.Context, t *testing.T, dsn string) string {
	t.Helper()
	if err := PrepareAppRole(ctx, dsn, "test password's"); err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}

	u.User = url.UserPassword(AppRole, "test password's")
	return u.String()
}

// TestTheApplicationCanOnlyWorkWithRows needs a disposable database: TEST_DATABASE_URL.
func TestTheApplicationCanOnlyWorkWithRows(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	p, closeDB, err := provider(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	app := asApp(ctx, t, dsn)
	asApp(ctx, t, dsn) // a second run only updates the role
	db, err := sql.Open("pgx", app)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var super, createRole, createDB bool
	if err := db.QueryRowContext(ctx, `select rolsuper, rolcreaterole, rolcreatedb from pg_roles where rolname = current_user`).Scan(&super, &createRole, &createDB); err != nil {
		t.Fatal(err)
	}

	if super || createRole || createDB {
		t.Fatalf("superuser %v, create role %v, create database %v", super, createRole, createDB)
	}

	if _, err := db.ExecContext(ctx, `insert into identity.users (moodle_site, moodle_user_id, full_name) values ('s', 1, 'T')`); err != nil {
		t.Fatalf("rows are its work: %v", err)
	}

	if _, err := db.ExecContext(ctx, `insert into identity.audit_log (action, entity, entity_id) values ('a', 'b', 1)`); err != nil {
		t.Fatalf("the audit log grows: %v", err)
	}

	for what, statement := range map[string]string{
		"change the audit log": `update identity.audit_log set action = 'x'`,
		"clear the audit log":  `delete from identity.audit_log`,
		"truncate":             `truncate identity.users cascade`,
		"drop a table":         `drop table moodle.courses`,
		"create a table":       `create table moodle.stolen (i int)`,
		"create a schema":      `create schema stolen`,
		"change a table":       `alter table moodle.courses add column stolen int`,
		"read the migrations":  `select * from goose_db_version`,
		"read a file":          `select pg_read_file('/etc/passwd')`,
		"run a program":        `copy (select 1) to program 'true'`,
		"create a role":        `create role stolen`,
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Errorf("it can %s", what)
		}
	}
}

// TestMigrateUpDown needs a disposable database: TEST_DATABASE_URL=postgres://.../autograder_test. Skipped otherwise.
func TestMigrateUpDown(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	p, closeDB, err := provider(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if n, err := Migrate(ctx, dsn); err != nil || n == 0 {
		t.Fatalf("migrate: applied=%d err=%v", n, err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"identity.users", "moodle.courses", "moodle.course_memberships", "moodle.assignments", "moodle.moodle_attempts", "moodle.students", "moodle.submissions", "moodle.submission_files", "identity.audit_log", "ai.checklists", "grading.grade_drafts", "settings.course_settings", "integrity.integrity_matches"} {
		var exists bool
		if err := db.QueryRowContext(ctx, "select to_regclass($1) is not null", table).Scan(&exists); err != nil || !exists {
			t.Errorf("table %s missing (err=%v)", table, err)
		}
	}

	if n, err := Migrate(ctx, dsn); err != nil || n != 0 {
		t.Fatalf("second migrate must be a no-op: applied=%d err=%v", n, err)
	}

	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
}
