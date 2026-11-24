import { useState } from 'react'
import { App, Button, Flex, Input, InputNumber, Popconfirm, Tag, Tooltip, Typography } from 'antd'
import type { TableColumnsType } from 'antd'
import { CloudUploadOutlined, CommentOutlined, ExportOutlined, FileOutlined, FilePdfOutlined, FileZipOutlined, FolderOpenOutlined, SaveOutlined } from '@ant-design/icons'
import { api, CourseSettings, errText, fmtDate, lateBy, Match, pct, Submission } from '../api'
import { CompareModal } from './Compare'
import { FilesModal } from './Files'
import { AICell, AIDrawer } from './AICheck'
import { attemptCount, AttemptsModal, firstOnTime, gradeInMoodle, lastEarlier, statusText } from './Attempts'

// The grade sheet of an assignment (a row per student) and of a student (a row per assignment) share their columns, the
// teacher's edits and the windows they open. A row carries its own assignment: maximum, deadline, link to Moodle.

interface Edit { points: number | null; comment: string }

const flagText: Record<string, { label: string; color: string; hint: string }> = {
  'no-code': { label: 'No code archive', color: 'red', hint: 'No source files were found: probably only a report was uploaded' },
  'no-report': { label: 'No report text', color: 'orange', hint: 'No report, or a PDF with images only (no text to check)' },
  'invisible-unicode': { label: 'Hidden characters', color: 'orange', hint: 'Several zero-width or tag characters in the text or code: text may be hidden from people but readable by a model. They are removed before the AI check; look at the work.' },
  'hidden-text': { label: 'Hidden text', color: 'orange', hint: 'The Word or PDF report has text a person does not see: hidden, white on white, tiny or under a shape. It was left out of the AI check; look at the report.' },
  executable: { label: 'Executable file', color: 'magenta', hint: 'An executable file was uploaded' },
  'type-mismatch': { label: 'Type mismatch', color: 'orange', hint: 'A file content does not match its extension' },
  'archive-limit': { label: 'Archive too big', color: 'orange', hint: 'The archive exceeded size or file count limits; only part was checked' },
  'unsafe-path': { label: 'Unsafe paths', color: 'orange', hint: 'The archive contains paths outside its folder; they were skipped' },
  'nested-archive': { label: 'Nested archive', color: 'default', hint: 'Archives inside archives' },
  unreadable: { label: 'Unreadable file', color: 'orange', hint: 'A file could not be opened' },
  'office-macros': { label: 'Macros', color: 'magenta', hint: 'An Office document with macros' },
}

const blocking = ['executable', 'invisible-unicode', 'hidden-text']
const informational = ['nested-archive']

export const submitted = (s: Submission) => s.moodle_status === 'submitted'
export const attention = (s: Submission) => submitted(s) && (s.integrity !== 'ok' || s.flags.length > 0)
export const ready = (s: Submission) => submitted(s) && s.points !== null && s.state !== 'published' && s.state !== 'approved'
export const clean = (s: Submission, requireCode = true) => submitted(s) && s.integrity === 'ok' && !s.flags.some(f => blocking.includes(f) || (requireCode && f === 'no-code')) && (!s.ai || s.ai.status === 'done')
// Nothing asks the teacher to look: no strong similarity, no flag, the AI check finished without remarks. A draft the
// teacher saved counts as looked at.
export const unremarkable = (s: Submission) => s.source === 'teacher' || (s.integrity !== 'high' && !s.flags.some(f => !informational.includes(f)) && (!s.ai || s.ai.status === 'done'))

interface SheetOptions {
  load: () => void // reads the rows again after a change
  settings: CourseSettings | null
  name: (s: Submission) => string // what a message calls a row: the student, or the assignment
  pointsTitle: string
  perRowMax?: boolean // show each row's maximum next to its points: the assignments of a student differ
}

