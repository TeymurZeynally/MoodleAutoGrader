import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Card, Checkbox, Col, Collapse, Descriptions, Drawer, Dropdown, Empty, Flex, Input, InputNumber, Modal, Popconfirm, Progress, Row, Select, Space, Spin, Table, Tabs, Tag, Timeline, Tooltip, Typography } from 'antd'
import { StarFilled, StopOutlined, CheckCircleOutlined, CloseCircleOutlined, DeleteOutlined, DownOutlined, EditOutlined, ExclamationCircleOutlined, EyeOutlined, MinusCircleOutlined, PlusOutlined, RedoOutlined, RobotOutlined, SaveOutlined, SyncOutlined } from '@ant-design/icons'
import { AISummary, api, Assessment, Checklist, CriterionResult, errText, fmtDate, HistoryEvent, Place, PreviewStep } from '../api'
import { usePoll } from '../App'
import { ModelsModal, RunsPanel } from './Runs'

const statusTag: Record<string, { color: string; text: string }> = {
  queued: { color: 'default', text: 'Queued' }, running: { color: 'processing', text: 'Analyzing…' }, done: { color: 'green', text: 'Checked' },
  needs_attention: { color: 'orange', text: 'Check manually' }, failed: { color: 'red', text: 'Failed' },
}

// AICell is the grades table cell: met criteria and points, or the analysis state.
export function AICell({ ai, max, onOpen, extras = true }: { ai: AISummary | null; max: number | null; onOpen: () => void; extras?: boolean }) {
  if (!ai) return <Typography.Text type="secondary">Not analyzed</Typography.Text>
  const st = statusTag[ai.status] ?? { color: 'default', text: ai.status }
  const busy = ai.status === 'queued' || ai.status === 'running'
  return (
    <Flex vertical gap={4} align="start">
      <Tag color={st.color} icon={busy ? <SyncOutlined spin={ai.status === 'running'} /> : <RobotOutlined />} style={{ cursor: 'pointer' }} onClick={onOpen}>{st.text}</Tag>
      {extras && ai.extra && <Tooltip title="An extra task is done: you may correct the grade of the earlier term by hand"><Tag color="gold" icon={<StarFilled />} style={{ cursor: 'pointer' }} onClick={onOpen}>Extra task done</Tag></Tooltip>}
      {ai.total > 0 && <Button type="link" size="small" style={{ padding: 0, height: 'auto' }} onClick={onOpen}>
        {ai.met}/{ai.total} criteria · {ai.points ?? '—'}{max !== null ? ` / ${max}` : ''} pts
      </Button>}
    </Flex>
  )
}

const critIcon: Record<string, React.ReactNode> = {
  met: <CheckCircleOutlined style={{ color: '#52c41a' }} />, partial: <MinusCircleOutlined style={{ color: '#faad14' }} />,
  not_met: <CloseCircleOutlined style={{ color: '#ff4d4f' }} />, unverified: <ExclamationCircleOutlined style={{ color: '#fa8c16' }} />,
}

export const places: Record<Place, { label: string; color: string }> = {
  report: { label: 'Report', color: 'blue' }, screenshots: { label: 'Screenshots', color: 'purple' }, code: { label: 'Code', color: 'cyan' }, archive: { label: 'Archive', color: 'gold' },
}

const PlaceTag = ({ where }: { where: Place }) => <Tag color={places[where]?.color} style={{ marginInlineEnd: 6 }}>{places[where]?.label ?? where}</Tag>

const critText: Record<string, string> = { met: 'Met', partial: 'Partial', not_met: 'Not met', unverified: 'Claimed, no proof' }

