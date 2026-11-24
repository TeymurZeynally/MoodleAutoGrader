-- A course may keep only grades, comments and attempts from Moodle (its sync downloads no file), and may leave its works
-- to the teacher alone (no AI check).

-- +goose Up
alter table settings.course_settings add column download_files boolean not null default true,
    add column ai_check boolean not null default true;

-- +goose Down
alter table settings.course_settings drop column ai_check, drop column download_files;