// useSheet keeps the teacher's unsaved edits, the selection and the open windows of a sheet, and builds its columns.
export function useSheet({ load, settings, name, pointsTitle, perRowMax = false }: SheetOptions) {
  const { message } = App.useApp()
  const [edits, setEdits] = useState<Record<number, Edit>>({}), [selected, setSelected] = useState<number[]>([]), [busy, setBusy] = useState(false)
  const [compare, setCompare] = useState<{ s: Submission; m: Match } | null>(null), [viewing, setViewing] = useState<Submission | null>(null), [aiOpen, setAiOpen] = useState<Submission | null>(null)
  const [attemptsOf, setAttemptsOf] = useState<Submission | null>(null)

  const edit = (s: Submission) => edits[s.id] ?? { points: s.points, comment: s.comment }
  const dirty = (s: Submission) => !!edits[s.id] && (edits[s.id].points !== s.points || edits[s.id].comment !== s.comment)
  const change = (s: Submission, e: Partial<Edit>) => setEdits(prev => ({ ...prev, [s.id]: { ...edit(s), ...e } }))
  const save = async (s: Submission) => {
    const e = edit(s)
    try { await api.saveDraft(s.id, e.points, e.comment, s.version); setEdits(({ [s.id]: _, ...rest }) => rest); message.success(`Saved: ${name(s)}`) } catch (err) { message.error(errText(err)) }
    load()
  }

  const publish = async (ids: number[]) => {
    setBusy(true)
    try { await api.publish(ids); setSelected([]); message.success(`Publishing ${ids.length} grade(s) to Moodle…`) } catch (e) { message.error(errText(e)) } finally { setBusy(false) }
    load()
  }

  const deadlines = settings?.track_deadlines ?? true, extrasOn = settings?.extras_enabled ?? false
  const penalty = (s: Submission) => (deadlines && settings?.late_penalty_enabled && s.late ? settings.late_penalty : 0)
  const columns: TableColumnsType<Submission> = [
    {
      title: 'Submission', width: 240, render: (_, s) => !submitted(s) ? <Flex gap={4} wrap>
        <Tag style={{ marginInlineEnd: 0 }}>{statusText[s.moodle_status] ?? s.moodle_status}</Tag>
        {attemptCount(s) > 1 && <Tag color="geekblue" style={{ cursor: 'pointer', marginInlineEnd: 0 }} onClick={() => setAttemptsOf(s)}>Attempts ({attemptCount(s)})</Tag>}
      </Flex> : <Flex vertical gap={4}>
        <Flex gap={4} wrap align="center">
          <Typography.Text type="secondary">{fmtDate(s.submitted_at)}</Typography.Text>
          {deadlines && s.late && <Tooltip title={<>Deadline: {fmtDate(s.due_at)}<br />Uploaded: {fmtDate(s.submitted_at)}<br />{penalty(s) ? `Penalty: −${penalty(s)} point(s)` : 'No penalty (off in the course settings)'}</>}>
            <Tag color="red" style={{ marginInlineEnd: 0 }}>Late: {lateBy(s.submitted_at, s.due_at)}</Tag></Tooltip>}
          {deadlines && firstOnTime(s) && <Tooltip title={<>First uploaded {fmtDate(firstOnTime(s))}, before the deadline {fmtDate(s.due_at)}; the files were uploaded again later.{penalty(s) ? <><br />The draft still takes the late penalty off: −{penalty(s)} point(s).</> : null}</>}>
            <Tag color="green" style={{ marginInlineEnd: 0 }}>First upload on time</Tag></Tooltip>}
          {attemptCount(s) > 1 && <Tooltip title="Every attempt: what was uploaded, the grade and the comment"><Tag color="geekblue" style={{ cursor: 'pointer', marginInlineEnd: 0 }} onClick={() => setAttemptsOf(s)}>Attempt {s.attempt + 1} of {attemptCount(s)}</Tag></Tooltip>}
          {s.open_attempt !== null && <Tooltip title={`Attempt ${s.open_attempt + 1} is open in Moodle, usually opened by Moodle itself when a low grade is published. The student has not submitted it, so these are the files of attempt ${s.attempt + 1}.`}>
            <Tag color="gold" style={{ marginInlineEnd: 0 }}>Attempt {s.open_attempt + 1} {s.open_status === 'draft' ? 'in draft' : 'open, not submitted'}</Tag></Tooltip>}
          {s.files.length === 0 && <Tag color="red" style={{ marginInlineEnd: 0 }}>No files in this attempt</Tag>}
          {s.resubmitted && <Tooltip title="The student uploaded the work again after your comment was published"><Tag color="blue" style={{ marginInlineEnd: 0 }}>Re-uploaded after your comment</Tag></Tooltip>}
        </Flex>
        <Flex gap={4} wrap>{s.files.map(f => <Tag key={f} className="wrap-tag" style={{ cursor: 'pointer' }} title={f} onClick={() => setViewing(s)} icon={f.endsWith('.pdf') ? <FilePdfOutlined /> : /\.(zip|rar|7z|tar|gz)$/i.test(f) ? <FileZipOutlined /> : <FileOutlined />}>{f}</Tag>)}</Flex>
        {s.files.length > 0 && <Button size="small" icon={<FolderOpenOutlined />} onClick={() => setViewing(s)} style={{ width: 'fit-content' }}>View files</Button>}
        <Flex gap={4} wrap>{s.flags.filter(f => f !== 'no-code' || (settings?.require_code ?? true)).map(f => <Tooltip key={f} title={flagText[f]?.hint}><Tag color={flagText[f]?.color ?? 'orange'}>{flagText[f]?.label ?? f}</Tag></Tooltip>)}</Flex>
      </Flex>,
    },
    {
      title: 'Integrity', width: 280, render: (_, s) => !submitted(s) ? null : <Flex vertical gap={4} align="start">
        <Tag color={s.integrity === 'high' ? 'red' : s.integrity === 'medium' ? 'gold' : 'green'}>
          {s.integrity === 'high' ? (s.matches?.some(m => m.severity === 'high') ? 'Copy' : 'Check manually') : s.integrity === 'medium' ? 'Similar' : 'Original'}
        </Tag>
        {s.matches?.map((m, i) => (
          <Button key={i} type="link" size="small" style={{ padding: 0, height: 'auto', textAlign: 'left', whiteSpace: 'normal' }} onClick={() => setCompare({ s, m })}>
            {m.short_name || m.student}{m.groups.length ? ` (${m.groups.join(', ')})` : ''}: code {pct(m.code_similarity)}%, report {pct(m.report_similarity)}%{m.identical_files ? `, ${m.identical_files} identical files` : ''}
          </Button>
        ))}
      </Flex>,
    },
    {
      title: 'AI check', width: 170, render: (_, s) => !submitted(s) ? null : <AICell ai={s.ai} max={s.max_grade} extras={extrasOn} onOpen={() => setAiOpen(s)} />,
    },
    {
      title: 'In Moodle', width: 120, render: (_, s) => {
        const grade = gradeInMoodle(s), shown = s.attempts.find(a => a.attempt === s.attempt), before = lastEarlier(s, a => a.grade !== null), max = s.max_grade
        return <Flex vertical gap={4} align="start">
          {grade === null ? <Typography.Text type="secondary">—</Typography.Text>
          : <Tooltip title={<>Graded {fmtDate(shown?.graded_at ?? s.graded_at)}{shown?.comment && <div style={{ whiteSpace: 'pre-wrap', marginTop: 4 }}>{shown.comment}</div>}</>}>
            <Tag color="blue" icon={shown?.comment ? <CommentOutlined /> : undefined}>{grade}{max !== null ? ` / ${max}` : ''}</Tag></Tooltip>}
          {grade === null && before && <Tooltip title="The grade of an earlier attempt; this attempt has none">
            <Typography.Link style={{ fontSize: 12 }} onClick={() => setAttemptsOf(s)}>Attempt {before.attempt + 1}: {before.grade}{max !== null ? ` / ${max}` : ''}</Typography.Link></Tooltip>}
          {s.moodle_grader_url && <Tooltip title="Open the grading page of this student in Moodle"><Typography.Link href={s.moodle_grader_url} target="_blank" rel="noopener noreferrer" style={{ fontSize: 12 }}>
            <ExportOutlined /> Open</Typography.Link></Tooltip>}
        </Flex>
      },
    },
    {
      title: pointsTitle, width: perRowMax ? 140 : 110, render: (_, s) => !submitted(s) ? null
      : <Flex vertical gap={2}>
        <Flex gap={4} align="center">
          <InputNumber min={0} max={s.max_grade ?? undefined} step={settings?.points_step ?? 0.5} value={edit(s).points} onChange={v => change(s, { points: v ?? null })} style={{ width: 90 }} placeholder="—" />
          {perRowMax && s.max_grade !== null && <Typography.Text type="secondary">/ {s.max_grade}</Typography.Text>}
        </Flex>
        <Typography.Text type="secondary" style={{ fontSize: 11 }}>{pointsSource(s)}</Typography.Text>
      </Flex>,
    },
    {
      title: 'Comment for the student', width: 340, render: (_, s) => !submitted(s) ? null
      : <Flex vertical gap={4}>
        <Input.TextArea autoSize={{ minRows: 1, maxRows: 8 }} value={edit(s).comment} onChange={e => change(s, { comment: e.target.value })} placeholder="Empty: the work is fine" />
        <PreviousComment s={s} onOpen={() => setAttemptsOf(s)} />
      </Flex>,
    },
    {
      title: 'Status', width: 150, render: (_, s) => !submitted(s) ? null : <Flex vertical gap={6} align="start">
        {dirty(s) ? <Tag color="orange">Unsaved</Tag> : <StateTag s={s} />}
        {s.publish_error && <Typography.Text type="danger" style={{ fontSize: 12 }}>{s.publish_error}</Typography.Text>}
        {dirty(s) ? <Button size="small" type="primary" icon={<SaveOutlined />} onClick={() => save(s)}>Save</Button>
        : s.points !== null && s.state !== 'approved' && <Popconfirm title={`Publish ${s.points} points to Moodle?`} onConfirm={() => publish([s.id])}>
          <Button size="small" icon={<CloudUploadOutlined />}>{s.state === 'published' ? 'Republish' : 'Publish'}</Button>
        </Popconfirm>}
      </Flex>,
    },
  ]

  const modals = (courseId: number | null) => <>
    <AIDrawer id={aiOpen?.id ?? 0} student={aiOpen?.student ?? ''} open={!!aiOpen} onClose={() => setAiOpen(null)} onChanged={load} courseId={courseId} />
    <FilesModal id={viewing?.id ?? 0} student={viewing?.student ?? ''} open={!!viewing} onClose={() => setViewing(null)} />
    <AttemptsModal s={attemptsOf} max={attemptsOf?.max_grade ?? null} onClose={() => setAttemptsOf(null)} onViewFiles={id => { if (attemptsOf) setViewing({ ...attemptsOf, id }) }} />
    <CompareModal id={compare?.s.id ?? 0} student={compare?.s.student ?? ''} match={compare?.m ?? null} onClose={() => setCompare(null)} />
  </>

  return { dirty, selected, setSelected, busy, setBusy, publish, penalty, columns, modals }
}