// AIDrawer shows what the AI check did for one work: checklist with evidence, the injection check, what it saw on screenshots.
export function AIDrawer({ id, student, open, onClose, onChanged, courseId = null }: { id: number; student: string; open: boolean; onClose: () => void; onChanged: () => void; courseId?: number | null }) {
  const { message } = App.useApp()
  const [a, setA] = useState<Assessment | null>(null), [error, setError] = useState('')
  const [tab, setTab] = useState('result')
  const load = useCallback(() => { api.assessment(id).then(setA, e => setError(errText(e))) }, [id])
  useEffect(() => { if (open) { setA(null); setError(''); setTab('result'); load() } }, [open, load])
  usePoll(load, open && (a?.status === 'queued' || a?.status === 'running'), 3000)

  const [picking, setPicking] = useState(false)

  const reassess = async (chosen?: Record<string, string>) => {
    try {
      await api.reassess(id, chosen); setPicking(false)
      message.info(chosen ? 'Queued for a new analysis with the chosen models' : 'Queued for a new analysis'); load(); onChanged()
    } catch (e) { message.error(errText(e)) }
  }

  const busy = a?.status === 'queued' || a?.status === 'running'
  const r = a?.result
  const st = a ? statusTag[a.status] : null
  return (
    <Drawer title={<Flex gap={8} align="center"><RobotOutlined />AI check: {student}</Flex>} open={open} onClose={onClose} size="large" width={920}
      extra={<Space.Compact>
        <Button icon={<RedoOutlined />} onClick={() => reassess()} disabled={busy}>Re-analyze</Button>
        <Dropdown disabled={busy} menu={{ items: [{ key: 'models', label: 'Re-analyze with other models…' }], onClick: () => setPicking(true) }}><Button icon={<DownOutlined />} /></Dropdown>
      </Space.Compact>}>
      <ModelsModal open={picking} courseId={courseId} title="Re-analyze with other models" onCancel={() => setPicking(false)} onOk={reassess}
        text={<>The work is checked again with the same checklist on the models below, and the result becomes the current one: its points and comment go into the draft.
          The course models do not change. To compare models without replacing the current result, use the “Runs &amp; models” tab.</>} />
      <Tabs activeKey={tab} onChange={setTab} items={[
        { key: 'result', label: 'Current result', children: <>
          {error && <Alert type="info" showIcon title={error} />}
          {!a && !error ? <Spin /> : a && <>
            <Descriptions size="small" column={3} style={{ marginBottom: 16 }} items={[
              { label: 'Status', children: st && <Tag color={st.color}>{st.text}</Tag> },
              { label: 'Points', children: !r ? '—' : r.injection?.suspected ? 'not graded' : r.late_penalty ? `${r.points} / ${r.max} (checklist ${r.score}, late −${r.late_penalty})` : `${r.points} / ${r.max}` },
              { label: 'Model', children: a.model ?? '—' },
              { label: 'Finished', children: fmtDate(a.finished_at) },
            ]} />
            {a.error && <Alert type="error" showIcon title="The analysis failed" description={a.error} style={{ marginBottom: 12 }} />}
            {r?.attention?.length ? <Alert type="warning" showIcon title="Needs your attention" description={<ul style={{ margin: 0, paddingLeft: 18 }}>{r.attention.map((x, i) => <li key={i}>{x}</li>)}</ul>} style={{ marginBottom: 12 }} /> : null}
            {r?.injection?.suspected && <Alert type="error" showIcon title="Possible prompt injection: the work was not graded"
              description={<>{r.injection.explanation}<ul>{r.injection.quotes?.map((q, i) => <li key={i}><Typography.Text code>{q}</Typography.Text></li>)}</ul></>} style={{ marginBottom: 12 }} />}
            {r?.injection?.unverified && <Alert type="warning" showIcon title="The injection check suspected text addressed to the grader, but its quotes were not found in the work"
              description={<>{r.injection.explanation} The work was graded; look at it yourself.<ul>{r.injection.quotes?.map((q, i) => <li key={i}><Typography.Text code>{q}</Typography.Text></li>)}</ul></>} style={{ marginBottom: 12 }} />}
            {r && !r.injection?.suspected && !r.injection?.unverified && <Alert type="success" showIcon title="No text addressed to the grader was found" style={{ marginBottom: 12 }} />}
            {r?.truncated && <Alert type="info" showIcon title="Part of the code or report did not fit into the model and was not checked" style={{ marginBottom: 12 }} />}
            {r?.criteria?.length ? (
              <Table<CriterionResult> rowKey="id" size="small" pagination={false} dataSource={r.criteria} style={{ marginBottom: 16 }}
                expandable={{ rowExpandable: c => !!c.evidence?.length, expandedRowRender: c => (
                  <Flex vertical gap={6}>{c.evidence?.map((e, i) => (
                    <div key={i}>
                      <Tag color={e.verified ? 'green' : 'red'}>{e.verified ? 'found in the work' : 'not found'}</Tag>
                      <Typography.Text type="secondary">{e.source}{e.file ? `: ${e.file}${e.line ? `:${e.line}` : ''}` : ''}</Typography.Text>
                      <pre style={{ margin: '4px 0 0', whiteSpace: 'pre-wrap', background: '#fafafa', padding: 8, borderRadius: 6, fontSize: 12 }}>{e.quote}</pre>
                    </div>))}</Flex>) }}
                columns={[
                  { title: 'Criterion', width: '38%', render: (_, c) => <>{c.where && <PlaceTag where={c.where} />}{c.text}</> },
                  { title: 'Result', width: 170, render: (_, c) => <Flex vertical gap={2}><Flex gap={6} align="center">{critIcon[c.status]}{critText[c.status] ?? c.status}</Flex>{c.no_quote && <Tooltip title="The model gave no quote, or its quote was not found in the work: check this criterion yourself"><Tag color="orange" style={{ width: 'fit-content' }}>no quote</Tag></Tooltip>}</Flex> },
                  { title: 'Points', width: 80, render: (_, c) => `${c.awarded} / ${c.points}` },
                  { title: 'Note', dataIndex: 'note' },
                ]} />
            ) : r && !r.injection?.suspected && <Empty description="No checklist results" />}
            {r?.extras?.length ? (
              <Card size="small" style={{ marginBottom: 16, borderColor: r.extras.some(x => x.status === 'met') ? '#faad14' : undefined }}
                title={<Flex gap={8} align="center"><StarFilled style={{ color: '#faad14' }} />Extra tasks (not scored)</Flex>}>
                {r.extras.map(x => (
                  <Flex key={x.id} vertical gap={4} style={{ marginBottom: 8 }}>
                    <Flex gap={8} align="center">{critIcon[x.status]}<Typography.Text strong>{critText[x.status] ?? x.status}</Typography.Text><PlaceTag where={x.where} />{x.text}</Flex>
                    {x.note && <Typography.Text type="secondary">{x.note}</Typography.Text>}
                    {x.evidence?.map((e, i) => <Typography.Text key={i} code style={{ whiteSpace: 'pre-wrap' }}>{e.verified ? '✓ ' : '✗ '}{e.file ? `${e.file}${e.line ? `:${e.line}` : ''}: ` : ''}{e.quote}</Typography.Text>)}
                  </Flex>))}
                {r.extras.some(x => x.status === 'met') && <Alert type="warning" showIcon title="The extra task is done: correct the grade of the earlier term in Moodle by hand." />}
              </Card>
            ) : null}
            {a.comment && <Card size="small" title="Suggested comment for the student" style={{ marginBottom: 16 }}><Typography.Paragraph style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{a.comment}</Typography.Paragraph></Card>}
            <History id={id} open={open} />
            {r?.pages?.length ? (
              <Collapse size="small" items={[{ key: 'p', label: `What the model saw on ${r.pages.length} screenshot(s)`, children: (
                <Flex vertical gap={12}>{r.pages.map((p, i) => (
                  <div key={i}><Typography.Text strong>{p.source}</Typography.Text><Typography.Paragraph style={{ margin: '4px 0' }}>{p.description}</Typography.Paragraph>
                    <pre style={{ whiteSpace: 'pre-wrap', background: '#fafafa', padding: 8, borderRadius: 6, fontSize: 12, maxHeight: 240, overflow: 'auto' }}>{p.transcript}</pre></div>))}</Flex>) }]} />
            ) : null}
          </>}
        </> },
        { key: 'runs', label: 'Runs & models', children: <RunsPanel id={id} courseId={courseId} open={open && tab === 'runs'} onChanged={() => { load(); onChanged() }} /> },
      ]} />
    </Drawer>
  )
}

