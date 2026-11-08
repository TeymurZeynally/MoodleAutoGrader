// Thin API client. The session lives in an HttpOnly cookie; JavaScript never sees the Moodle token.
export interface Retry { activity: string; attempt: number; error: string }
export interface Progress {
  works: number; works_processed: number; files: number; files_downloaded: number; bytes: number; bytes_downloaded: number
  started_at: string | null; last_activity_at: string | null; stage: string; done: number; total: number; pending: number; retrying: Retry[] | null
}

export interface Course { id: number; moodle_id: number; name: string; connected: boolean; sync_status: string; sync_error: string | null; last_synced_at: string | null; progress: Progress }
export interface AIProgress { queued: number; running: number; done: number; needs_attention: number; failed: number; has_checklist: boolean }
export interface Assignment { id: number; name: string; due_at: string | null; max_grade: number | null; marking_workflow: boolean; submitted: number; flagged: number; published: number; ai: AIProgress }
// Rerun says whether a re-analysis waits for the check of the assignment that is running now.
export interface Rerun { queued: boolean }
export interface AssignmentHeader { id: number; name: string; due_at: string | null; max_grade: number | null; course_id: number; course_name: string }
export interface Match { other_id: number; student: string; short_name: string; groups: string[]; submitted_at: string | null; code_similarity: number; report_similarity: number; identical_files: number; severity: string }
// MoodleAttempt is one attempt of a student as Moodle has it; attempt counts from 0.
export interface MoodleAttempt { attempt: number; status: string; submitted_at: string | null; files: string[]; grade: number | null; graded_at: string | null; comment: string; submission_id: number | null }

export interface Submission {
  id: number; student_id: number; moodle_user_id: number; student: string; short_name: string; enrolled: boolean; moodle_grade: number | null; graded_at: string | null; attempts: MoodleAttempt[]; groups: string[]; moodle_status: string; submitted_at: string | null; files: string[]; flags: string[]; integrity: 'ok' | 'medium' | 'high'
  points: number | null; comment: string; source: string; version: number; state: string; publish_error: string | null; matches: Match[] | null
  ai: AISummary | null; late: boolean; attempt: number; resubmitted: boolean; open_attempt: number | null; open_status: string
}

export interface AISummary { status: 'queued' | 'running' | 'done' | 'needs_attention' | 'failed'; points: number | null; met: number; total: number; extra: boolean; score: number | null; late: number }
export type Place = 'report' | 'screenshots' | 'code' | 'archive'
export interface Criterion { id: string; text: string; where: Place; points: number }
export interface Evidence { source: 'code' | 'report' | 'screenshot'; file?: string; quote: string; line?: number; verified: boolean }
export type CriterionResult = Criterion & { status: 'met' | 'partial' | 'not_met' | 'unverified'; no_quote?: boolean; awarded: number; note: string; evidence: Evidence[] | null }
export interface AIResult {
  criteria: CriterionResult[] | null; extras: CriterionResult[] | null; pages: { source: string; transcript: string; description: string }[] | null
  injection: { suspected: boolean; unverified?: boolean; quotes: string[] | null; explanation: string }; points: number; score: number; late_penalty: number; max: number; attention: string[] | null; truncated: boolean; model: string; step_models?: Record<string, string>
}

export interface Assessment { status: AISummary['status']; model: string | null; points: number | null; comment: string | null; error: string | null; finished_at: string | null; result: AIResult | null }
export interface Checklist {
  checklist: { id: number; source: 'model' | 'teacher'; model: string; criteria: Criterion[]; extras: Criterion[] | null; created_at: string; approved: boolean } | null
  running: boolean; progress: { stage: string; Done: number; Total: number } | null; notes: string; max_grade: number | null; description: string; settings: CourseSettings; models_problem: string
}

// CourseSettings are the rules of a course (domain/course on the server); defaults apply until the teacher saves them.
export interface CourseSettings {
  track_deadlines: boolean; late_penalty_enabled: boolean; late_penalty: number; late_notice: string
  require_code: boolean; extras_enabled: boolean
  max_images: number; partial_credit: number; points_step: number
  similar_percent: number; copy_percent: number; copy_identical_files: number; template_min_works: number; template_share: number
  code_extensions: string[]; ignored_dirs: string[]
}

