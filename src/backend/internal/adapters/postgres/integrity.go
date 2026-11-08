package postgres

import (
	"context"

	"moodle-autograder/internal/app/comparing"
	"moodle-autograder/internal/domain/integrity"

	"github.com/jackc/pgx/v5"
)

// Schema integrity: fingerprints of works, similar pairs and their matching code regions.

func (s *Store) SaveFeatures(ctx context.Context, submissionID int64, f integrity.Features, flags []string) error {
	_, err := s.db.Exec(ctx, `insert into integrity.submission_features (submission_id, file_hashes, code_shingles, report_shingles, code_files, report_chars, flags)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (submission_id) do update set file_hashes = excluded.file_hashes, code_shingles = excluded.code_shingles, report_shingles = excluded.report_shingles,
		code_files = excluded.code_files, report_chars = excluded.report_chars, flags = excluded.flags, extracted_at = now()`,
		submissionID, nonNil(f.FileHashes), nonNil(f.CodeShingles), nonNil(f.ReportShingles), f.CodeFiles, f.ReportChars, nonNil(flags))
	if err != nil {
		return wrap("save features", err)
	}

	_, err = s.db.Exec(ctx, `update moodle.submissions set pipeline_state = 'INGESTED' where id = $1`, submissionID)
	return wrap("mark ingested", err)
}

func (s *Store) AssignmentFeatures(ctx context.Context, assignmentID int64) ([]comparing.Features, error) {
	rows, err := s.db.Query(ctx, `select s.id, s.student_id, st.full_name, s.submitted_at, f.flags, f.file_hashes, f.code_shingles, f.report_shingles, f.code_files, f.report_chars
		from moodle.submissions s join moodle.students st on st.id = s.student_id join integrity.submission_features f on f.submission_id = s.id
		where s.assignment_id = $1 and s.moodle_status = 'submitted' order by s.submitted_at nulls last, s.id`, assignmentID)
	if err != nil {
		return nil, wrap("features", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (comparing.Features, error) {
		var x comparing.Features
		err := r.Scan(&x.SubmissionID, &x.StudentID, &x.Student, &x.SubmittedAt, &x.Flags, &x.FileHashes, &x.CodeShingles, &x.ReportShingles, &x.CodeFiles, &x.ReportChars)
		return x, err
	})
}

// ReplaceFragments stores the Dolos regions of an assignment, replacing the previous run.
func (s *Store) ReplaceFragments(ctx context.Context, assignmentID int64, rows []comparing.Fragment) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `delete from integrity.code_fragments where assignment_id = $1`, assignmentID); err != nil {
		return wrap("clear fragments", err)
	}

	_, err = tx.CopyFrom(ctx, pgx.Identifier{"integrity", "code_fragments"}, []string{"assignment_id", "submission_id", "other_id", "file_a", "start_a", "end_a", "file_b", "start_b", "end_b"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			r := rows[i]
			return []any{assignmentID, r.SubmissionID, r.OtherID, r.FileA, r.StartA, r.EndA, r.FileB, r.StartB, r.EndB}, nil
		}))
	if err != nil {
		return wrap("insert fragments", err)
	}

	return wrap("commit fragments", tx.Commit(ctx))
}

// Fragments returns the stored regions of a pair; ok is false when the pair was not checked with Dolos.
func (s *Store) Fragments(ctx context.Context, submissionID, otherID int64) ([]integrity.Region, bool, error) {
	rows, err := s.db.Query(ctx, `select file_a, start_a, end_a, file_b, start_b, end_b from integrity.code_fragments
		where submission_id = $1 and other_id = $2 order by file_a, start_a`, submissionID, otherID)
	if err != nil {
		return nil, false, wrap("fragments", err)
	}

	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (integrity.Region, error) {
		var x integrity.Region
		err := r.Scan(&x.FileA, &x.StartA, &x.EndA, &x.FileB, &x.StartB, &x.EndB)
		return x, err
	})
	return out, len(out) > 0, wrap("scan fragments", err)
}

func (s *Store) ReplaceMatches(ctx context.Context, assignmentID int64, matches []comparing.Match) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return wrap("begin", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `delete from integrity.integrity_matches where assignment_id = $1`, assignmentID); err != nil {
		return wrap("clear matches", err)
	}

	for _, m := range matches {
		if _, err := tx.Exec(ctx, `insert into integrity.integrity_matches (assignment_id, submission_id, other_submission_id, code_similarity, report_similarity, identical_files, severity)
			values ($1, $2, $3, $4, $5, $6, $7)`, assignmentID, m.SubmissionID, m.OtherID, m.CodeSimilarity, m.ReportSimilarity, m.IdenticalFiles, m.Severity); err != nil {
			return wrap("insert match", err)
		}
	}

	return wrap("commit", tx.Commit(ctx))
}
