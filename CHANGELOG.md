# Changelog

What changed in each version, and what to do after an update. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). While the version starts with 0, a version with fixes only raises the last number; a version with a new feature, a database migration or a step to take after the update raises the middle one.

## [0.3.0] - 2026-11-26

### Added

- The AI check of a course can cover only chosen groups (**Settings → AI check → Groups**). Works of the other groups are still downloaded, shown and compared by the similarity check; the AI analyzes them only when you choose them yourself.
- The AI check can be turned off for single assignments, in the **AI check** column of the **Assignments** tab, or for every assignment at once from the menu of that column. Such an assignment gets no checklist and none of its works is analyzed.
- Word documents (DOCX and the older DOC) are laid out as pages, as Word shows them, by LibreOffice in the `tools` container. The file viewer shows them as pages, and the AI check reads their pages like those of a PDF, so pictures and diagrams drawn with shapes in a document reach the model.
- **Read every page**, a course setting (**Settings → AI check**). The model reads every page of a PDF or Word report as a picture, not only the pages with images, so diagrams drawn with lines and shapes are seen. Each page counts towards **Pictures per work**.
- **Export** on the page of an assignment: the sheet as an Excel workbook for the teacher of the course. A row per submitted work with a link to its grading page in Moodle, its originality and the works it is similar to, a column per criterion with the points awarded, the draft and the comment for the student; a second sheet with the AI's reason and the quote for every verdict, a block of rows per student.

### Fixed

- The AI check read only the first 16000 bytes of a report, about four pages of Russian text, and 1500 bytes of the text on each picture: a longer report was checked by its beginning and marked "too large for the model". A report is now read whole (up to 200000 bytes, plus what the code of the work leaves of its budget), and so is the text on a picture (up to 8000 bytes); a work that does not fit into the model is tried again with less, as before.

### Upgrade notes

- The database migration `00003` adds the new settings; `docker compose up -d --build` applies it. The AI check stays on for every group and every assignment and reads the pages with images, as before.
- The `tools` image now holds LibreOffice: it is about 450 MB larger, and its first build after the update takes a few minutes longer.
- Works in Word documents that the AI already checked are read the new way on **Re-analyze**.

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

[0.3.0]: https://github.com/TeymurZeynally/MoodleAutoGrader/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/TeymurZeynally/MoodleAutoGrader/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/TeymurZeynally/MoodleAutoGrader/releases/tag/v0.1.0
