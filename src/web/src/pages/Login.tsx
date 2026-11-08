import { useEffect, useState } from 'react'
import { Alert, Button, Card, Checkbox, Flex, Form, Input, Tabs, Typography } from 'antd'
import { GlobalOutlined, KeyOutlined, LockOutlined, UserOutlined } from '@ant-design/icons'
import { ApiError, api, errText } from '../api'

export function Login({ onDone }: { onDone: () => void }) {
  const [error, setError] = useState(''), [busy, setBusy] = useState(false)
  const [site, setSite] = useState<string | null>(null), [typed, setTyped] = useState('') // null: not known yet
  const [untrusted, setUntrusted] = useState(false), [skipVerify, setSkipVerify] = useState(false) // the certificate of the typed site did not verify

  useEffect(() => { api.site().then(s => setSite(s.url), () => setSite('')) }, [])

  const typeSite = (address: string) => { setTyped(address); setUntrusted(false); setSkipVerify(false) }

  const submit = async (creds: { token?: string; username?: string; password?: string }) => {
    if (!site && !typed.trim()) { setError('Enter the address of your Moodle site.'); return }
    setError(''); setBusy(true)
    try { await api.login({ ...creds, site: site || typed, skip_verify: skipVerify }); onDone() } catch (e) { setError(errText(e)); if (e instanceof ApiError && e.status === 495) setUntrusted(true) } finally { setBusy(false) }
  }

  const where = site
    ? <Typography.Paragraph type="secondary">Moodle: <Typography.Link href={site} target="_blank" rel="noreferrer">{site}</Typography.Link></Typography.Paragraph>
    : <Form layout="vertical" component="div"><Form.Item label="Moodle site" extra="The address you open Moodle at. The first sign-in sets it for this installation."><Input prefix={<GlobalOutlined />} placeholder="https://moodle.example.edu" value={typed} onChange={e => typeSite(e.target.value)} autoComplete="url" /></Form.Item>{untrusted && <Form.Item extra="Anyone between this server and Moodle could then read the password and the token. The choice stays for this installation."><Checkbox checked={skipVerify} onChange={e => setSkipVerify(e.target.checked)}>Do not verify the certificate of this site</Checkbox></Form.Item>}</Form>

  const account = (
    <Form layout="vertical" onFinish={submit} requiredMark={false}>
      <Form.Item name="username" label="Moodle username" rules={[{ required: true }]}><Input prefix={<UserOutlined />} autoComplete="username" /></Form.Item>
      <Form.Item name="password" label="Password" rules={[{ required: true }]} extra="Exchanged once for a Moodle token; the password is never stored."><Input.Password prefix={<LockOutlined />} autoComplete="current-password" /></Form.Item>
      <Button type="primary" htmlType="submit" block loading={busy}>Sign in</Button>
    </Form>
  )

  const token = (
    <Form layout="vertical" onFinish={submit} requiredMark={false}>
      <Form.Item name="token" label="Moodle token" rules={[{ required: true }]} extra="Profile → Preferences → Security keys → Moodle mobile web service."><Input.Password prefix={<KeyOutlined />} autoComplete="off" /></Form.Item>
      <Button type="primary" htmlType="submit" block loading={busy}>Sign in</Button>
    </Form>
  )

  return (
    <Flex justify="center" align="center" style={{ minHeight: '100vh', background: 'linear-gradient(135deg, #e6f0ff, #f5f7fa)' }}>
      <Card style={{ width: 420, boxShadow: '0 8px 24px rgba(0,0,0,.08)' }}>
        <Typography.Title level={3} style={{ marginTop: 0 }}>MoodleAutoGrader</Typography.Title>
        <Typography.Paragraph type="secondary">Sign in with your Moodle account. The token is kept only in an encrypted cookie for 12 hours.</Typography.Paragraph>
        {site !== null && where}
        {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 16 }} />}
        <Tabs items={[{ key: 'account', label: 'Username and password', children: account }, { key: 'token', label: 'Token', children: token }]} />
      </Card>
    </Flex>
  )
}
