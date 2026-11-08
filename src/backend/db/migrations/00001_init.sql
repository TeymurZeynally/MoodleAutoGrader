-- The database of Moodle Auto Grader: one schema per module. A change of the database is a new migration.

-- +goose Up
create schema identity;
create schema moodle;
create schema settings;
create schema integrity;
create schema ai;
create schema grading;

-- +goose StatementBegin
create function moodle.short_name(last_name text, first_name text, full_name text) returns text
    language sql immutable parallel safe
    as $$
    select case when last_name <> '' and first_name <> '' then last_name || ' ' ||
        (select string_agg(upper(left(w, 1)) || '.', ' ') from regexp_split_to_table(first_name, '\s+') w where w <> '')
    else full_name end
$$;
-- +goose StatementEnd

create table identity.users (
    id             bigint generated always as identity primary key,
    moodle_site    text not null,
    moodle_user_id bigint not null,
    full_name      text not null,
    created_at     timestamptz not null default now(),
    last_login_at  timestamptz,
    unique (moodle_site, moodle_user_id)
);

create table identity.audit_log (
    id        bigint generated always as identity primary key,
    at        timestamptz not null default now(),
    user_id   bigint references identity.users (id),
    action    text not null,
    entity    text not null,
    entity_id bigint,
    payload   jsonb not null default '{}'::jsonb
);

create table moodle.courses (
    id               bigint generated always as identity primary key,
    moodle_site      text not null,
    moodle_course_id bigint not null,
    full_name        text not null,
    connected_by     bigint references identity.users (id),
    connected_at     timestamptz,
    sync_mode        text not null default 'manual' check (sync_mode in ('manual', 'scheduled')),
    sync_interval    interval,
    last_synced_at   timestamptz,
    sync_status      text not null default 'idle',
    sync_error       text,
    sync_workflow_id text,
    sync_started_at  timestamptz,
    unique (moodle_site, moodle_course_id)
);

create table moodle.assignments (
    id               bigint generated always as identity primary key,
    course_id        bigint not null references moodle.courses (id) on delete cascade,
    moodle_assign_id bigint not null,
    cmid             bigint not null,
    name             text not null,
    due_at           timestamptz,
    max_grade        numeric,
    scale_id         bigint,
    marking_workflow boolean not null default false,
    check_enabled    boolean not null default true,
    intro            text not null default '',
    grader_notes     text not null default '',
    unique (course_id, moodle_assign_id)
);

create table moodle.course_memberships (
    user_id    bigint not null references identity.users (id) on delete cascade,
    course_id  bigint not null references moodle.courses (id) on delete cascade,
    can_grade  boolean not null,
    checked_at timestamptz not null default now(),
    groups     text[] not null default '{}'::text[],
    primary key (user_id, course_id)
);

create table moodle.moodle_attempts (
    assignment_id  bigint not null references moodle.assignments (id) on delete cascade,
    moodle_user_id bigint not null,
    read_mark      text not null,
    attempts       jsonb not null default '[]'::jsonb,
    primary key (assignment_id, moodle_user_id)
);

create table moodle.moodle_grades (
    assignment_id  bigint not null references moodle.assignments (id) on delete cascade,
    moodle_user_id bigint not null,
    grade          numeric not null,
    graded_at      timestamptz,
    primary key (assignment_id, moodle_user_id)
);

create table moodle.students (
    id             bigint generated always as identity primary key,
    course_id      bigint not null references moodle.courses (id) on delete cascade,
    moodle_user_id bigint not null,
    full_name      text not null,
    pseudonym      text not null,
    groups         text[] not null default '{}'::text[],
    enrolled       boolean not null default false,
    first_name     text not null default '',
    last_name      text not null default '',
    unique (course_id, moodle_user_id),
    unique (course_id, pseudonym)
);

create table moodle.submissions (
    id                   bigint generated always as identity primary key,
    assignment_id        bigint not null references moodle.assignments (id) on delete cascade,
    student_id           bigint not null references moodle.students (id) on delete cascade,
    moodle_submission_id bigint not null,
    attempt              integer not null,
    moodle_status        text not null,
    submitted_at         timestamptz,
    pipeline_state       text not null default 'NEW',
    workflow_id          text,
    synced_at            timestamptz not null default now(),
    unique (assignment_id, student_id, attempt)
);

create table moodle.submission_files (
    id                 bigint generated always as identity primary key,
    submission_id      bigint not null references moodle.submissions (id) on delete cascade,
    file_path          text not null,
    file_name          text not null,
    size_bytes         bigint not null,
    mime_type          text,
    moodle_modified_at timestamptz,
    object_key         text,
    sha256             text,
    downloaded_at      timestamptz,
    file_url           text not null default '',
    unique (submission_id, file_path, file_name)
);

create table settings.course_settings (
    course_id            bigint primary key references moodle.courses (id) on delete cascade,
    late_penalty_enabled boolean not null default true,
    late_penalty         numeric not null default 1,
    require_code         boolean not null default true,
    updated_by           bigint references identity.users (id) on delete set null,
    updated_at           timestamptz not null default now(),
    track_deadlines      boolean not null default true,
    extras_enabled       boolean not null default false,
    late_notice          text,
    max_images           integer,
    partial_credit       numeric,
    points_step          numeric,
    similar_percent      numeric,
    copy_percent         numeric,
    copy_identical_files integer,
    template_min_works   integer,
    template_share       numeric,
    code_extensions      text[],
    ignored_dirs         text[]
);

