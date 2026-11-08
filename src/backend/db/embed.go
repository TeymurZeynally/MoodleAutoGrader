// Package db is the database schema. migrations/ is the source of truth: goose applies the files in order and each
// one is written once and never edited after it ran. schema/ is generated from a database migrated from scratch
// (`make schema`), one file per object, so the current shape of every table can be read without replaying
// the history. Never edit schema/ by hand.
//
// Schemas (one per module): identity, moodle, settings, integrity, ai, grading.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
