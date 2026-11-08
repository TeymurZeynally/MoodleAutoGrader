<p align="right"><b>English</b> · <a href="README.ru.md">Русский</a></p>

<div align="center">

# Moodle Auto Grader

**A whole Moodle assignment on one screen: plagiarism found, every file readable, draft grades and comments from an AI model that runs on your own machine. Nothing reaches Moodle until you approve it.**

![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)
![React](https://img.shields.io/badge/React-UI-61DAFB?logo=react&logoColor=black)
![Docker Compose](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)
![Ollama](https://img.shields.io/badge/AI-local%20models-000000?logo=ollama&logoColor=white)

![The grade sheet of an assignment](assets/screenshots/grade-sheet.png)

<sub>All screenshots show a generated course: the students and their works are synthetic.</sub>

</div>

## Why

Grading a hundred lab works means opening a hundred archives, reading a hundred reports and hoping you remember the one from last year that looks the same. MoodleAutoGrader downloads the submissions of an assignment, compares every work with every other one in the course (all groups, past years too), shows all the files in the browser and lets a local AI model draft a grade and a comment against a checklist you approved. You go through the sheet, fix what you disagree with and publish. Student work never leaves your computer.

## What you get

- **Plagiarism across the whole course.** Every work is compared with every other: code by [Dolos](https://dolos.ugent.be) (renaming variables does not hide a copy), reports by their text, identical files by checksum. Code that most works share, such as starter code, is not counted. A side-by-side view highlights what matches.
- **Every submission in the browser.** PDF, archives (zip, rar, 7z, an archive inside an archive), source code with line numbers, images, Word documents. Nothing to download and unpack.
- **An AI check against your checklist.** The model drafts a checklist from the assignment description, you edit and approve it. For each work the model marks every criterion and must quote the work as evidence; the code sums the points from these verdicts. Then the model suggests a comment for the student.
- **Works that try to talk to the grader are caught.** A request for full marks in the report, the code or on a screenshot is found, quoted and not graded: the work is marked for you. Text a person does not see (hidden, white on white, tiny, invisible characters) is left out of the AI check and flagged.
- **A grade sheet that does the bookkeeping.** Points, comments, integrity, AI result, late penalty, the grade already in Moodle, earlier attempts with their comments. Filters for what is left to do; publish a selection to Moodle in one click.
- **Local and private.** Models run in [Ollama](https://ollama.com) on your machine, on the CPU or an NVIDIA GPU. No cloud service, no API key.
- **For several teachers.** Everyone signs in with their own Moodle account and sees the courses where Moodle lets them grade.

## Quick start

You need [Docker](https://docs.docker.com/engine/install/) with Compose and a teacher's account on a Moodle site where the Moodle mobile app is allowed (sign-in uses its token).

**1. Start**

```bash
git clone https://github.com/<owner>/MoodleAutoGrader.git
cd MoodleAutoGrader/deploy
docker compose up -d --build
```

Open **http://autograder.localhost:8180**, enter the address of your Moodle site and sign in with your Moodle username and password (exchanged for a token, never stored) or with a token. The first sign-in keeps the address for everyone who uses this installation.

<img src="assets/screenshots/sign-in.png" alt="The sign-in page of a new installation" width="520">

Connect a course and press **Sync now**: the grade sheet, the plagiarism check, the file viewer and publishing all work now.

**2. Add AI models**

```bash
docker compose run --rm ollama-cli pull gemma4:e4b
```

Then, in the course: **AI prompts** tab → choose the model for each step → **Sync now**. On the assignment page the generated checklist appears: edit it if you like, press **Approve checklist**, then **Re-analyze all**.

A GPU is optional: on the CPU the check is just slower. See [Models](#models) for a GPU and other models.

## Screenshots

<table>
<tr>
<td width="50%"><img src="assets/screenshots/ai-result.png" width="100%" alt="The AI check of one work"><br><b>The AI check of a work.</b> Each criterion with its verdict and the quote from the work that proves it; the points are summed by code.</td>
<td width="50%"><img src="assets/screenshots/injection.png" width="100%" alt="A caught prompt injection"><br><b>A work that asks the grader for full marks.</b> Found, quoted, not graded and left for the teacher.</td>
</tr>
<tr>
<td><img src="assets/screenshots/compare.png" width="100%" alt="Two works side by side"><br><b>Two works side by side.</b> Matching code and report lines are highlighted, starter code is grey.</td>
<td><img src="assets/screenshots/files.png" width="100%" alt="The file viewer"><br><b>The file viewer.</b> An archive as a tree, code with line numbers, PDF pages, nested archives.</td>
</tr>
<tr>
<td><img src="assets/screenshots/prompts.png" width="100%" alt="Prompts and models of a course"><br><b>Prompts and models of a course.</b> Every prompt can be edited; each step can use its own model.</td>
<td><img src="assets/screenshots/settings.png" width="100%" alt="Settings of a course"><br><b>Settings of a course.</b> Deadlines and late penalty, what a work must contain, similarity thresholds, file types.</td>
</tr>
<tr>
<td colspan="2"><img src="assets/screenshots/checklist.png" width="100%" alt="The checklist of an assignment"><br><b>The checklist of an assignment.</b> Generated from the description, edited and approved by the teacher, with notes for the grader.</td>
</tr>
</table>

## How a work gets graded

1. **Sync.** On **Sync now** the course, its students, the submissions and the grades come from Moodle. Only files that changed since the last sync are downloaded.
2. **Reading.** Archives are listed and code is read; reports are turned into text; screenshots and PDF pages with pictures are prepared for the model.
3. **Plagiarism and flags.** Every work is compared with every other one of the assignment in the course. A work without code, without report text, with an executable, a suspicious archive or late is marked.
4. **AI check**, if a model is chosen and the checklist approved: the model reads the screenshots, a separate step looks for instructions aimed at the grader, then every criterion is checked with a quote and a comment is suggested. Works are checked one at a time, in the background, only when they are new or changed.
5. **You.** The sheet shows what needs your attention. You change points and comments, or accept the draft.
6. **Publish.** Selected grades and comments are sent to Moodle, exactly as you approved them.

## Models

Models are managed with the Ollama command line. It is a separate container with internet access that sees no student work; the model server itself has no internet access.

```bash
cd deploy
docker compose run --rm ollama-cli pull <model>   # download, e.g. gemma4:e4b
docker compose run --rm ollama-cli list           # what is downloaded
docker compose run --rm ollama-cli rm <model>     # delete
```

Any model from the [Ollama library](https://ollama.com/search) can be used. Choose them per course on the **AI prompts** tab; each step can have its own:

| Step | What the model does | What it needs |
|---|---|---|
| Checklist | drafts the checklist from the assignment description | any model |
| Screenshots | describes screenshots and PDF pages with pictures | a [vision model](https://ollama.com/search?c=vision) |
| Injection check | looks for text that tries to steer the grader | any model; a small one misses some, a weak one raises false alarms on honest works |
| Checklist check | marks every criterion with a quote | the larger, the better |
| Comment | writes the comment for the student | any model |

- **A GPU** (NVIDIA, with [nvidia-container-toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)): set `COMPOSE_FILE=docker-compose.yml:compose.gpu.yml` in `deploy/.env` and run `docker compose up -d`.
- **One model is loaded at a time**: different models per step cost loading time on every work.
- **Generation options** for your hardware (context size, temperature and so on) go to `deploy/seed/models.json`, see `deploy/seed.example/models.json`; `"*"` applies to every model. After a change: `docker compose restart app worker-core`.
- **A deleted model** that a course still uses is reported on the assignment page, with the command to download it.
- The default prompts write the comment for the student **in Russian**. Change the comment prompt on the **AI prompts** tab for another language.

## What you need

| | |
|---|---|
| **Docker** | Docker Engine with Compose v2. Tested on Linux. |
| **Disk** | About 12 GB for the images, plus the downloaded submissions and the models. |
| **Memory** | About 0.5 GB while idle without a model. A model needs a bit more than its file size: the 6.6 GB `gemma4:e4b` took 8.2 GB, a 3.4 GB model 4.2 GB. |
| **GPU** | Optional. Everything works on the CPU, the AI check is slower. |
| **Moodle** | A teacher's account that can grade the assignments, and the Moodle mobile app service enabled on the site. The functions used: `core_webservice_get_site_info`, `core_enrol_get_users_courses`, `core_enrol_get_enrolled_users`, `mod_assign_get_assignments`, `mod_assign_list_participants`, `mod_assign_get_submissions`, `mod_assign_get_grades`, `mod_assign_get_submission_status`, `gradereport_user_get_grade_items`, and `mod_assign_save_grade` to publish. |

## Configuration

Nothing has to be set. Optional settings go to `deploy/.env`: copy `.env.example` and uncomment what you need.

| Variable | Default | |
|---|---|---|
| `MOODLE_URL` | | Your Moodle site, set in advance. Without it the first sign-in asks for it; with it, the address typed at sign-in is not used. |
| `APP_PORT` | `8180` | Port of the web UI, on 127.0.0.1 only. |
| `TZ` | `UTC` | Local time in the comments for students. |
| `COOKIE_SECURE` | `0` | `1` behind an HTTPS proxy. |
| `COMPOSE_FILE` | | `docker-compose.yml:compose.gpu.yml` for a GPU. |
| `LLM_PROVIDER` | `ollama` | `none` turns the AI check off. |
| `OLLAMA_FLASH_ATTENTION`, `OLLAMA_KV_CACHE_TYPE` | | Ollama's own settings, to fit a larger context into video memory. |
| `APP_UID`, `APP_GID` | `1000` | The user the application runs as and who owns `deploy/secrets`. |

Passwords and keys are generated on the first start into `deploy/secrets`. Course settings (late penalty, similarity thresholds, file types, what counts as code) are on the **Settings** tab of each course.

## Everyday commands

Run them in `deploy`.

```bash
git pull && docker compose up -d --build      # update
docker compose stop                           # stop, keep everything
docker compose start                          # start again
docker compose exec -T postgres pg_dump -U autograder -d autograder -Fc > autograder.dump   # back up the database
docker compose down                           # remove the containers, keep the data
docker compose down -v                        # remove the data AND the downloaded models
```

The files of students can be downloaded again from Moodle by a sync; the database holds what is yours: drafts, checklists, prompts, settings. After a database is lost, a sync brings back the grades and comments that are in Moodle.

## Sharing approved checklists

A course's approved checklists, with the notes for the grader, the prompts, the models and the settings, can be passed to a colleague on the same Moodle site, or kept to load again after the database is lost.

**Export** a course from a running installation, by the course's id in Moodle:

```bash
cd deploy
mkdir -p seed && docker compose exec -T app /autograder seed-export 12345 | tar -xC seed
```

**Import:** put the folder `seed/12345` into `deploy/seed` of the other installation and press **Sync now** in the course. Only what the course does not have yet is loaded; what a teacher already changed there stays. The layout of the folder is in `deploy/seed.example`, where `12345` is the course's id in Moodle and `67890` the assignment's.

## Architecture

**A modular monolith, run as three processes split by privilege.** One codebase and one Go binary, one Postgres database with a schema per module, released and deployed together. The binary runs in three roles: `app` serves the web UI, `worker-moodle` talks to Moodle, `worker-core` reads the works and runs the checks. Each role gets only the secrets and networks its job needs: the process that reads student files holds no keys and has no internet access, and the processes that reach Moodle never parse those files. The roles work together through the database and Temporal; parsing, code comparison and the models run in containers of their own.

<img src="assets/components.svg" alt="The components of Moodle Auto Grader and the protocols between them">

| Process | Does | Session key | Internet | Reads student files |
|---|---|---|---|---|
| `migrate` | applies database migrations, prepares the role of the others, exits | no | no | no |
| `app` | web UI and API, starts background jobs | yes | yes | no |
| `worker-moodle` | reads from Moodle, publishes grades; stores files without reading them | yes | yes | no |
| `worker-core` | reads and compares works, runs the AI check, runs the workflows | no | no | yes |
| `tools` | reads PDFs, and archives and Word documents for the viewer | no | no | yes |
| `dolos`, `ollama` | code similarity, models | no | no | yes |

A sync and a publication go through [Temporal](https://temporal.io), so a restart in the middle loses nothing:

<img src="assets/sequence.svg" alt="A sync and a publication, step by step">

## Security

- The Moodle token lives in an encrypted HttpOnly cookie for 12 hours, and in the history of Temporal for the background jobs, encrypted with the same key but not usable as a cookie. It is not stored anywhere else.
- Only the web UI is published, on 127.0.0.1. Use `autograder.localhost` rather than `localhost`: a cookie for plain `localhost` would also go to any other program on a local port, for example a student's project. The other services have no published ports, but any process on this machine can reach them on the Docker network addresses, and anyone in the `docker` group controls the whole stack. Do not run students' code on this machine outside a container.
- Submissions are untrusted input. The application is split so that a file breaking a parser finds nothing to steal: `worker-core`, which reads them, has no session key and no internet; `app` and `worker-moodle`, which hold the key and reach Moodle, do not parse them; PDFs, and archives for the viewer, are read by `tools`, a container without secrets. Text from a work is treated as data by the AI check, and a teacher approves every grade. Text a person does not see in a report is left out of the AI check and flagged, and pictures are decoded and drawn again before a model sees them.
- The running processes connect to Postgres as a role that can only work with rows, and only add to the audit log. The password of the database owner is only given to the one-off `migrate` service.
- Every container runs with `no-new-privileges` and the default seccomp profile, and none gets the Docker socket. The application, the parsers and Dolos run as non-root users on read-only file systems, without Linux capabilities; the application image has no shell. Postgres, SeaweedFS and Temporal also run on read-only file systems as non-root users, keeping only the capabilities their images need to start. The model server runs without capabilities. Containers have memory and process limits; the model server has a process limit only.

## Development

- Backend: Go 1.27 in `src/backend`. Web UI: React + Vite in `src/web` (`npm run dev` proxies `/api` to the app on :8180).
- `make lint`, `make lint-fix`, `make test`, `make schema` run in Docker from the repository root. Database tests need `TEST_DATABASE_URL` of a disposable Postgres.
- The diagrams are plain SVG files in `assets/`.