// ChecklistCard shows the assignment checklist the AI check uses: edit it, add notes for the grader, see the prompts,
// and analyze all works again.
export function ChecklistCard({ assignmentId, onChanged, queued = 0, failed = 0, courseId = null }: { assignmentId: number; onChanged: () => void; queued?: number; failed?: number; courseId?: number | null }) {
  const { message } = App.useApp()
  const [c, setC] = useState<Checklist | null>(null), [editing, setEditing] = useState<{ text: string; where: Place; points: number }[] | null>(null)
  const [extras, setExtras] = useState<{ text: string; where: Place }[]>([])
  const [notes, setNotes] = useState(''), [preview, setPreview] = useState<PreviewStep[] | null>(null)
  const load = useCallback(() => { api.checklist(assignmentId).then(x => { setC(x); setNotes(n => n === '' ? x.notes : n) }, () => {}) }, [assignmentId])
  useEffect(load, [load])
  usePoll(() => { load(); onChanged() }, !!c?.running, 4000)

  const [includeGraded, setIncludeGraded] = useState(false)

  const run = async (newChecklist: boolean, checklistOnly = false) => {
    try {
      const r = await api.reassessAll(assignmentId, newChecklist, checklistOnly, includeGraded)
      message.info(r.queued ? 'A check of this assignment is running; yours starts right after it' : checklistOnly ? 'Building a new checklist; review it, then run “Re-analyze all”' : 'The AI check started'); setTimeout(load, 800); onChanged()
    } catch (e) { message.error(errText(e)) }
  }

  const [pickFailed, setPickFailed] = useState(false)

  const runFailed = async (models?: Record<string, string>) => {
    try { const r = await api.reassessFailed(assignmentId, models); setPickFailed(false); message.info(r.queued ? 'A check of this assignment is running; the failed works are analyzed right after it' : 'The failed works are queued for a new analysis'); setTimeout(load, 800); onChanged() } catch (e) { message.error(errText(e)) }
  }

  const saveChecklist = async () => {
    try { await api.saveChecklist(assignmentId, editing ?? [], extras.filter(x => x.text.trim())); message.success('Checklist saved. Use “Re-analyze all” to check the works with it.'); setEditing(null); load() } catch (e) { message.error(errText(e)) }
  }

  const saveNotes = async () => {
    try { await api.saveNotes(assignmentId, notes); message.success('Notes saved. They apply to the next analysis.'); load() } catch (e) { message.error(errText(e)) }
  }

  const stop = async () => {
    try { await api.stopAssess(assignmentId); message.success('The AI check is stopped'); load(); onChanged() } catch (e) { message.error(errText(e)) }
  }

  const approve = async () => {
    try { await api.approveChecklist(assignmentId); message.success('Checklist approved: works are checked with it after each sync'); load() } catch (e) { message.error(errText(e)) }
  }

  const showPrompt = async () => { try { setPreview(await api.promptPreview(assignmentId)) } catch (e) { message.error(errText(e)) } }
  const p = c?.progress
  const sum = (editing ?? c?.checklist?.criteria ?? []).reduce((n, x) => n + (Number(x.points) || 0), 0)
  return (
    <Card size="small" style={{ marginBottom: 16 }} title={<Flex gap={8} align="center"><RobotOutlined />AI check
      {c?.checklist && <Tag color={c.checklist.source === 'teacher' ? 'blue' : 'default'}>{c.checklist.source === 'teacher' ? 'your checklist' : `generated by ${c.checklist.model}`}</Tag>}
      {c?.checklist && (c.checklist.approved ? <Tag color="green">approved</Tag> : <Tag color="orange">not approved: works are not checked after a sync</Tag>)}</Flex>}
    extra={<Flex gap={8} wrap>
      {c?.checklist && !c.checklist.approved && <Button size="small" type="primary" icon={<CheckCircleOutlined />} onClick={approve}>Approve checklist</Button>}
      <Button size="small" icon={<EyeOutlined />} onClick={showPrompt}>Show prompt</Button>
      {(c?.running || queued > 0) && <Popconfirm title="Stop the AI check of this assignment?" description="Works not analyzed yet leave the queue; finished results stay." onConfirm={stop}>
        <Button size="small" danger icon={<StopOutlined />}>Stop AI check</Button></Popconfirm>}
      <Popconfirm title="Analyze the works again?" onConfirm={() => run(false)} description={<Flex vertical gap={4}>
        <span>Runs on the model server, one work at a time; it can take a while.</span>
        <Checkbox checked={includeGraded} onChange={e => setIncludeGraded(e.target.checked)}>Including works already graded in Moodle</Checkbox></Flex>}><Button size="small" icon={<RedoOutlined />} disabled={c?.running}>Re-analyze all</Button></Popconfirm>
      {failed > 0 && <Space.Compact>
        <Popconfirm title={`Analyze the ${failed} failed works again?`} description="On the course models, with the current checklist." onConfirm={() => runFailed()}>
          <Button size="small" danger icon={<RedoOutlined />} disabled={c?.running}>Re-analyze failed ({failed})</Button></Popconfirm>
        <Dropdown disabled={c?.running} menu={{ items: [{ key: 'models', label: 'Re-analyze failed with other models…' }], onClick: () => setPickFailed(true) }}><Button size="small" danger icon={<DownOutlined />} /></Dropdown>
      </Space.Compact>}
      <ModelsModal open={pickFailed} courseId={courseId} title={`Re-analyze ${failed} failed works with other models`} onCancel={() => setPickFailed(false)} onOk={runFailed}
        text="The failed works are checked again with the current checklist on the models below; the results become the current ones and go into the drafts. The course models do not change." />
      <Popconfirm title="Generate a new checklist from the assignment description?" description={c?.checklist?.source === 'teacher' ? 'Your edited checklist will be replaced. Works are not analyzed until you run “Re-analyze all”.' : 'Works are not analyzed until you run “Re-analyze all”.'} onConfirm={() => run(true, true)}><Button size="small" disabled={c?.running}>New checklist</Button></Popconfirm>
    </Flex>}>
      {c?.models_problem && <Alert type="warning" showIcon style={{ marginBottom: 8 }} title="The AI check cannot run yet" description={c.models_problem} />}
      {c?.running && p && <Flex gap={12} align="center" style={{ marginBottom: 8 }}>
        <Typography.Text>{p.stage === 'EnsureRubric' ? 'Preparing the checklist…' : `Analyzing works: ${p.Done} of ${p.Total}`}</Typography.Text>
        <Progress percent={p.Total ? Math.floor((100 * p.Done) / p.Total) : 0} size="small" style={{ flex: 1, maxWidth: 400 }} status="active" />
      </Flex>}
      {!c ? <Spin size="small" /> : (
        <Row gutter={24}>
          <Col xs={24} lg={14}>
            <Flex justify="space-between" align="center" style={{ marginBottom: 6 }}>
              <Typography.Text strong>Checklist</Typography.Text>
              {!editing ? <Button size="small" icon={<EditOutlined />} onClick={() => { setEditing((c.checklist?.criteria ?? []).map(x => ({ text: x.text, where: x.where || 'code', points: x.points }))); setExtras((c.checklist?.extras ?? []).map(x => ({ text: x.text, where: x.where || 'code' }))) }}>Edit</Button>
              : <Flex gap={8}><Button size="small" type="primary" icon={<SaveOutlined />} onClick={saveChecklist}>Save</Button><Button size="small" onClick={() => setEditing(null)}>Cancel</Button></Flex>}
            </Flex>
            {editing ? (
              <Flex vertical gap={6}>
                {editing.map((x, i) => (
                  <Flex key={i} gap={6}>
                    <Select value={x.where} style={{ width: 130, flex: 'none' }} onChange={v => setEditing(ed => ed!.map((y, j) => j === i ? { ...y, where: v } : y))}
                      options={(Object.keys(places) as Place[]).map(k => ({ value: k, label: places[k].label }))} />
                    <Input value={x.text} onChange={e => setEditing(ed => ed!.map((y, j) => j === i ? { ...y, text: e.target.value } : y))} placeholder="Criterion" />
                    <InputNumber min={0.5} step={0.5} value={x.points} onChange={v => setEditing(ed => ed!.map((y, j) => j === i ? { ...y, points: Number(v) || 0 } : y))} style={{ width: 90 }} />
                    <Button icon={<DeleteOutlined />} onClick={() => setEditing(ed => ed!.filter((_, j) => j !== i))} />
                  </Flex>))}
                <Flex justify="space-between"><Button size="small" icon={<PlusOutlined />} onClick={() => setEditing(ed => [...ed!, { text: '', where: 'report', points: 1 }])}>Add criterion</Button>
                  <Typography.Text type={c.max_grade !== null && sum !== c.max_grade ? 'danger' : 'secondary'}>Total: {sum}{c.max_grade !== null ? ` of ${c.max_grade}` : ''}</Typography.Text></Flex>
                {c.settings.extras_enabled && <>
                  <Typography.Text strong style={{ marginTop: 8 }}><StarFilled style={{ color: '#faad14' }} /> Extra tasks (not scored)</Typography.Text>
                  {extras.map((x, i) => (
                    <Flex key={i} gap={6}>
                      <Select value={x.where} style={{ width: 130, flex: 'none' }} onChange={v => setExtras(ex => ex.map((y, j) => j === i ? { ...y, where: v } : y))}
                        options={(Object.keys(places) as Place[]).map(k => ({ value: k, label: places[k].label }))} />
                      <Input value={x.text} onChange={e => setExtras(ex => ex.map((y, j) => j === i ? { ...y, text: e.target.value } : y))} placeholder="Extra task" />
                      <Button icon={<DeleteOutlined />} onClick={() => setExtras(ex => ex.filter((_, j) => j !== i))} />
                    </Flex>))}
                  <Button size="small" icon={<PlusOutlined />} style={{ width: 'fit-content' }} onClick={() => setExtras(ex => [...ex, { text: '', where: 'code' }])}>Add extra task</Button>
                </>}
              </Flex>
            ) : !c.checklist ? <Typography.Text type="secondary">No checklist yet: it is generated from the assignment description on the first AI check, or write your own with Edit.</Typography.Text> : (
              <>
                <ol style={{ margin: 0, paddingLeft: 20 }}>{c.checklist.criteria.map(x => <li key={x.id} style={{ marginBottom: 2 }}>{x.where && <PlaceTag where={x.where} />}{x.text} <Typography.Text type="secondary">({x.points} pts)</Typography.Text></li>)}</ol>
                <Typography.Text type="secondary">Total: {sum} points</Typography.Text>
                {c.settings.extras_enabled && c.checklist.extras?.length ? <>
                  <div style={{ marginTop: 8 }}><Typography.Text strong><StarFilled style={{ color: '#faad14' }} /> Extra tasks (not scored)</Typography.Text></div>
                  <ul style={{ margin: 0, paddingLeft: 20 }}>{c.checklist.extras.map(x => <li key={x.id}>{x.where && <PlaceTag where={x.where} />}{x.text}</li>)}</ul>
                </> : null}
              </>
            )}
          </Col>
          <Col xs={24} lg={10}>
            <Typography.Text strong>Notes for the grader</Typography.Text>
            <Input.TextArea value={notes} onChange={e => setNotes(e.target.value)} autoSize={{ minRows: 3, maxRows: 8 }} style={{ marginTop: 6 }}
              placeholder="For example: any web framework is fine; a Swagger screenshot is required; tests are not needed." />
            <Button size="small" icon={<SaveOutlined />} style={{ marginTop: 6 }} disabled={notes === c.notes} onClick={saveNotes}>Save notes</Button>
          </Col>
        </Row>
      )}
      <Modal open={!!preview} title="Prompts for this assignment, as the model gets them" onCancel={() => setPreview(null)} footer={null} width="80vw" style={{ top: 24 }}>
        <Typography.Paragraph type="secondary">The student's work is inserted where the placeholder is. Edit the templates on the <a href="#/prompts">Prompts</a> page.</Typography.Paragraph>
        <Collapse items={(preview ?? []).map(s => ({ key: s.key, label: s.title, children: <>
          <Typography.Text strong>Instructions (system)</Typography.Text><pre className="prompt-pre">{s.system}</pre>
          <Typography.Text strong>Request (user)</Typography.Text><pre className="prompt-pre">{s.user}</pre>
        </> }))} />
      </Modal>
    </Card>
  )
}

