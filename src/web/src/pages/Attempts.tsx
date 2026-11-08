import { Button, Card, Empty, Flex, Modal, Tag, Typography } from 'antd'
import { CommentOutlined, FileOutlined, FolderOpenOutlined } from '@ant-design/icons'
import { fmtDate, Submission } from '../api'

export const statusText: Record<string, string> = {
  none: 'Not started', new: 'Nothing uploaded', draft: 'Draft, not submitted', reopened: 'Reopened, nothing uploaded', submitted: 'Submitted',
}

// attemptCount is how many attempts the student has in Moodle: the table shows one of them.
export const attemptCount = (s: Submission) => Math.max(s.attempts.length, s.attempt + 1, (s.open_attempt ?? -1) + 1)

// gradeInMoodle is the grade of the attempt shown. Moodle's list of grades knows only the latest attempt, so the grade of
// an attempt it reopened comes from the attempts.
export const gradeInMoodle = (s: Submission) => s.attempts.find(a => a.attempt === s.attempt)?.grade ?? s.moodle_grade

// lastEarlier is the newest earlier attempt that has what is asked for.
export const lastEarlier = (s: Submission, has: (a: Submission['attempts'][number]) => boolean) => s.attempts.filter(a => a.attempt < s.attempt && has(a)).at(-1)

// AttemptsModal shows every attempt of a student, newest first: what was uploaded, the grade and the comment in Moodle.
export function AttemptsModal({ s, max, onClose, onViewFiles }: { s: Submission | null; max: number | null; onClose: () => void; onViewFiles: (submissionId: number) => void }) {
  const list = [...(s?.attempts ?? [])].reverse()
  return (
    <Modal title={s ? `Attempts: ${s.student}` : ''} open={!!s} onCancel={onClose} footer={null} width={720}>
      {list.length === 0 ? <Empty description="Moodle has not reported the attempts of this student yet: they are read at the next sync." /> : (
        <Flex vertical gap={12}>
          {list.map(a => (
            <Card key={a.attempt} size="small" title={<Flex gap={8} align="center" wrap>
              Attempt {a.attempt + 1}
              <Tag color={a.status === 'submitted' ? 'green' : 'gold'} style={{ marginInlineEnd: 0 }}>{statusText[a.status] ?? a.status}</Tag>
              {a.attempt === s?.attempt && <Tag color="blue" style={{ marginInlineEnd: 0 }}>Shown in the table</Tag>}
            </Flex>}>
              <Flex vertical gap={8}>
                {a.status === 'submitted' && <Typography.Text type="secondary">Uploaded {fmtDate(a.submitted_at)}</Typography.Text>}
                {a.files.length > 0 && <Flex gap={4} wrap align="center">
                  {a.files.map(f => <Tag key={f} className="wrap-tag" icon={<FileOutlined />} title={f}>{f}</Tag>)}
                  {a.submission_id !== null && <Button size="small" icon={<FolderOpenOutlined />} onClick={() => { if (a.submission_id !== null) onViewFiles(a.submission_id) }}>View files</Button>}
                </Flex>}
                <div>
                  {a.grade === null ? <Typography.Text type="secondary">No grade in Moodle</Typography.Text>
                  : <><Tag color="blue">{a.grade}{max !== null ? ` / ${max}` : ''}</Tag><Typography.Text type="secondary">graded {fmtDate(a.graded_at)}</Typography.Text></>}
                </div>
                {a.comment ? <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', margin: 0 }}><CommentOutlined /> {a.comment}</Typography.Paragraph>
                : <Typography.Text type="secondary">No comment</Typography.Text>}
              </Flex>
            </Card>
          ))}
        </Flex>
      )}
    </Modal>
  )
}
