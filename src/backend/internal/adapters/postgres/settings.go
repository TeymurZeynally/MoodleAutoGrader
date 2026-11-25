package postgres

import (
	"context"
	"errors"
	"time"

	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/domain/course"

	"github.com/jackc/pgx/v5"
)

// Schema settings: the Moodle site of the installation, and the rules and prompts a teacher sets per course. A NULL rule
// means the default (domain/course.Defaults).

// MoodleSite is the site the first sign-in set and whether its certificate goes unchecked; empty before it.
func (s *Store) MoodleSite(ctx context.Context) (string, bool, error) {
	var site string
	var skipVerify bool
	err := s.db.QueryRow(ctx, `select url, skip_tls_verify from settings.site`).Scan(&site, &skipVerify)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	return site, skipVerify, wrap("moodle site", err)
}

// SetMoodleSite keeps url and skipVerify when no site is kept yet, and returns the site kept now.
func (s *Store) SetMoodleSite(ctx context.Context, url string, skipVerify bool) (string, bool, error) {
	var kept string
	var keptSkip bool
	err := s.db.QueryRow(ctx, `with put as (insert into settings.site (url, skip_tls_verify) values ($1, $2) on conflict do nothing returning url, skip_tls_verify)
		select url, skip_tls_verify from put union all select url, skip_tls_verify from settings.site limit 1`, url, skipVerify).Scan(&kept, &keptSkip)
	return kept, keptSkip, wrap("set moodle site", err)
}

func (s *Store) CourseSettings(ctx context.Context, courseID int64) (course.Settings, error) {
	c := course.Defaults()
	var notice *string
	var images, copyFiles, minWorks *int
	var partial, step, similar, copyPct, share *float64
	var code, ignored []string
	err := s.db.QueryRow(ctx, `select track_deadlines, late_penalty_enabled, late_penalty::float8, require_code, extras_enabled,
		late_notice, max_images, partial_credit::float8, points_step::float8, similar_percent::float8, copy_percent::float8, copy_identical_files,
		template_min_works, template_share::float8, code_extensions, ignored_dirs, download_files, ai_check, ai_groups, read_all_pages
		from settings.course_settings where course_id = $1`, courseID).
		Scan(&c.TrackDeadlines, &c.LatePenaltyEnabled, &c.LatePenalty, &c.RequireCode, &c.ExtrasEnabled,
			&notice, &images, &partial, &step, &similar, &copyPct, &copyFiles, &minWorks, &share, &code, &ignored, &c.DownloadFiles, &c.AICheck, &c.AIGroups, &c.ReadAllPages)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}

	if err != nil {
		return c, wrap("course settings", err)
	}

	set(&c.LateNotice, notice)
	set(&c.MaxImages, images)
	set(&c.PartialCredit, partial)
	set(&c.PointsStep, step)
	set(&c.SimilarPercent, similar)
	set(&c.CopyPercent, copyPct)
	set(&c.CopyIdenticalFiles, copyFiles)
	set(&c.TemplateMinWorks, minWorks)
	set(&c.TemplateShare, share)
	if code != nil {
		c.CodeExtensions = code
	}

	if ignored != nil {
		c.IgnoredDirs = ignored
	}

	return c, nil
}

// set keeps the default when the column is NULL.
func set[T any](dst, v *T) {
	if v != nil {
		*dst = *v
	}
}