// AssignmentDescription is the assignment text from Moodle, folded by default.
export function AssignmentDescription({ assignmentId }: { assignmentId: number }) {
  const [text, setText] = useState<string | null>(null)
  useEffect(() => { api.checklist(assignmentId).then(c => setText(c.description), () => setText('')) }, [assignmentId])

  if (!text) return null
  return (
    <Collapse style={{ marginBottom: 16, background: '#fff' }} items={[{ key: 'd', label: <Typography.Text strong>Assignment</Typography.Text>,
      children: <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', margin: 0, maxHeight: '60vh', overflow: 'auto' }}>{text}</Typography.Paragraph> }]} />
  )
}

const eventText: Record<string, { color: string; label: string }> = {
  upload: { color: 'blue', label: 'Uploaded' }, ai: { color: 'purple', label: 'AI check' }, published: { color: 'green', label: 'Published to Moodle' },
}

// History is the review of this student's work on the assignment over all attempts: uploads, AI checks, your grades.
function History({ id, open }: { id: number; open: boolean }) {
  const [h, setH] = useState<HistoryEvent[] | null>(null)
  useEffect(() => { if (open) api.history(id).then(setH, () => setH([])) }, [id, open])

  if (!h?.length) return null
  return (
    <Card size="small" title="History" style={{ marginBottom: 16 }}>
      <Timeline items={h.map(e => ({
        color: eventText[e.kind]?.color,
        children: <>
          <Typography.Text strong>{eventText[e.kind]?.label ?? e.kind}</Typography.Text>{' '}
          <Typography.Text type="secondary">{fmtDate(e.at)} · attempt {e.attempt + 1}{e.by ? ` · ${e.by}` : ''}</Typography.Text>
          {e.points != null && <div>Points: {e.points}{e.kind === 'ai' && e.details?.total ? ` · ${e.details.met}/${e.details.total} criteria` : ''}{e.details?.late_penalty ? ` · late −${e.details.late_penalty}` : ''}</div>}
          {e.comment && <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', margin: '4px 0 0', fontSize: 13 }} ellipsis={{ rows: 4, expandable: true }}>{e.comment}</Typography.Paragraph>}
        </>,
      }))} />
    </Card>
  )
}