// Run is one AI check of a work; experiments tried other models and do not replace the current result.
export interface Run { id: number; status: 'done' | 'needs_attention' | 'failed'; experiment: boolean; current: boolean; models: string; step_models: Record<string, string>; points: number | null; comment: string | null; error: string | null; started_at: string | null; finished_at: string; result: AIResult | null }
export interface HistoryEvent { kind: 'upload' | 'ai' | 'published'; at: string; attempt: number; points: number | null; comment: string | null; by: string | null; details: { score?: number; late_penalty?: number; met?: number; total?: number; status?: string } }
export interface PromptPart { body: string; default: string; edited: { by: string | null; at: string | null } | null }
export interface PromptStep { key: string; title: string; vars: string[]; required: string[] | null; system: PromptPart; user: PromptPart; model: PromptPart }
export interface PreviewStep { key: string; title: string; system: string; user: string }

export interface Line { text: string; mark?: 'match' | 'common' }
export interface FileView { path: string; lines: Line[]; matched: number; map?: number[] }
export interface Diff { pairs: { a: FileView; b: FileView | null }[]; only_b: string[] | null; report_a: FileView; report_b: FileView }

export interface UploadedFile { id: number; name: string; size: number; kind: string }
export interface Entry { path: string; size: number; kind: string; note?: string }

export interface AIActivity { kind: 'checklist' | 'work'; assignment_id: number | null; assignment: string | null; course_id: number | null; submission_id: number | null; student: string | null; step: string; started_at: string; updated_at: string }
export interface AIStatus { enabled: boolean; activity: AIActivity | null; queued: number }

export class ApiError extends Error { constructor(public status: number, message: string) { super(message) } }

// errorOf is the message of a failed reply: the API's own text, or the HTTP status text when the body is not JSON.
async function errorOf(res: Response) {
  const body = (await res.json().catch(() => null)) as { error?: string } | null
  return body?.error ?? res.statusText
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Autograder': '1' }, body: body === undefined ? undefined : JSON.stringify(body) })
  if (!res.ok) throw new ApiError(res.status, await errorOf(res))
  const text = await res.text() // 202/204 and other bodiless replies are fine
  return (text ? JSON.parse(text) : undefined) as T
}

