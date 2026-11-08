import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, App, Breadcrumb, Button, Card, Collapse, Empty, Flex, Input, InputNumber, Popconfirm, Segmented, Select, Space, Statistic, Table, Tag, Tooltip, Typography } from 'antd'
import type { TableColumnsType } from 'antd'
import { CheckOutlined, CloudUploadOutlined, CommentOutlined, ExportOutlined, FolderOpenOutlined, FileOutlined, FilePdfOutlined, FileZipOutlined, SaveOutlined, SyncOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { api, AssignmentHeader, CourseSettings, errText, fmtDate, lateBy, Match, pct, Submission } from '../api'
import { usePoll } from '../App'
import { CompareModal } from './Compare'
import { FilesModal } from './Files'
import { AICell, AIDrawer, AssignmentDescription, ChecklistCard } from './AICheck'
import { attemptCount, AttemptsModal, gradeInMoodle, lastEarlier, statusText } from './Attempts'

interface Edit { points: number | null; comment: string }
type Filter = 'all' | 'notgraded' | 'submitted' | 'missing' | 'attention'

const filters: { value: Filter; label: string }[] = [
  { value: 'all', label: 'All' }, { value: 'notgraded', label: 'Not graded' }, { value: 'submitted', label: 'Submitted' }, { value: 'missing', label: 'Not submitted' }, { value: 'attention', label: 'Need attention' },
]

// The chosen tab is remembered per course, in this browser.
const filterKey = (courseId: number) => `assignment-filter:${courseId}`
const savedFilter = (courseId: number) => filters.find(f => f.value === localStorage.getItem(filterKey(courseId)))?.value ?? 'all'

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

const submitted = (s: Submission) => s.moodle_status === 'submitted'
const attention = (s: Submission) => submitted(s) && (s.integrity !== 'ok' || s.flags.length > 0)
const ready = (s: Submission) => submitted(s) && s.points !== null && s.state !== 'published' && s.state !== 'approved'
const clean = (s: Submission, requireCode = true) => submitted(s) && s.integrity === 'ok' && !s.flags.some(f => blocking.includes(f) || (requireCode && f === 'no-code')) && (!s.ai || s.ai.status === 'done')
// Nothing asks the teacher to look: no strong similarity, no flag, the AI check finished without remarks. A draft the
// teacher saved counts as looked at.
const unremarkable = (s: Submission) => s.source === 'teacher' || (s.integrity !== 'high' && !s.flags.some(f => !informational.includes(f)) && (!s.ai || s.ai.status === 'done'))
const groupOf = (s: Submission) => s.groups.join(', ') || 'No group'

export function AssignmentPage({ id }: { id: number }) {
  const { message } = App.useApp()
  const [all, setAll] = useState<Submission[] | null>(null), [head, setHead] = useState<AssignmentHeader | null>(null), [myGroups, setMyGroups] = useState<string[]>([])
  const [group, setGroup] = useState('mine'), [filter, setFilter] = useState<Filter>('all'), [query, setQuery] = useState('')
  const [settings, setSettings] = useState<CourseSettings | null>(null), [grader, setGrader] = useState('')
  const [hasChecklist, setHasChecklist] = useState(false)
  useEffect(() => { api.checklist(id).then(c => { setSettings(c.settings); setHasChecklist(!!c.checklist) }, () => {}) }, [id])

  const [edits, setEdits] = useState<Record<number, Edit>>({}), [selected, setSelected] = useState<number[]>([]), [busy, setBusy] = useState(false)
  const [compare, setCompare] = useState<{ s: Submission; m: Match } | null>(null), [viewing, setViewing] = useState<Submission | null>(null), [aiOpen, setAiOpen] = useState<Submission | null>(null)
  const [attemptsOf, setAttemptsOf] = useState<Submission | null>(null)
  const load = useCallback(() => {
    api.submissions(id).then(r => { setAll(r.submissions); setHead(r.assignment); setMyGroups(r.my_groups); setGrader(r.moodle_grader_url) }, e => message.error(errText(e)))
  }, [id, message])

  useEffect(load, [load])
  usePoll(load, (all ?? []).some(s => s.state === 'approved' || s.ai?.status === 'running' || s.ai?.status === 'queued'), 4000)

  const courseId = head?.course_id ?? null
  useEffect(() => { if (courseId !== null) setFilter(savedFilter(courseId)) }, [courseId])

  const pickFilter = (f: Filter) => {
    setFilter(f)
    if (courseId !== null) localStorage.setItem(filterKey(courseId), f)
  }

  const max = head?.max_grade ?? null
  const inGroup = useCallback((s: Submission) => group === 'all' || (group === 'mine' ? myGroups.length === 0 || s.groups.some(g => myGroups.includes(g)) : groupOf(s) === group), [group, myGroups])
  const scope = useMemo(() => (all ?? []).filter(inGroup), [all, inGroup])

  const shown = scope.filter(s => (filter === 'all' || (filter === 'notgraded' ? submitted(s) && gradeInMoodle(s) === null && s.state !== 'published' : filter === 'submitted' ? submitted(s) : filter === 'missing' ? !submitted(s) : attention(s)))
    && (!query || s.student.toLowerCase().includes(query.toLowerCase())))

  const groups = [...new Set((all ?? []).map(groupOf))].sort()

  const edit = (s: Submission) => edits[s.id] ?? { points: s.points, comment: s.comment }
  const dirty = (s: Submission) => !!edits[s.id] && (edits[s.id].points !== s.points || edits[s.id].comment !== s.comment)
  const change = (s: Submission, e: Partial<Edit>) => setEdits(prev => ({ ...prev, [s.id]: { ...edit(s), ...e } }))
  const save = async (s: Submission) => {
    const e = edit(s)
    try { await api.saveDraft(s.id, e.points, e.comment, s.version); setEdits(({ [s.id]: _, ...rest }) => rest); message.success(`Saved: ${s.student}`) } catch (err) { message.error(errText(err)) }
    load()
  }

  const reanalyze = async (ids: number[]) => {
    setBusy(true)
    try {
      const r = await api.reassessSelected(id, ids)
      setSelected([]); message.info(r.queued ? `A check of this assignment is running; the ${ids.length} chosen work(s) are analyzed right after it` : `Analyzing ${ids.length} chosen work(s) again…`)
    } catch (e) { message.error(errText(e)) } finally { setBusy(false) }

    load()
  }

  const publish = async (ids: number[]) => {
    setBusy(true)
    try { await api.publish(ids); setSelected([]); message.success(`Publishing ${ids.length} grade(s) to Moodle…`) } catch (e) { message.error(errText(e)) } finally { setBusy(false) }
    load()
  }

  const publishable = selected.filter(sid => all?.some(s => s.id === sid && ready(s)))
  const toFill = scope.filter(s => clean(s, settings?.require_code ?? true) && s.points === null && s.state === 'draft' && !dirty(s))
  const deadlines = settings?.track_deadlines ?? true, extrasOn = settings?.extras_enabled ?? false
  const penalty = (s: Submission) => (deadlines && settings?.late_penalty_enabled && s.late ? settings.late_penalty : 0)
  const fillMax = async () => {
    if (max === null) return
    setBusy(true)
    let failed = 0
    for (const s of toFill) await api.saveDraft(s.id, Math.max(0, max - penalty(s)), s.comment, s.version).catch(() => failed++)
    setBusy(false)
    if (failed) message.warning(`${failed} draft(s) changed meanwhile and were skipped`)
    else message.success(`Set ${max} points for ${toFill.length} clean works. Review and publish them.`)

    load()
  }

  const columns: TableColumnsType<Submission> = [
    {
      title: 'Student', width: 200, render: (_, s) => <Flex vertical>
        <Tooltip title={s.student}><Typography.Text strong>{s.short_name || s.student}</Typography.Text></Tooltip>
        {!s.enrolled && <Tooltip title="No longer enrolled in the course (e.g. a graduated group); kept for plagiarism checks"><Tag style={{ width: 'fit-content' }}>Left the course</Tag></Tooltip>}
      </Flex>,
    },
    {
      title: 'Submission', width: 240, render: (_, s) => !submitted(s) ? <Flex gap={4} wrap>
        <Tag style={{ marginInlineEnd: 0 }}>{statusText[s.moodle_status] ?? s.moodle_status}</Tag>
        {attemptCount(s) > 1 && <Tag color="geekblue" style={{ cursor: 'pointer', marginInlineEnd: 0 }} onClick={() => setAttemptsOf(s)}>Attempts ({attemptCount(s)})</Tag>}
      </Flex> : <Flex vertical gap={4}>
        <Flex gap={4} wrap align="center">
          <Typography.Text type="secondary">{fmtDate(s.submitted_at)}</Typography.Text>
          {deadlines && s.late && <Tooltip title={<>Deadline: {fmtDate(head?.due_at ?? null)}<br />Uploaded: {fmtDate(s.submitted_at)}<br />{penalty(s) ? `Penalty: −${penalty(s)} point(s)` : 'No penalty (off in the course settings)'}</>}>
            <Tag color="red" style={{ marginInlineEnd: 0 }}>Late: {lateBy(s.submitted_at, head?.due_at ?? null)}</Tag></Tooltip>}
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
      title: 'AI check', width: 170, render: (_, s) => !submitted(s) ? null : <AICell ai={s.ai} max={max} extras={extrasOn} onOpen={() => setAiOpen(s)} />,
    },
    {
      title: 'In Moodle', width: 120, render: (_, s) => {
        const grade = gradeInMoodle(s), shown = s.attempts.find(a => a.attempt === s.attempt), before = lastEarlier(s, a => a.grade !== null)
        return <Flex vertical gap={4} align="start">
          {grade === null ? <Typography.Text type="secondary">—</Typography.Text>
          : <Tooltip title={<>Graded {fmtDate(shown?.graded_at ?? s.graded_at)}{shown?.comment && <div style={{ whiteSpace: 'pre-wrap', marginTop: 4 }}>{shown.comment}</div>}</>}>
            <Tag color="blue" icon={shown?.comment ? <CommentOutlined /> : undefined}>{grade}{max !== null ? ` / ${max}` : ''}</Tag></Tooltip>}
          {grade === null && before && <Tooltip title="The grade of an earlier attempt; this attempt has none">
            <Typography.Link style={{ fontSize: 12 }} onClick={() => setAttemptsOf(s)}>Attempt {before.attempt + 1}: {before.grade}{max !== null ? ` / ${max}` : ''}</Typography.Link></Tooltip>}
          {grader && <Tooltip title="Open the grading page of this student in Moodle"><Typography.Link href={`${grader}&userid=${s.moodle_user_id}`} target="_blank" rel="noopener noreferrer" style={{ fontSize: 12 }}>
            <ExportOutlined /> Open</Typography.Link></Tooltip>}
        </Flex>
      },
    },
    {
      title: `Points${max !== null ? ` (max ${max})` : ''}`, width: 110, render: (_, s) => !submitted(s) ? null
      : <Flex vertical gap={2}>
        <InputNumber min={0} max={max ?? undefined} step={settings?.points_step ?? 0.5} value={edit(s).points} onChange={v => change(s, { points: v ?? null })} style={{ width: 90 }} placeholder="—" />
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

  const count = (f: (s: Submission) => boolean) => scope.filter(f).length
  const sections = groups.filter(g => shown.some(s => groupOf(s) === g))
  return (
    <>
      <Breadcrumb style={{ marginBottom: 16 }} items={[{ title: <a href="#/courses">Courses</a> }, { title: head ? <a href={`#/course/${head.course_id}`}>{head.course_name}</a> : '…' }, { title: head?.name ?? '…' }]} />
      <AssignmentDescription assignmentId={id} />
      <Card style={{ marginBottom: 16 }}>
        <Typography.Title level={4} style={{ marginTop: 0 }}>{head?.name}</Typography.Title>
        <Flex gap={40} wrap>
          <Statistic title="Students" value={scope.length} />
          <Statistic title="Submitted" value={count(submitted)} />
          <Statistic title="Not submitted" value={count(s => !submitted(s))} />
          <Statistic title="Need attention" value={count(attention)} styles={{ content: { color: count(attention) ? '#cf1322' : undefined } }} />
          <Statistic title="Graded in Moodle" value={count(s => gradeInMoodle(s) !== null)} />
          <Statistic title="Drafts ready to publish" value={count(ready)} />
          {extrasOn && <Statistic title="★ Extra task done" value={count(s => !!s.ai?.extra)} styles={{ content: { color: count(s => !!s.ai?.extra) ? '#d48806' : undefined } }} />}
        </Flex>
      </Card>
      <ChecklistCard assignmentId={id} onChanged={load} queued={(all ?? []).filter(s => s.ai?.status === 'queued' || s.ai?.status === 'running').length}
        failed={(all ?? []).filter(s => s.ai?.status === 'failed').length} courseId={head?.course_id ?? null} />
      <Card>
        <Flex gap={12} wrap align="center" style={{ marginBottom: 16 }}>
          <Select value={group} onChange={setGroup} style={{ minWidth: 240 }} options={[
            { value: 'mine', label: `My groups${myGroups.length ? ` (${myGroups.join(', ')})` : ''}` }, { value: 'all', label: 'All groups' }, ...groups.map(g => ({ value: g, label: g })),
          ]} />
          <Segmented<Filter> value={filter} onChange={pickFilter} options={filters} />
          <Input.Search placeholder="Find a student" allowClear value={query} onChange={e => setQuery(e.target.value)} style={{ width: 220 }} />
          <span style={{ flex: 1 }} />
          <Space>
            {max !== null && <Popconfirm title={`Set ${max} points for ${toFill.length} works?`} description="Only works that are original, have code and no flags, and have no points yet (no AI check result)." onConfirm={fillMax} disabled={!toFill.length}>
              <Tooltip title="Without the AI review nobody checked these works against the assignment: this gives them full points as drafts for you to review">
                <Button icon={<ThunderboltOutlined />} disabled={!toFill.length} loading={busy}>Full points for clean works ({toFill.length})</Button>
              </Tooltip>
            </Popconfirm>}
            <Tooltip title="Drafts nothing asks you to look at: no strong similarity, no flags, an AI check without remarks; and drafts you saved yourself. Others can be ticked one by one."><Button icon={<CheckOutlined />} onClick={() => setSelected(scope.filter(s => ready(s) && unremarkable(s) && !dirty(s)).map(s => s.id))}>Select ready</Button></Tooltip>
            {selected.length > 0 && hasChecklist && <Popconfirm title={`Analyze ${selected.length} chosen work(s) again with the AI check?`} onConfirm={() => reanalyze(selected)}>
              <Button icon={<SyncOutlined />} loading={busy}>Re-analyze selected ({selected.length})</Button>
            </Popconfirm>}
            <Popconfirm title={`Publish ${publishable.length} grade(s) to Moodle?`} onConfirm={() => publish(publishable)} disabled={!publishable.length}>
              <Button type="primary" icon={<CloudUploadOutlined />} disabled={!publishable.length} loading={busy}>Publish selected ({publishable.length})</Button>
            </Popconfirm>
          </Space>
        </Flex>
        {all !== null && !all.some(submitted) && <Alert type="info" showIcon title="No submitted works yet" description="Run a sync on the Courses page to load submissions from Moodle." style={{ marginBottom: 16 }} />}
        {all === null ? <Table loading columns={columns} dataSource={[]} /> : sections.length === 0 ? <Empty description="No students match the filter" /> : (
          <Collapse defaultActiveKey={groups} items={sections.map(g => {
            const rows = shown.filter(s => groupOf(s) === g)
            return {
              key: g,
              label: <Flex gap={12} align="center"><Typography.Text strong>{g}</Typography.Text>
                <Typography.Text type="secondary">{rows.length} students · {rows.filter(submitted).length} submitted · {rows.filter(s => gradeInMoodle(s) !== null).length} graded in Moodle</Typography.Text>
                {rows.some(attention) && <Tag color="red">{rows.filter(attention).length} need attention</Tag>}</Flex>,
              children: <Table<Submission> className="grades" rowKey="student_id" size="middle" dataSource={rows} columns={columns} pagination={false} tableLayout="fixed" scroll={{ x: 1650 }}
                rowSelection={{ selectedRowKeys: rows.filter(s => selected.includes(s.id)).map(s => s.student_id), getCheckboxProps: s => ({ disabled: !submitted(s) || dirty(s) }),
                  onChange: (_, picked) => setSelected(prev => [...prev.filter(x => !rows.some(r => r.id === x)), ...picked.map(s => s.id)]) }} />,
            }
          })} />
        )}
      </Card>
      <AIDrawer id={aiOpen?.id ?? 0} student={aiOpen?.student ?? ''} open={!!aiOpen} onClose={() => setAiOpen(null)} onChanged={load} courseId={head?.course_id ?? null} />
      <FilesModal id={viewing?.id ?? 0} student={viewing?.student ?? ''} open={!!viewing} onClose={() => setViewing(null)} />
      <AttemptsModal s={attemptsOf} max={max} onClose={() => setAttemptsOf(null)} onViewFiles={id => { if (attemptsOf) setViewing({ ...attemptsOf, id }) }} />
      <CompareModal id={compare?.s.id ?? 0} student={compare?.s.student ?? ''} match={compare?.m ?? null} onClose={() => setCompare(null)} />
    </>
  )
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
