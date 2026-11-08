import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, App, Button, Card, Checkbox, Empty, Flex, Modal, Popconfirm, Select, Spin, Table, Tag, Tooltip, Typography } from 'antd'
import { CheckCircleOutlined, CloseCircleOutlined, ExperimentOutlined, MinusCircleOutlined, SwapOutlined } from '@ant-design/icons'
import { api, CriterionResult, duration, errText, fmtDate, Run } from '../api'
import { usePoll } from '../App'

// The steps of a work's check that can run on another model; the checklist itself is not rebuilt for an experiment.
const steps: { key: string; label: string; hint: string }[] = [
  { key: 'page', label: 'Screenshots', hint: 'reads each screenshot and PDF page with pictures; needs a vision model' },
  { key: 'injection', label: 'Injection check', hint: 'looks for text addressed to the grader' },
  { key: 'check', label: 'Checklist', hint: 'decides every criterion with quotes: the step that sets the points' },
  { key: 'comment', label: 'Comment', hint: 'writes the comment for the student from the results' },
]

const verdict: Record<string, React.ReactNode> = {
  met: <Tag icon={<CheckCircleOutlined />} color="green">Met</Tag>, partial: <Tag icon={<MinusCircleOutlined />} color="gold">Partial</Tag>,
  not_met: <Tag icon={<CloseCircleOutlined />} color="red">Not met</Tag>,
}

// loadModels returns the installed models and the course's model for each step.
export async function loadModels(courseId: number | null): Promise<[string[], Record<string, string>]> {
  const installed = await api.models().catch(() => [] as string[])
  const ps = courseId ? await api.prompts(courseId).catch(() => []) : []
  return [installed, Object.fromEntries(ps.filter(p => steps.some(s => s.key === p.key)).map(p => [p.key, p.model.body]))]
}

// ModelPicker chooses a model for each step of a work's check.
export function ModelPicker({ models, installed, onChange }: { models: Record<string, string>; installed: string[]; onChange: (m: Record<string, string>) => void }) {
  return (
    <Flex gap={12} wrap>
      {steps.map(s => (
        <Flex key={s.key} vertical gap={4} style={{ minWidth: 200, flex: 1 }}>
          <Tooltip title={s.hint}><Typography.Text strong>{s.label}</Typography.Text></Tooltip>
          <Select id={`model-${s.key}`} value={models[s.key] || undefined} placeholder="Choose a model" onChange={v => onChange({ ...models, [s.key]: v })} options={installed.map(m => ({ value: m, label: m }))} />
        </Flex>
      ))}
    </Flex>
  )
}

// ModelsModal asks for the models of each step before a re-analysis; the course's models are preselected.
export function ModelsModal({ open, courseId, title, text, onCancel, onOk }: { open: boolean; courseId: number | null; title: string; text: React.ReactNode; onCancel: () => void; onOk: (models: Record<string, string>) => Promise<void> }) {
  const [installed, setInstalled] = useState<string[]>([]), [models, setModels] = useState<Record<string, string>>({}), [busy, setBusy] = useState(false)
  useEffect(() => { if (open) void loadModels(courseId).then(([i, m]) => { setInstalled(i); setModels(m) }) }, [open, courseId])

  const ok = async () => { setBusy(true); try { await onOk(models) } finally { setBusy(false) } }
  return (
    <Modal title={title} open={open} onCancel={onCancel} onOk={ok} okText="Re-analyze" okButtonProps={{ disabled: !installed.length, loading: busy }} width={720} destroyOnHidden>
      <Typography.Paragraph type="secondary">{text}</Typography.Paragraph>
      <ModelPicker models={models} installed={installed} onChange={setModels} />
    </Modal>
  )
}

const runLabel = (r: Run) => `${fmtDate(r.finished_at)} · ${r.models || 'model unknown'}`

