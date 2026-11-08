import { useCallback, useEffect, useState } from 'react'
import { Badge, Tooltip, Typography } from 'antd'
import { RobotOutlined } from '@ant-design/icons'
import { AIStatus, api, duration } from '../api'
import { useNow, usePoll } from '../App'

// useAIStatus polls what the model is doing; every page that shows AI progress shares the same answer.
export function useAIStatus(ms = 4000) {
  const [st, setSt] = useState<AIStatus | null>(null)
  const load = useCallback(() => { api.aiStatus().then(setSt, () => {}) }, [])
  useEffect(load, [load])
  usePoll(load, true, ms)

  return st
}

// AIStatusBadge is the header indicator: the job on the model server right now, its step and the queue.
export function AIStatusBadge() {
  const st = useAIStatus()
  const now = useNow(!!st?.activity)

  if (!st?.enabled) return null
  const a = st.activity
  if (!a) {
    return <Typography.Text type="secondary"><RobotOutlined /> AI idle{st.queued ? ` · ${st.queued} in queue` : ''}</Typography.Text>
  }

  const silent = now - new Date(a.updated_at).getTime()
  const what = a.kind === 'checklist' ? 'building the checklist' : `${a.student ?? 'a work'} · ${a.step}`
  const text = `AI: ${what} · ${duration(now - new Date(a.started_at).getTime())}${st.queued ? ` · ${st.queued} in queue` : ''}`
  return (
    <Tooltip title={<>{a.assignment}<br />{a.kind === 'work' ? `Step: ${a.step}` : 'The checklist is built from the assignment description'}{silent > 600_000 ? <><br />No update for {duration(silent)}: the job may have stopped</> : null}</>}>
      <a href={a.assignment_id ? `#/assignment/${a.assignment_id}` : '#/courses'} style={{ color: 'rgba(0,0,0,.88)', maxWidth: 560, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        <Badge status={silent > 600_000 ? 'warning' : 'processing'} /> <RobotOutlined /> {text}
      </a>
    </Tooltip>
  )
}