// PreviousComment shows under the comment what the student was told about the attempt before this one.
function PreviousComment({ s, onOpen }: { s: Submission; onOpen: () => void }) {
  const before = lastEarlier(s, a => a.comment !== '')
  if (!before) return null
  return (
    <Tooltip title={<div style={{ whiteSpace: 'pre-wrap' }}>{before.comment}</div>}>
      <Typography.Paragraph type="secondary" ellipsis={{ rows: 2 }} style={{ fontSize: 12, margin: 0, cursor: 'pointer' }} onClick={onOpen}>
        <CommentOutlined /> Attempt {before.attempt + 1}: {before.comment}
      </Typography.Paragraph>
    </Tooltip>
  )
}

function StateTag({ s }: { s: Submission }) {
  switch (s.state) {
    case 'approved': return <Tag color="processing">Publishing…</Tag>
    case 'published': return <Tag color="success">Published</Tag>
    case 'publish_failed': return <Tag color="error">Publish failed</Tag>
    default: return s.points === null ? <Tag>No grade yet</Tag> : <Tag color="default">{s.source === 'teacher' ? 'Your draft' : s.source === 'llm' ? 'AI draft' : 'Auto draft'}</Tag>
  }
}

// pointsSource explains where the draft points come from: the AI check minus the late penalty, or you.
function pointsSource(s: Submission) {
  if (s.source === 'teacher') return 'your grade'
  if (s.source === 'auto' && s.state === 'published') return 'from Moodle'
  if (s.source === 'llm' && s.ai?.score != null) return s.ai.late ? `AI ${s.ai.score} − late ${s.ai.late}` : `AI ${s.ai.score}`
  return ''
}