// RunsPanel lists every AI check of a work, compares two of them criterion by criterion, and tries other models
// without touching the current result.
export function RunsPanel({ id, courseId, open, onChanged }: { id: number; courseId: number | null; open: boolean; onChanged: () => void }) {
  const { message } = App.useApp()
  const [runs, setRuns] = useState<Run[] | null>(null), [installed, setInstalled] = useState<string[]>([])
  const [models, setModels] = useState<Record<string, string>>({}), [waiting, setWaiting] = useState<number | null>(null)
  const [pick, setPick] = useState<number[]>([]), [busy, setBusy] = useState(false)
  const load = useCallback(() => {
    api.runs(id).then(rs => {
      setRuns(rs)
      setWaiting(w => (w !== null && rs.length > w ? null : w))
    }, e => message.error(errText(e)))
  }, [id, message])

  useEffect(() => {
    if (!open) return
    setRuns(null); setPick([]); setWaiting(null); load()
    void loadModels(courseId).then(([i, m]) => { setInstalled(i); setModels(m) })
  }, [open, id, courseId, load])

  usePoll(load, open && waiting !== null, 5000)

  const start = async () => {
    setBusy(true)
    try {
      await api.experiment(id, models)
      setWaiting(runs?.length ?? 0)
      message.info('Queued: this work is checked with the chosen models after the work the model server is busy with')
    } catch (e) { message.error(errText(e)) } finally { setBusy(false) }
  }

  const promote = async (r: Run) => {
    try { await api.promoteRun(r.id); message.success('This run is now the current result; its points and comment went into the draft'); load(); onChanged() } catch (e) { message.error(errText(e)) }
  }

  const toggle = (rid: number, on: boolean) => setPick(p => (on ? [...p.filter(x => x !== rid), rid].slice(-2) : p.filter(x => x !== rid)))
  const [a, b] = useMemo(() => pick.map(p => runs?.find(r => r.id === p)).filter(Boolean) as Run[], [pick, runs])

  if (!runs) return <Spin />
  return (
    <Flex vertical gap={16}>
      <Card size="small" title={<Flex gap={8} align="center"><ExperimentOutlined />Try other models</Flex>}>
        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          The work is checked again with the same checklist on the models below. The result is kept as a separate run for comparison:
          the current result, the draft and the course models do not change until you choose “Use as current”.
        </Typography.Paragraph>
        <ModelPicker models={models} installed={installed} onChange={setModels} />
        <Flex gap={12} align="center" style={{ marginTop: 12 }}>
          <Button type="primary" icon={<ExperimentOutlined />} loading={busy || waiting !== null} onClick={start} disabled={!installed.length}>
            {waiting !== null ? 'Waiting for the result…' : 'Check with these models'}
          </Button>
          {waiting !== null && <Typography.Text type="secondary">A work takes a few minutes; a model that is not loaded yet adds about half a minute.</Typography.Text>}
        </Flex>
      </Card>

      <Card size="small" title="Runs" extra={<Typography.Text type="secondary">Tick two runs to compare them</Typography.Text>}>
        {!runs.length ? <Empty description="No AI checks yet" /> : (
          <Table<Run> rowKey="id" size="small" pagination={false} dataSource={runs} columns={[
            { title: '', width: 36, render: (_, r) => <Checkbox id={`run-${r.id}`} checked={pick.includes(r.id)} onChange={e => toggle(r.id, e.target.checked)} /> },
            { title: 'Finished', width: 170, render: (_, r) => <Flex vertical>{fmtDate(r.finished_at)}{r.started_at && <Typography.Text type="secondary" style={{ fontSize: 12 }}>took {duration(new Date(r.finished_at).getTime() - new Date(r.started_at).getTime())}</Typography.Text>}</Flex> },
            { title: 'Models', render: (_, r) => <Flex gap={4} wrap>{r.current && <Tag color="blue">current</Tag>}{r.experiment && <Tag color="purple">experiment</Tag>}<span>{r.models || '—'}</span></Flex> },
            { title: 'Points', width: 90, render: (_, r) => r.status === 'failed' ? <Tooltip title={r.error}><Tag color="red">failed</Tag></Tooltip> : r.result?.injection?.suspected ? 'not graded' : `${r.points ?? '—'}${r.result ? ` / ${r.result.max}` : ''}` },
            { title: 'Criteria met', width: 110, render: (_, r) => r.result?.criteria?.length ? `${r.result.criteria.filter(c => c.status === 'met').length} / ${r.result.criteria.length}` : '—' },
            { title: '', width: 140, render: (_, r) => !r.current && r.status !== 'failed' && r.result && (
              <Popconfirm title="Use this run as the current result?" description="Its points and comment go into the draft, unless you edited the draft yourself." onConfirm={() => promote(r)}>
                <Button size="small">Use as current</Button>
              </Popconfirm>) },
          ]} />
        )}
      </Card>

      {a && b ? <Compare a={a} b={b} /> : pick.length === 1 && <Alert type="info" showIcon title="Tick one more run to compare" />}
    </Flex>
  )
}