export const api = {
  site: () => call<{ url: string }>('GET', '/api/site'),
  login: (creds: { site?: string; token?: string; username?: string; password?: string }) => call<{ name: string; site: string }>('POST', '/api/login', creds),
  logout: () => call<void>('POST', '/api/logout'),
  me: () => call<{ user_id: number; name: string }>('GET', '/api/me'),
  courses: (available = false) => call<Course[]>('GET', `/api/courses${available ? '?available=1' : ''}`),
  connect: (id: number, on: boolean) => call<void>('POST', `/api/courses/${id}/${on ? 'connect' : 'disconnect'}`),
  sync: (id: number) => call<void>('POST', `/api/courses/${id}/sync`),
  assignments: (courseId: number) => call<Assignment[]>('GET', `/api/courses/${courseId}/assignments`),
  submissions: (assignmentId: number) => call<{ assignment: AssignmentHeader; submissions: Submission[]; my_groups: string[]; moodle_grader_url: string }>('GET', `/api/assignments/${assignmentId}/submissions`),
  saveDraft: (id: number, points: number | null, comment: string, version: number) => call<void>('PUT', `/api/submissions/${id}/draft`, { points, comment, version }),
  compare: (id: number, other: number) => call<{ a: string; b: string; method: 'dolos' | 'fingerprints'; diff: Diff }>('GET', `/api/submissions/${id}/compare/${other}`),
  files: (id: number) => call<UploadedFile[]>('GET', `/api/submissions/${id}/files`),
  entries: (id: number, file: number, inner: string[]) => call<Entry[]>('GET', `/api/submissions/${id}/files/${file}/list${query(inner)}`),
  assessment: (id: number) => call<Assessment>('GET', `/api/submissions/${id}/assessment`),
  reassess: (id: number, models?: Record<string, string>) => call<void>('POST', `/api/submissions/${id}/assess`, models ? { models } : undefined),
  checklist: (assignmentId: number) => call<Checklist>('GET', `/api/assignments/${assignmentId}/checklist`),
  reassessAll: (assignmentId: number, newChecklist: boolean, checklistOnly = false, includeGraded = false) => call<Rerun>('POST', `/api/assignments/${assignmentId}/assess`, { new_checklist: newChecklist, checklist_only: checklistOnly, include_graded: includeGraded }),
  reassessFailed: (assignmentId: number, models?: Record<string, string>) => call<Rerun>('POST', `/api/assignments/${assignmentId}/assess`, { failed_only: true, models }),
  reassessSelected: (assignmentId: number, ids: number[]) => call<Rerun>('POST', `/api/assignments/${assignmentId}/assess`, { submission_ids: ids }),
  stopAssess: (assignmentId: number) => call<void>('POST', `/api/assignments/${assignmentId}/assess/stop`),
  runs: (submissionId: number) => call<Run[]>('GET', `/api/submissions/${submissionId}/runs`),
  experiment: (submissionId: number, models: Record<string, string>) => call<void>('POST', `/api/submissions/${submissionId}/experiment`, { models }),
  promoteRun: (runId: number) => call<void>('POST', `/api/runs/${runId}/promote`),
  history: (submissionId: number) => call<HistoryEvent[]>('GET', `/api/submissions/${submissionId}/history`),
  courseSettings: (courseId: number) => call<CourseSettings>('GET', `/api/courses/${courseId}/settings`),
  saveCourseSettings: (courseId: number, s: CourseSettings) => call<void>('PUT', `/api/courses/${courseId}/settings`, s),
  saveChecklist: (assignmentId: number, criteria: { text: string; where: Place; points: number }[], extras: { text: string; where: Place }[]) => call<void>('PUT', `/api/assignments/${assignmentId}/checklist`, { criteria, extras }),
  approveChecklist: (assignmentId: number) => call<void>('POST', `/api/assignments/${assignmentId}/checklist/approve`),
  saveNotes: (assignmentId: number, notes: string) => call<void>('PUT', `/api/assignments/${assignmentId}/notes`, { notes }),
  promptPreview: (assignmentId: number) => call<PreviewStep[]>('GET', `/api/assignments/${assignmentId}/prompt-preview`),
  prompts: (courseId: number) => call<PromptStep[]>('GET', `/api/courses/${courseId}/prompts`),
  aiStatus: () => call<AIStatus>('GET', '/api/ai/status'),
  models: () => call<string[]>('GET', '/api/models'),
  savePrompt: (courseId: number, key: string, body: string) => call<void>('PUT', `/api/courses/${courseId}/prompts/${key}`, { body }),
  publish: (ids: number[]) => call<{ approvals: number[] }>('POST', '/api/publish', { submission_ids: ids }),
}

const query = (inner: string[], extra = '') => { const q = new URLSearchParams(inner.map(p => ['p', p])); if (extra) q.set('as', extra); const s = q.toString(); return s ? `?${s}` : '' }
export const rawUrl = (id: number, file: number, inner: string[], as = '') => `/api/submissions/${id}/files/${file}/raw${query(inner, as)}`
export async function fetchRaw(url: string) {
  const res = await fetch(url, { credentials: 'same-origin' })
  if (!res.ok) throw new ApiError(res.status, await errorOf(res))
  return res
}

export const size = (b: number) => (b < 1024 ? `${b} B` : b < 1048576 ? `${(b / 1024).toFixed(0)} KB` : `${(b / 1048576).toFixed(1)} MB`)
export const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))
export const fmtDate = (s: string | null) => (s ? new Date(s).toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'short' }) : '—')
export const mb = (b: number) => `${(b / 1048576).toFixed(1)} MB`
export const pct = (x: number) => Math.round(x * 100)

// "3 s", "2 min 05 s", "1 h 12 min"
export function duration(ms: number) {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s} s`
  if (s < 3600) return `${Math.floor(s / 60)} min ${String(s % 60).padStart(2, '0')} s`
  return `${Math.floor(s / 3600)} h ${Math.floor((s % 3600) / 60)} min`
}

// lateBy says how late an upload was: "3 d 4 h", "5 h 20 min", "12 min".
export function lateBy(submitted: string | null, due: string | null) {
  if (!submitted || !due) return ''
  const min = Math.max(0, Math.round((new Date(submitted).getTime() - new Date(due).getTime()) / 60000))
  const d = Math.floor(min / 1440), h = Math.floor((min % 1440) / 60), m = min % 60
  return d ? `${d} d${h ? ` ${h} h` : ''}` : h ? `${h} h${m ? ` ${m} min` : ''}` : `${m} min`
}

export const ago = (s: string | null, now = Date.now()) => (s ? `${duration(now - new Date(s).getTime())} ago` : 'never')
