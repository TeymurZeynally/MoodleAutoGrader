import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Card, Col, Flex, Input, Popconfirm, Row, Select, Spin, Tag, Typography } from 'antd'
import { SaveOutlined, UndoOutlined } from '@ant-design/icons'
import { api, errText, fmtDate, PromptPart, PromptStep } from '../api'

// PromptsPage edits the templates the AI check sends to the model. They are shared by all teachers. Code still wraps
// the student's work, forces the answer format, checks every quote and sums the points, whatever the prompt says.
export function PromptsPage({ courseId }: { courseId: number }) {
  const { message } = App.useApp()
  const [steps, setSteps] = useState<PromptStep[] | null>(null), [models, setModels] = useState<string[]>([]), [aiOn, setAiOn] = useState(true)

  const load = useCallback(() => {
    api.prompts(courseId).then(setSteps, e => message.error(errText(e)))
    api.models().then(setModels, () => {})
    api.aiStatus().then(s => setAiOn(s.enabled), () => {})
  }, [courseId, message])

  useEffect(load, [load])

  const useForAll = async (m: string) => {
    try {
      await Promise.all((steps ?? []).map(s => api.savePrompt(courseId, `${s.key}.model`, m)))
      message.success(`Model for every step: ${m}`); load()
    } catch (e) { message.error(errText(e)) }
  }

  const unset = (steps ?? []).filter(s => !s.model.body)
  return (
    <Flex vertical gap={16}>
      {aiOn && steps && !models.length && <Alert type="warning" showIcon title="No models downloaded yet" action={<Button size="small" onClick={load}>Check again</Button>}
        description={<>The AI check runs on models you download. From the <Typography.Text code>deploy</Typography.Text> folder:
          {' '}<Typography.Text code copyable>docker compose run --rm ollama-cli pull MODEL</Typography.Text>; the models are listed at <a href="https://ollama.com/library" target="_blank" rel="noreferrer">ollama.com/library</a>.</>} />}
      {aiOn && models.length > 0 && unset.length > 0 && <Alert type="warning" showIcon title="The AI check of this course waits for its models"
        description={`Works are not checked until every step has a model. No model yet for: ${unset.map(s => s.key).join(', ')}.`}
        action={<Select placeholder="Use one model for every step" style={{ minWidth: 240 }} onChange={useForAll} options={models.map(m => ({ value: m, label: m }))} />} />}
      <Alert type="info" showIcon title="Prompts and models of the AI check for this course. Saving a prompt re-analyzes works on the next run."
        description={<>Templates use Go syntax: <Typography.Text code>{'{{.Assignment}}'}</Typography.Text>, <Typography.Text code>{'{{if .TeacherNotes}}…{{end}}'}</Typography.Text>.
          Whatever the prompt says, the code wraps the student's work as data, forces the answer format, verifies every quote and computes the points.</>} />
      {!steps ? <Spin /> : steps.map(s => <StepCard key={s.key} courseId={courseId} step={s} models={models} onSaved={load} />)}
    </Flex>
  )
}

function StepCard({ courseId, step, models, onSaved }: { courseId: number; step: PromptStep; models: string[]; onSaved: () => void }) {
  const { message } = App.useApp()

  const setModel = async (m?: string) => {
    try { await api.savePrompt(courseId, `${step.key}.model`, m ?? ''); message.success(m ? `Model: ${m}` : 'Model cleared'); onSaved() } catch (e) { message.error(errText(e)) }
  }

  const current = step.model.body
  return (
    <Card title={step.title} size="small" extra={<Flex gap={4} wrap>{step.vars.map(v => <Tag key={v} color={step.required?.includes(v) ? 'blue' : 'default'}>{`{{.${v}}}`}</Tag>)}</Flex>}>
      <Flex gap={8} align="center" style={{ marginBottom: 12 }}>
        <Typography.Text strong>Model</Typography.Text>
        <Select value={current || undefined} onChange={setModel} allowClear placeholder="Choose a model" status={current ? undefined : 'warning'} style={{ minWidth: 260 }}
          options={[...new Set([...(current ? [current] : []), ...models])].map(m => ({ value: m, label: m + (models.length && !models.includes(m) ? ' (not downloaded)' : '') }))} />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>Models that can think reason before answering: slower, more careful. Reading screenshots needs a vision model.</Typography.Text>
      </Flex>
      <Row gutter={16}>
        <Col xs={24} lg={12}><Part courseId={courseId} label="Instructions (system)" k={`${step.key}.system`} part={step.system} onSaved={onSaved} /></Col>
        <Col xs={24} lg={12}><Part courseId={courseId} label="Request (user)" k={`${step.key}.user`} part={step.user} onSaved={onSaved} required={step.required} /></Col>
      </Row>
    </Card>
  )
}

function Part({ courseId, label, k, part, onSaved, required }: { courseId: number; label: string; k: string; part: PromptPart; onSaved: () => void; required?: string[] | null }) {
  const { message } = App.useApp()
  const [body, setBody] = useState(part.body), [busy, setBusy] = useState(false)
  useEffect(() => setBody(part.body), [part.body])

  const save = async (value: string) => {
    setBusy(true)
    try { await api.savePrompt(courseId, k, value); message.success(value ? 'Prompt saved' : 'Default restored'); onSaved() } catch (e) { message.error(errText(e)) } finally { setBusy(false) }
  }

  return (
    <Flex vertical gap={6}>
      <Flex justify="space-between" align="center">
        <Typography.Text strong>{label}</Typography.Text>
        {part.edited ? <Tag color="orange">edited{part.edited.by ? ` by ${part.edited.by}` : ''} · {fmtDate(part.edited.at)}</Tag> : <Tag>default</Tag>}
      </Flex>
      <Input.TextArea value={body} onChange={e => setBody(e.target.value)} autoSize={{ minRows: 4, maxRows: 18 }} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 12 }} />
      {required?.length ? <Typography.Text type="secondary" style={{ fontSize: 12 }}>Must use: {required.map(v => `{{.${v}}}`).join(', ')}</Typography.Text> : null}
      <Flex gap={8}>
        <Button size="small" type="primary" icon={<SaveOutlined />} loading={busy} disabled={body === part.body} onClick={() => save(body)}>Save</Button>
        {body !== part.body && <Button size="small" onClick={() => setBody(part.body)}>Cancel</Button>}
        {part.edited && <Popconfirm title="Restore the default prompt?" onConfirm={() => save('')}><Button size="small" icon={<UndoOutlined />}>Reset to default</Button></Popconfirm>}
      </Flex>
    </Flex>
  )
}
