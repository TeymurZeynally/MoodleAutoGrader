# Changelog

What changed in each version, and what to do after an update. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). While the version starts with 0, a version with fixes only raises the last number; a version with a new feature, a database migration or a step to take after the update raises the middle one.

## [0.2.0] - 2026-11-24

### Added

- **Download files**, a course setting. Turned off, a sync copies only the grades, comments and attempts of the course: its files are not downloaded, and the file viewer, the similarity check and the AI check are off in that course.
- **AI check**, a course setting. Turned off, no checklist is built and no work is analyzed in that course; the similarity check stays.
- The **Students** tab of a course and the page of a student: every assignment of the student in one sheet, with the edits, Re-analyze and Publish of the sheet of an assignment. A student's name in the sheet of an assignment leads to their page.
- The tag **First upload on time** on a late work whose first upload with files came before the deadline. The late penalty still counts from the attempt shown.

### Changed

- **Points step** is in a new **Grades** section of the course settings.
- Re-analyzing chosen works of an assignment that has no checklist yet is refused: build the checklist on the page of the assignment first. Before, the check built a checklist nobody had seen.

### Fixed

- An archive is opened by its content, not by its name. A zip saved as `.rar` was unreadable, for the AI check and in the file viewer. A `.rar` or `.7z` file with other content is now flagged `type-mismatch`, as `.zip` and `.pdf` already were.

### Upgrade notes

- The database migration `00002` adds the two course settings; `docker compose up -d --build` applies it. Both are on, so every course works as before.
- In a course where works were flagged unreadable, press **Sync now**: a sync reads every work again. The AI check does not run again by itself, as the files did not change: re-analyze those works.

## [0.1.0] - 2026-11-18

The first public version.

### Added

- Sign-in with a Moodle teacher account. The site is set on the first sign-in; a site whose certificate does not verify can be accepted there.
- Sync of a course from Moodle on **Sync now**: assignments, students and groups, submissions with their files, grades, comments and attempts.
- Similarity check across the whole course: code by Dolos, reports by their text, identical files by checksum, with a side-by-side view.
- File viewer in the browser: PDF, zip, rar and 7z archives (nested too), source code, images, Word documents.
- AI check with local models in Ollama: a checklist drafted from the assignment description and approved by the teacher, a verdict with a quote for every criterion, a suggested comment. Works that address the grader and text a person does not see are flagged.
- Grade sheet with points, comments, late penalty, the grade in Moodle and earlier attempts; chosen grades are published to Moodle.
- Export and import of a course's approved checklists, prompts, models and settings.

[0.2.0]: https://github.com/TeymurZeynally/MoodleAutoGrader/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/TeymurZeynally/MoodleAutoGrader/releases/tag/v0.1.0
