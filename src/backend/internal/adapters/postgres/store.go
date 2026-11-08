// Package postgres is the PostgreSQL adapter: one Store implements the Store ports of every use case in internal/app.
// Files are named after the schema they work with (db/migrations/00017): identity.go, moodle_*.go, settings.go,
// integrity.go, ai_*.go, grading_*.go. SQL always names the schema; types the queries return belong to the use cases.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"moodle-autograder/internal/app"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ db *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() { s.db.Close() }

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}

func wrap(what string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", what, err)
}

// found turns "no rows" into app.ErrNotFound, for lookups of a resource the user named.
func found(what string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Fail(app.ErrNotFound, "not found")
	}

	return wrap(what, err)
}

// courseOf runs a query that returns the course id of a resource.
func (s *Store) courseOf(ctx context.Context, what, query string, id int64) (int64, error) {
	var courseID int64
	err := s.db.QueryRow(ctx, query, id).Scan(&courseID)
	return courseID, found(what, err)
}
