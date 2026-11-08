import { useCallback, useEffect, useState } from 'react'
import { App, Breadcrumb, Button, Card, Flex, Form, Input, InputNumber, Progress, Select, Switch, Table, Tabs, Tag, Tooltip, Typography } from 'antd'
import { EyeOutlined, SyncOutlined } from '@ant-design/icons'
import { AIProgress, api, Assignment, Course, CourseSettings, errText, fmtDate } from '../api'
import { PromptsPage } from './Prompts'
import { usePoll } from '../App'
import { useAIStatus } from './AIStatus'

export function CoursePage({ id }: { id: number }) {
  const { message } = App.useApp()
  const [list, setList] = useState<Assignment[] | null>(null), [course, setCourse] = useState<Course | null>(null)
  const load = useCallback(() => { api.assignments(id).then(setList, e => message.error(errText(e))) }, [id, message])
  useEffect(() => {
    load()
    api.courses().then(cs => setCourse(cs.find(c => c.id === id) ?? null), () => {})
  }, [id, load])

  const ai = useAIStatus(5000)
  usePoll(load, (list ?? []).some(a => a.ai.queued + a.ai.running > 0) || !!ai?.activity, 5000)

  const columns = [
    {
      title: 'Assignment', dataIndex: 'name', render: (_: string, a: Assignment) => <>
        <a href={`#/assignment/${a.id}`} style={{ fontWeight: 500 }}>{a.name}</a>
        {!a.marking_workflow && <Tooltip title="Marking workflow is off in Moodle: students see a grade as soon as it is published"><Tag icon={<EyeOutlined />} style={{ marginLeft: 8 }}>Instant</Tag></Tooltip>}
      </>,
    },
    { title: 'Due', dataIndex: 'due_at', width: 170, render: (d: string | null) => fmtDate(d) },
    { title: 'Max', dataIndex: 'max_grade', width: 80, render: (m: number | null) => m ?? 'scale' },
    { title: 'Submitted', dataIndex: 'submitted', width: 110 },
    { title: 'Need attention', dataIndex: 'flagged', width: 140, render: (n: number) => n ? <Tag color="red">{n}</Tag> : <Tag color="green">0</Tag> },
    { title: 'AI check', width: 260, render: (_: unknown, a: Assignment) => <AIColumn ai={a.ai} building={ai?.activity?.kind === 'checklist' && ai.activity.assignment_id === a.id} /> },
    { title: 'Published', width: 200, render: (_: unknown, a: Assignment) => <Progress size="small" percent={a.submitted ? Math.round((100 * a.published) / a.submitted) : 0} format={() => `${a.published}/${a.submitted}`} /> },
  ]

  return (
    <>
      <Breadcrumb style={{ marginBottom: 16 }} items={[{ title: <a href="#/courses">Courses</a> }, { title: course?.name ?? '…' }]} />
      <Card title={<Typography.Title level={4} style={{ margin: 0 }}>{course?.name ?? 'Assignments'}</Typography.Title>}>
        <Tabs items={[
          { key: 'assignments', label: 'Assignments', children: <Table rowKey="id" loading={list === null} dataSource={list ?? []} columns={columns} pagination={false} /> },
          { key: 'settings', label: 'Settings', children: <SettingsTab courseId={id} /> },
          { key: 'prompts', label: 'AI prompts', children: <PromptsPage courseId={id} /> },
        ]} />
      </Card>
    </>
  )
}

// AIColumn shows where the background AI check of an assignment is: building the checklist, checking N of M, results.
function AIColumn({ ai, building }: { ai: AIProgress; building: boolean }) {
  const total = ai.queued + ai.running + ai.done + ai.needs_attention + ai.failed
  if (building) return <Typography.Text><SyncOutlined spin /> Building the checklist…</Typography.Text>
  if (!total) return <Typography.Text type="secondary">{ai.has_checklist ? 'not checked' : '—'}</Typography.Text>
  const finished = ai.done + ai.needs_attention + ai.failed
  const busy = ai.queued + ai.running > 0
  return (
    <Flex vertical gap={2}>
      {busy && (!ai.has_checklist && !ai.running ? <Typography.Text><SyncOutlined spin /> Building the checklist…</Typography.Text>
      : <Typography.Text><SyncOutlined spin /> Checking: {finished} of {total}</Typography.Text>)}
      {busy && <Progress size="small" percent={Math.floor((100 * finished) / total)} showInfo={false} status="active" />}
      <Flex gap={4} wrap>
        {ai.done > 0 && <Tag color="green">{ai.done} checked</Tag>}
        {ai.needs_attention > 0 && <Tag color="orange">{ai.needs_attention} check manually</Tag>}
        {ai.failed > 0 && <Tag color="red">{ai.failed} failed</Tag>}
        {!busy && ai.done + ai.needs_attention === 0 && ai.failed === 0 && <Typography.Text type="secondary">not checked</Typography.Text>}
      </Flex>
    </Flex>
  )
}

