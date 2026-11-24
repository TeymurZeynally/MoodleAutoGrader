import { useCallback, useEffect, useState } from 'react'
import { Button, Flex, Layout, Spin, Typography, Tooltip } from 'antd'
import { HomeOutlined, LogoutOutlined, UserOutlined } from '@ant-design/icons'
import { api } from './api'
import { Login } from './pages/Login'
import { CoursesPage } from './pages/Courses'
import { CoursePage } from './pages/Course'
import { AssignmentPage } from './pages/Assignment'
import { StudentPage } from './pages/Student'
import { AIStatusBadge } from './pages/AIStatus'

// Minimal hash router: #/courses, #/course/:id[/students|/settings|/prompts], #/course/:id/student/:id, #/assignment/:id
function useRoute() {
  const [hash, setHash] = useState(location.hash || '#/courses')
  useEffect(() => { const on = () => setHash(location.hash || '#/courses'); addEventListener('hashchange', on); return () => removeEventListener('hashchange', on) }, [])

  const [, page, id, view, sub] = hash.split('/')
  return { page, id: Number(id), view, sub: sub ? Number(sub) : null }
}

// usePoll re-runs fn every ms while active.
export function usePoll(fn: () => void, active: boolean, ms = 2000) {
  useEffect(() => { if (!active) return; const t = setInterval(fn, ms); return () => clearInterval(t) }, [fn, active, ms])
}

// useNow ticks once a second, for elapsed times and "last activity N s ago".
export function useNow(active = true) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => { if (!active) return; const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [active])

  return now
}

export function App() {
  const [user, setUser] = useState<string | null | undefined>(undefined)
  const loadMe = useCallback(() => { api.me().then(m => setUser(m.name), () => setUser(null)) }, [])
  useEffect(loadMe, [loadMe])

  const route = useRoute()

  if (user === undefined) return <Flex justify="center" style={{ marginTop: '30vh' }}><Spin size="large" /></Flex>
  if (user === null) return <Login onDone={loadMe} />
  const logout = async () => { await api.logout(); setUser(null) }
  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Layout.Header style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '0 24px', background: '#fff', borderBottom: '1px solid #f0f0f0' }}>
        <Tooltip title="All courses"><Button type="text" href="#/courses" aria-label="All courses" icon={<HomeOutlined style={{ fontSize: 18 }} />} /></Tooltip>
        <span style={{ flex: 1 }} />
        <AIStatusBadge />
        <Typography.Text><UserOutlined /> {user}</Typography.Text>
        <Button icon={<LogoutOutlined />} onClick={logout}>Sign out</Button>
      </Layout.Header>
      <Layout.Content style={{ padding: 24 }}>
        {route.page === 'course' && route.view === 'student' && route.sub !== null ? <StudentPage courseId={route.id} studentId={route.sub} />
        : route.page === 'course' ? <CoursePage id={route.id} view={route.view} /> : route.page === 'assignment' ? <AssignmentPage id={route.id} /> : <CoursesPage />}
      </Layout.Content>
    </Layout>
  )
}
