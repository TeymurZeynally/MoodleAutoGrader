package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"moodle-autograder/db"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

// Migrate applies all pending migrations from db/migrations and returns how many were applied.
func Migrate(ctx context.Context, dsn string) (int, error) {
	p, closeDB, err := provider(dsn)
	if err != nil {
		return 0, err
	}
	defer closeDB()
	results, err := p.Up(ctx)
	if err != nil {
		return len(results), fmt.Errorf("migrate up: %w", err)
	}

	return len(results), nil
}

// AppRole is the role the running processes connect with: it works with rows and can change nothing else.
const AppRole = "autograder_app"

// PrepareAppRole creates the role or updates its password, and gives it what the application needs on every table, also
// on those a migration just added. The audit log can only grow. It runs as the owner of the database, after the migrations.
func PrepareAppRole(ctx context.Context, dsn, password string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx, `select exists(select 1 from pg_roles where rolname = $1)`, AppRole).Scan(&exists); err != nil {
		return fmt.Errorf("find role: %w", err)
	}

	verb := "create"
	if exists {
		verb = "alter"
	}

	statements := []string{}
	var role string
	if err := conn.QueryRow(ctx, `select format('%s role %I login nosuperuser nocreatedb nocreaterole noinherit noreplication password %L', $1::text, $2::text, $3::text)`, verb, AppRole, password).Scan(&role); err != nil {
		return fmt.Errorf("build role statement: %w", err)
	}

	statements = append(statements, role)
	rows, err := conn.Query(ctx, `select nspname from pg_namespace where nspname not like 'pg\_%' and nspname not in ('information_schema', 'public') order by 1`)
	if err != nil {
		return fmt.Errorf("list schemas: %w", err)
	}

	schemas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("read schemas: %w", err)
	}

	id := pgx.Identifier{AppRole}.Sanitize()
	for _, schema := range schemas {
		s := pgx.Identifier{schema}.Sanitize()
		statements = append(statements,
			"grant usage on schema "+s+" to "+id,
			"grant select, insert, update, delete on all tables in schema "+s+" to "+id,
			"grant usage, select on all sequences in schema "+s+" to "+id,
			"grant execute on all functions in schema "+s+" to "+id)
	}

	statements = append(statements, "revoke update, delete on identity.audit_log from "+id)
	for _, st := range statements {
		if _, err := conn.Exec(ctx, st); err != nil {
			return fmt.Errorf("prepare the role of the application: %w", err)
		}
	}

	return nil
}

func provider(dsn string) (*goose.Provider, func(), error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres: %w", err)
	}

	sub, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("migrations fs: %w", err)
	}

	p, err := goose.NewProvider(goose.DialectPostgres, conn, sub)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("goose provider: %w", err)
	}

	return p, func() { _ = conn.Close() }, nil
}