func (s *Store) SaveCourseSettings(ctx context.Context, courseID, userID int64, c course.Settings) error {
	_, err := s.db.Exec(ctx, `insert into settings.course_settings (course_id, track_deadlines, late_penalty_enabled, late_penalty, require_code, extras_enabled,
		late_notice, max_images, partial_credit, points_step, similar_percent, copy_percent, copy_identical_files, template_min_works, template_share,
		code_extensions, ignored_dirs, download_files, ai_check, ai_groups, read_all_pages, updated_by)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
		on conflict (course_id) do update set track_deadlines = excluded.track_deadlines, late_penalty_enabled = excluded.late_penalty_enabled,
		late_penalty = excluded.late_penalty, require_code = excluded.require_code, extras_enabled = excluded.extras_enabled,
		late_notice = excluded.late_notice, max_images = excluded.max_images, partial_credit = excluded.partial_credit, points_step = excluded.points_step,
		similar_percent = excluded.similar_percent, copy_percent = excluded.copy_percent, copy_identical_files = excluded.copy_identical_files,
		template_min_works = excluded.template_min_works, template_share = excluded.template_share,
		code_extensions = excluded.code_extensions, ignored_dirs = excluded.ignored_dirs, download_files = excluded.download_files, ai_check = excluded.ai_check,
		ai_groups = excluded.ai_groups, read_all_pages = excluded.read_all_pages, updated_by = excluded.updated_by, updated_at = now()`,
		courseID, c.TrackDeadlines, c.LatePenaltyEnabled, c.LatePenalty, c.RequireCode, c.ExtrasEnabled,
		c.LateNotice, c.MaxImages, c.PartialCredit, c.PointsStep, c.SimilarPercent, c.CopyPercent, c.CopyIdenticalFiles, c.TemplateMinWorks, c.TemplateShare,
		nonNil(c.CodeExtensions), nonNil(c.IgnoredDirs), c.DownloadFiles, c.AICheck, nonNil(c.AIGroups), c.ReadAllPages, userID)
	return wrap("save course settings", err)
}

// AssignmentAICheck reports whether the AI check is on for the assignment; it is until the teacher turns it off.
func (s *Store) AssignmentAICheck(ctx context.Context, assignmentID int64) (bool, error) {
	on := true
	err := s.db.QueryRow(ctx, `select ai_check from settings.assignment_settings where assignment_id = $1`, assignmentID).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}

	return on, wrap("assignment ai check", err)
}

func (s *Store) SetAssignmentAICheck(ctx context.Context, assignmentID int64, on bool) error {
	_, err := s.db.Exec(ctx, `insert into settings.assignment_settings (assignment_id, ai_check) values ($1, $2)
		on conflict (assignment_id) do update set ai_check = excluded.ai_check`, assignmentID, on)
	return wrap("set assignment ai check", err)
}

func (s *Store) PromptOverrides(ctx context.Context, courseID int64) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `select key, body from settings.prompt_overrides where course_id = $1`, courseID)
	if err != nil {
		return nil, wrap("prompt overrides", err)
	}

	out := map[string]string{}
	_, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct{}, error) {
		var k, v string
		err := r.Scan(&k, &v)
		out[k] = v
		return struct{}{}, err
	})
	return out, wrap("scan prompt overrides", err)
}

func (s *Store) PromptEdits(ctx context.Context, courseID int64) (map[string]assessing.PromptEdit, error) {
	rows, err := s.db.Query(ctx, `select p.key, u.full_name, p.updated_at from settings.prompt_overrides p left join identity.users u on u.id = p.updated_by
		where p.course_id = $1`, courseID)
	if err != nil {
		return nil, wrap("prompt edits", err)
	}

	out := map[string]assessing.PromptEdit{}
	_, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct{}, error) {
		var k string
		var by *string
		var at *time.Time
		err := r.Scan(&k, &by, &at)
		out[k] = assessing.PromptEdit{By: by, At: at}
		return struct{}{}, err
	})
	return out, wrap("scan prompt edits", err)
}

// SetPrompt stores the course's version of a prompt; an empty body restores the default.
func (s *Store) SetPrompt(ctx context.Context, courseID int64, key, body string, userID int64) error {
	if body == "" {
		_, err := s.db.Exec(ctx, `delete from settings.prompt_overrides where course_id = $1 and key = $2`, courseID, key)
		return wrap("reset prompt", err)
	}

	_, err := s.db.Exec(ctx, `insert into settings.prompt_overrides (course_id, key, body, updated_by) values ($1, $2, $3, $4)
		on conflict (course_id, key) do update set body = excluded.body, updated_by = excluded.updated_by, updated_at = now()`, courseID, key, body, userID)
	return wrap("set prompt", err)
}
