import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Badge, Button, Card, Drawer, Empty, Flex, Input, List, Progress, Steps, Table, Tag, Tooltip, Typography } from 'antd'
import { CheckCircleOutlined, CloudDownloadOutlined, PlusOutlined, SyncOutlined, WarningOutlined } from '@ant-design/icons'
import { api, Course, duration, errText, fmtDate, mb, Progress as P } from '../api'
import { useNow, usePoll } from '../App'

export function CoursesPage() {
  const { message } = App.useApp()
  const [mine, setMine] = useState<Course[] | null>(null), [drawer, setDrawer] = useState(false)
  const load = useCallback(() => { api.courses().then(setMine, e => message.error(errText(e))) }, [message])
  useEffect(load, [load])

  const running = (mine ?? []).some(c => c.sync_status === 'running')
  usePoll(load, running)

  const now = useNow(running)

  const sync = async (c: Course) => {
    try { await api.sync(c.id); message.info(`Sync of “${c.name}” started`) } catch (e) { message.error(errText(e)) }
    load()
  }

  const columns = [
    { title: 'Course', dataIndex: 'name', render: (_: string, c: Course) => <a href={`#/course/${c.id}`} style={{ fontWeight: 500 }}>{c.name}</a> },
    {
      title: 'Synchronization', width: '55%', render: (_: unknown, c: Course) => c.sync_status === 'running' ? <SyncPanel p={c.progress} now={now} />
      : c.sync_status === 'failed' ? <Alert type="error" showIcon title="Last sync failed" description={c.sync_error} />
      : c.last_synced_at ? <Flex gap={8} align="center"><Tag icon={<CheckCircleOutlined />} color="success">Up to date</Tag><Typography.Text type="secondary">synced {fmtDate(c.last_synced_at)} · {c.progress.works} works, {c.progress.files} files ({mb(c.progress.bytes)})</Typography.Text></Flex>
      : <Tag>Never synced</Tag>,
    },
    {
      title: '', width: 150, align: 'right' as const, render: (_: unknown, c: Course) =>
        <Button type="primary" icon={<SyncOutlined spin={c.sync_status === 'running'} />} disabled={c.sync_status === 'running'} onClick={() => sync(c)}>{c.sync_status === 'running' ? 'Syncing' : 'Sync now'}</Button>,
    },
  ]

  return (
    <Card title={<Typography.Title level={4} style={{ margin: 0 }}>My courses</Typography.Title>} extra={<Button icon={<PlusOutlined />} onClick={() => setDrawer(true)}>Add course</Button>}>
      <Table rowKey="id" loading={mine === null} dataSource={mine ?? []} columns={columns} pagination={false}
        locale={{ emptyText: <Empty description="No connected courses yet"><Button type="primary" icon={<PlusOutlined />} onClick={() => setDrawer(true)}>Add a course from Moodle</Button></Empty> }} />
      <AddCourses open={drawer} onClose={() => { setDrawer(false); load() }} />
    </Card>
  )
}

// Stages of SyncCourse in order; the server reports the activity in flight (Temporal), counters show how far it got.
const stages = [
  { key: 'FetchCourse', title: 'Read course' },
  { key: 'DownloadFiles', title: 'Download files' },
  { key: 'ProcessSubmission', title: 'Extract' },
  { key: 'CheckAssignment', title: 'Compare works' },
  { key: 'StartAssess', title: 'Start AI check' },
]

function currentStage(p: P) {
  const i = stages.findIndex(s => s.key === p.stage)
  if (i >= 0) return i
  return p.stage === 'FinishSync' ? stages.length : 0
}

const unit: Record<string, string> = { DownloadFiles: 'works downloaded', ProcessSubmission: 'works unpacked and fingerprinted', CheckAssignment: 'assignments compared', StartAssess: 'assignments queued for the AI check (it runs in the background)' }

// SyncPanel shows the stage and counts reported by the sync workflow itself, so the stage only moves forward.
// "No progress" means the stage counter has not moved for 2 minutes in stages where items are quick.
function SyncPanel({ p, now }: { p: P; now: number }) {
  const step = currentStage(p)
  const key = `${p.stage}/${p.done}`
  const [mark, setMark] = useState({ key, at: now })

  if (mark.key !== key) setMark({ key, at: now })
  const still = mark.key === key ? now - mark.at : 0
  const started = p.started_at ? now - new Date(p.started_at).getTime() : null
  const stalled = (p.stage === 'DownloadFiles' || p.stage === 'ProcessSubmission') && still > 120_000
  const percent = p.total ? Math.floor((100 * p.done) / p.total) : 0
  const detail = step === 0 ? 'Reading assignments, students and the list of files from Moodle…'
    : step >= stages.length ? 'Finishing…'
    : `${p.done} of ${p.total} ${unit[p.stage] ?? 'done'}${p.stage === 'DownloadFiles' && p.total === 0 ? ' (nothing new in Moodle)' : ''}`

  return (
    <Flex vertical gap={8}>
      <Steps size="small" current={step} items={stages.map(s => ({ title: s.title }))} />
      {step > 0 && step < stages.length && <Progress percent={percent} status={stalled ? 'exception' : 'active'} size="small" />}
      <Flex gap={12} wrap align="center">
        <Badge status={stalled ? 'warning' : 'processing'} text={detail} />
        <Typography.Text type="secondary">
          {started !== null && <>running {duration(started)}</>}{step > 0 && <> · this stage unchanged for {duration(still)}</>}
        </Typography.Text>
      </Flex>
      {stalled && <Alert type="warning" showIcon icon={<WarningOutlined />} title="No progress for over 2 minutes" description="Moodle may be slow or unreachable; the sync keeps retrying on its own." />}
      {p.retrying?.slice(0, 3).map((r, i) => (
        <Alert key={i} type="warning" showIcon title={`Retrying ${stages.find(s => s.key === r.activity)?.title ?? r.activity} (attempt ${r.attempt})`} description={r.error} />
      ))}
    </Flex>
  )
}

function AddCourses({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { message } = App.useApp()
  const [list, setList] = useState<Course[] | null>(null), [query, setQuery] = useState(''), [busy, setBusy] = useState<number | null>(null)
  const load = useCallback(() => { setList(null); api.courses(true).then(setList, e => { message.error(errText(e)); setList([]) }) }, [message])
  useEffect(() => { if (open) load() }, [open, load])

  const toggle = async (c: Course) => {
    setBusy(c.id)
    try { await api.connect(c.id, !c.connected); setList(await api.courses(true)) } catch (e) { message.error(errText(e)) } finally { setBusy(null) }
  }

  const shown = (list ?? []).filter(c => c.name.toLowerCase().includes(query.toLowerCase()))
  return (
    <Drawer title="Courses where you can grade" open={open} onClose={onClose} size="large"
      extra={<Tooltip title="Reload the list from Moodle"><Button icon={<CloudDownloadOutlined />} onClick={load} /></Tooltip>}>
      <Input.Search placeholder="Search courses" allowClear value={query} onChange={e => setQuery(e.target.value)} style={{ marginBottom: 16 }} />
      <List loading={list === null} dataSource={shown} renderItem={c => (
        <List.Item actions={[<Button key="t" type={c.connected ? 'default' : 'primary'} danger={c.connected} loading={busy === c.id} onClick={() => toggle(c)}>{c.connected ? 'Disconnect' : 'Connect'}</Button>]}>
          <List.Item.Meta title={c.name} description={c.connected ? <Tag color="blue">Connected</Tag> : null} />
        </List.Item>
      )} />
    </Drawer>
  )
}
