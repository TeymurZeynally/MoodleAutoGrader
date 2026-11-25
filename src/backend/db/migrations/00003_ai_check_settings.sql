-- The AI check of a course may cover only some of its groups (empty: every group) and may be off for single
-- assignments; downloads, the similarity check and the grade sheets still cover them all. The model may read every page
-- of a report as a picture, not only the pages with images: diagrams drawn with lines and shapes have no image in them.

-- +goose Up
alter table settings.course_settings add column ai_groups text[] not null default '{}';
alter table settings.course_settings add column read_all_pages boolean not null default false;

create table settings.assignment_settings (
    assignment_id bigint primary key references moodle.assignments (id) on delete cascade,
    ai_check boolean not null default true
);

-- +goose Down
drop table settings.assignment_settings;
alter table settings.course_settings drop column read_all_pages;
alter table settings.course_settings drop column ai_groups;