// Compare puts two runs side by side: every criterion with both verdicts, then points and comments.
function Compare({ a, b }: { a: Run; b: Run }) {
  const crit = (r: Run) => new Map((r.result?.criteria ?? []).map(c => [c.id, c]))
  const ca = crit(a), cb = crit(b)
  const ids = [...new Set([...ca.keys(), ...cb.keys()])]
  const rows = ids.map(id => ({ id, a: ca.get(id), b: cb.get(id) }))
  const differ = rows.filter(r => r.a?.status !== r.b?.status).length
  const cell = (c: CriterionResult | undefined) => !c ? <Typography.Text type="secondary">—</Typography.Text> : (
    <Flex vertical gap={4}>
      <Flex gap={6} align="center">{verdict[c.status] ?? <Tag>{c.status}</Tag>}<Typography.Text>{c.awarded} / {c.points}</Typography.Text>{c.no_quote && <Tag color="orange">no quote</Tag>}</Flex>
      {c.note && <Typography.Text type="secondary" style={{ fontSize: 12 }}>{c.note}</Typography.Text>}
    </Flex>)

  return (
    <Card size="small" title={<Flex gap={8} align="center"><SwapOutlined />Comparison: {differ ? `${differ} of ${rows.length} criteria differ` : 'same verdicts on every criterion'}</Flex>}>
      <Table rowKey="id" size="small" pagination={false} dataSource={rows}
        rowClassName={r => (r.a?.status !== r.b?.status ? 'run-diff' : '')}
        onRow={r => ({ style: r.a?.status !== r.b?.status ? { background: 'rgba(250, 173, 20, 0.12)' } : undefined })}
        columns={[
          { title: 'Criterion', width: '34%', render: (_, r) => (r.a ?? r.b)?.text },
          { title: <RunTitle r={a} />, render: (_, r) => cell(r.a) },
          { title: <RunTitle r={b} />, render: (_, r) => cell(r.b) },
        ]}
        summary={() => (
          <Table.Summary.Row>
            <Table.Summary.Cell index={0}><Typography.Text strong>Points</Typography.Text></Table.Summary.Cell>
            <Table.Summary.Cell index={1}><Typography.Text strong>{a.points ?? '—'}</Typography.Text>{a.result?.late_penalty ? ` (late −${a.result.late_penalty})` : ''}</Table.Summary.Cell>
            <Table.Summary.Cell index={2}><Typography.Text strong>{b.points ?? '—'}</Typography.Text>{b.result?.late_penalty ? ` (late −${b.result.late_penalty})` : ''}</Table.Summary.Cell>
          </Table.Summary.Row>
        )} />
      <Flex gap={12} style={{ marginTop: 12 }} wrap>
        {[a, b].map(r => (
          <Card key={r.id} size="small" title={<RunTitle r={r} />} style={{ flex: 1, minWidth: 280 }}>
            <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{r.comment || '—'}</Typography.Paragraph>
          </Card>
        ))}
      </Flex>
    </Card>
  )
}

const RunTitle = ({ r }: { r: Run }) => (
  <Tooltip title={runLabel(r)}><span>{r.current ? 'Current' : r.experiment ? 'Experiment' : 'Earlier'}: {r.step_models?.check ?? r.models.split(',')[0] ?? '—'}</span></Tooltip>
)