create table settings.prompt_overrides (
    course_id  bigint not null references moodle.courses (id) on delete cascade,
    key        text not null,
    body       text not null,
    updated_by bigint references identity.users (id) on delete set null,
    updated_at timestamptz not null default now(),
    primary key (course_id, key)
);

create table settings.site (
    id              boolean primary key default true check (id),
    url             text not null,
    set_at          timestamptz not null default now(),
    skip_tls_verify boolean not null default false -- the teacher chose it at the first sign-in, after the check of the certificate failed
);

create table integrity.code_fragments (
    assignment_id bigint not null references moodle.assignments (id) on delete cascade,
    submission_id bigint not null references moodle.submissions (id) on delete cascade,
    other_id      bigint not null references moodle.submissions (id) on delete cascade,
    file_a        text not null,
    start_a       integer not null,
    end_a         integer not null,
    file_b        text not null,
    start_b       integer not null,
    end_b         integer not null
);
create index code_fragments_pair_idx on integrity.code_fragments (submission_id, other_id);

create table integrity.integrity_matches (
    assignment_id       bigint not null references moodle.assignments (id) on delete cascade,
    submission_id       bigint not null references moodle.submissions (id) on delete cascade,
    other_submission_id bigint not null references moodle.submissions (id) on delete cascade,
    code_similarity     real not null,
    report_similarity   real not null,
    identical_files     integer not null,
    severity            text not null check (severity in ('high', 'medium')),
    primary key (submission_id, other_submission_id)
);

create table integrity.submission_features (
    submission_id   bigint primary key references moodle.submissions (id) on delete cascade,
    file_hashes     text[] not null default '{}'::text[],
    code_shingles   bigint[] not null default '{}'::bigint[],
    report_shingles bigint[] not null default '{}'::bigint[],
    code_files      integer not null default 0,
    report_chars    integer not null default 0,
    flags           text[] not null default '{}'::text[],
    extracted_at    timestamptz not null default now()
);

create table ai.ai_activity (
    id            integer primary key default 1 check (id = 1),
    kind          text not null,
    assignment_id bigint references moodle.assignments (id) on delete cascade,
    submission_id bigint references moodle.submissions (id) on delete cascade,
    step          text not null default '',
    started_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now()
);

create table ai.checklists (
    id            bigint generated always as identity primary key,
    assignment_id bigint not null references moodle.assignments (id) on delete cascade,
    intro_hash    text not null,
    model         text not null,
    criteria      jsonb not null,
    created_at    timestamptz not null default now(),
    source        text not null default 'model',
    created_by    bigint references identity.users (id) on delete set null,
    extras        jsonb not null default '[]'::jsonb,
    approved_at   timestamptz,
    approved_by   bigint references identity.users (id) on delete set null
);
create index checklists_assignment_idx on ai.checklists (assignment_id, id desc);

create table ai.assessment_runs (
    id            bigint generated always as identity primary key,
    submission_id bigint not null references moodle.submissions (id) on delete cascade,
    status        text not null,
    experiment    boolean not null default false,
    models        text not null default '',
    step_models   jsonb not null default '{}'::jsonb,
    checklist_id  bigint references ai.checklists (id) on delete set null,
    input_hash    text,
    result        jsonb,
    points        numeric,
    comment       text,
    error         text,
    started_at    timestamptz,
    finished_at   timestamptz not null default now()
);
create index assessment_runs_submission_idx on ai.assessment_runs (submission_id, finished_at);

create table ai.assessments (
    submission_id bigint primary key references moodle.submissions (id) on delete cascade,
    status        text not null,
    input_hash    text,
    checklist_id  bigint references ai.checklists (id) on delete set null,
    model         text,
    result        jsonb,
    points        numeric,
    comment       text,
    error         text,
    queued_at     timestamptz not null default now(),
    started_at    timestamptz,
    finished_at   timestamptz,
    run_id        bigint references ai.assessment_runs (id) on delete set null
);

create table grading.approvals (
    id            bigint generated always as identity primary key,
    submission_id bigint not null references moodle.submissions (id) on delete cascade,
    draft_version integer not null,
    points        numeric not null,
    comment       text not null,
    content_hash  text not null,
    approved_by   bigint not null references identity.users (id),
    approved_at   timestamptz not null default now(),
    published_at  timestamptz,
    unique (submission_id, draft_version)
);

create table grading.grade_drafts (
    submission_id bigint primary key references moodle.submissions (id) on delete cascade,
    version       integer not null default 1,
    points        numeric,
    comment       text not null default '',
    integrity     text not null default 'ok' check (integrity in ('ok', 'medium', 'high')),
    source        text not null default 'auto' check (source in ('auto', 'llm', 'teacher')),
    assessment    jsonb,
    state         text not null default 'draft' check (state in ('draft', 'approved', 'published', 'publish_failed')),
    publish_error text,
    edited_by     bigint references identity.users (id),
    updated_at    timestamptz not null default now()
);

create table grading.review_events (
    id            bigint generated always as identity primary key,
    submission_id bigint not null references moodle.submissions (id) on delete cascade,
    kind          text not null,
    at            timestamptz not null,
    points        numeric,
    comment       text,
    details       jsonb not null default '{}'::jsonb
);
create index review_events_submission_idx on grading.review_events (submission_id, at);

-- +goose Down
drop schema grading, ai, integrity, settings, moodle, identity cascade;