// SettingsTab holds the rules of the course: everything that may differ between courses lives here, not in code.
function SettingsTab({ courseId }: { courseId: number }) {
  const { message } = App.useApp()
  const [s, setS] = useState<CourseSettings | null>(null), [busy, setBusy] = useState(false)
  useEffect(() => { api.courseSettings(courseId).then(setS, e => message.error(errText(e))) }, [courseId, message])

  if (!s) return null
  const set = (patch: Partial<CourseSettings>) => setS({ ...s, ...patch })
  const num = (v: number | string | null, def: number) => (v === null || v === '' ? def : Number(v))
  const save = async () => {
    setBusy(true)
    try {
      await api.saveCourseSettings(courseId, s)
      message.success('Settings saved. AI check rules apply to works analyzed from now on (“Re-analyze all” applies them to the rest); file and similarity rules apply from the next sync.')
    } catch (e) { message.error(errText(e)) } finally { setBusy(false) }
  }

  const late = s.track_deadlines && s.late_penalty_enabled
  return (
    <Form layout="vertical" style={{ maxWidth: 720 }}>
      <Typography.Title level={5}>Deadlines</Typography.Title>
      <Form.Item label="Track deadlines" extra="Show how late each work was uploaded after the deadline set in Moodle. Moodle extensions for single students are not taken into account.">
        <Switch id="track-deadlines" checked={s.track_deadlines} onChange={v => set({ track_deadlines: v })} />
      </Form.Item>
      <Form.Item label="Late penalty" extra="Points taken off a late work, whatever the AI check decided.">
        <Flex gap={12} align="center">
          <Switch id="late-penalty" checked={late} disabled={!s.track_deadlines} onChange={v => set({ late_penalty_enabled: v })} />
          <InputNumber id="late-penalty-points" min={0} max={100} step={0.5} value={s.late_penalty} disabled={!late} onChange={v => set({ late_penalty: num(v, 0) })} addonAfter="points off" />
        </Flex>
      </Form.Item>
      <Form.Item label="Late notice for the student" extra={<>Added to the comment of a late work. <Typography.Text code>{'{points}'}</Typography.Text> becomes the points in Russian, e.g. “1 балл”, “0,5 балла”.</>}>
        <Input id="late-notice" value={s.late_notice} disabled={!late} onChange={e => set({ late_notice: e.target.value })} />
      </Form.Item>

      <Typography.Title level={5}>What a work must contain</Typography.Title>
      <Form.Item label="A code archive is required" extra="When on, a work without source code is marked “No code archive” and needs your attention. Turn it off for courses without code.">
        <Switch id="require-code" checked={s.require_code} onChange={v => set({ require_code: v })} />
      </Form.Item>
      <Form.Item label="Extra tasks" extra="Assignments may have extra tasks beyond the grade, for example to raise the grade of an earlier term. The AI check marks whether they are done; they give no points. When off, checklists, filters and tags show no extra tasks.">
        <Switch id="extras-enabled" checked={s.extras_enabled} onChange={v => set({ extras_enabled: v })} />
      </Form.Item>

      <Typography.Title level={5}>AI check</Typography.Title>
      <Flex gap={24} wrap>
        <Form.Item label="Pictures per work" extra="PDF pages with images and screenshots the model reads; the slowest step of the check.">
          <InputNumber id="max-images" min={0} max={100} value={s.max_images} onChange={v => set({ max_images: num(v, 16) })} />
        </Form.Item>
        <Form.Item label="“Partial” gives" extra="Share of a criterion’s points for a partly done criterion.">
          <InputNumber id="partial-credit" min={0} max={100} step={5} value={Math.round(s.partial_credit * 100)} onChange={v => set({ partial_credit: num(v, 50) / 100 })} addonAfter="%" />
        </Form.Item>
        <Form.Item label="Points step" extra="Generated checklists round points to this step; also the step of the grade field.">
          <InputNumber id="points-step" min={0.01} max={10} step={0.25} value={s.points_step} onChange={v => set({ points_step: num(v, 0.5) })} />
        </Form.Item>
      </Flex>

      <Typography.Title level={5}>Similarity check</Typography.Title>
      <Flex gap={24} wrap>
        <Form.Item label="“Similar” from" extra="Code or report similarity.">
          <InputNumber id="similar-percent" min={1} max={100} value={s.similar_percent} onChange={v => set({ similar_percent: num(v, 50) })} addonAfter="%" />
        </Form.Item>
        <Form.Item label="“Copy” from" extra="Code or report similarity.">
          <InputNumber id="copy-percent" min={1} max={100} value={s.copy_percent} onChange={v => set({ copy_percent: num(v, 75) })} addonAfter="%" />
        </Form.Item>
        <Form.Item label="Identical files for “Copy”" extra="…when they are at least half of the smaller work.">
          <InputNumber id="copy-files" min={1} max={100} value={s.copy_identical_files} onChange={v => set({ copy_identical_files: num(v, 2) })} />
        </Form.Item>
      </Flex>
      <Flex gap={24} wrap>
        <Form.Item label="Template: from works" extra="With fewer works no code counts as a template.">
          <InputNumber id="template-works" min={2} max={1000} value={s.template_min_works} onChange={v => set({ template_min_works: num(v, 10) })} />
        </Form.Item>
        <Form.Item label="Template: code in more than" extra="Code shared by more works than this is a template, not evidence; it is shown grey.">
          <InputNumber id="template-share" min={1} max={99} step={5} value={Math.round(s.template_share * 100)} onChange={v => set({ template_share: num(v, 50) / 100 })} addonAfter="% of works" />
        </Form.Item>
      </Flex>

      <Typography.Title level={5}>Files</Typography.Title>
      <Form.Item label="Source code files" extra="Extensions read as code: compared by the similarity check and shown to the model. Other text files are compared as plain text.">
        <Select id="code-extensions" mode="tags" value={s.code_extensions} onChange={v => set({ code_extensions: v })} tokenSeparators={[',', ' ']} />
      </Form.Item>
      <Form.Item label="Ignored folders" extra="Build output and dependencies (bin, obj, node_modules…): left out of the checks and marked in the file viewer.">
        <Select id="ignored-dirs" mode="tags" value={s.ignored_dirs} onChange={v => set({ ignored_dirs: v })} tokenSeparators={[',', ' ']} />
      </Form.Item>
      <Button type="primary" loading={busy} onClick={save}>Save settings</Button>
    </Form>
  )
}
