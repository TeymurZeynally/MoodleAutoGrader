import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Breadcrumb, Button, Card, Flex, Input, Popconfirm, Select, Space, Statistic, Table, Tag, Tooltip, Typography } from 'antd'
import type { TableColumnsType } from 'antd'
import { CheckOutlined, CloudUploadOutlined, SyncOutlined } from '@ant-design/icons'
import { api, Course, CourseSettings, errText, fmtDate, Student, Submission } from '../api'
import { usePoll } from '../App'
import { gradeInMoodle } from './Attempts'
import { attention, ready, submitted, unremarkable, useSheet } from './Sheet'

const groupsOf = (s: Student) => (s.groups.length ? s.groups : ['No group'])

// StudentsTab lists the students of a course, as the Assignments tab lists its assignments; a student opens their page.
export function StudentsTab({ courseId }: { courseId: number }) {
  const { message } = App.useApp()
  const [students, setStudents] = useState<Student[] | null>(null), [query, setQuery] = useState(''), [group, setGroup] = useState('all')
  useEffect(() => { api.students(courseId).then(list => setStudents(list ?? []), e => message.error(errText(e))) }, [courseId, message])

  const groups = [...new Set((students ?? []).flatMap(groupsOf))].sort()
  const q = query.trim().toLowerCase()
  const shown = (students ?? []).filter(s => (group === 'all' || groupsOf(s).includes(group)) && (!q || `${s.name} ${s.short_name}`.toLowerCase().includes(q)))
  const columns: TableColumnsType<Student> = [
    {
      title: 'Student', render: (_, s) => <Flex gap={8} align="center">
        <a href={`#/course/${courseId}/student/${s.id}`} style={{ fontWeight: 500 }}>{s.name}</a>
        {!s.enrolled && <Tooltip title="No longer enrolled in the course (e.g. a graduated group)"><Tag>Left the course</Tag></Tooltip>}
      </Flex>,
    },
    { title: 'Group', width: 220, render: (_, s) => s.groups.join(', ') || '—' },
    { title: 'Submitted', dataIndex: 'submitted', width: 120 },
    { title: 'Graded in Moodle', dataIndex: 'graded', width: 160 },
    { title: 'Need attention', dataIndex: 'attention', width: 140, render: (n: number) => n ? <Tag color="red">{n}</Tag> : <Tag color="green">0</Tag> },
  ]

  return (
    <>
      <Flex gap={12} wrap style={{ marginBottom: 16 }}>
        <Input.Search placeholder="Find a student" allowClear value={query} onChange={e => setQuery(e.target.value)} style={{ width: 260 }} />
        <Select value={group} onChange={setGroup} style={{ minWidth: 220 }} options={[{ value: 'all', label: 'All groups' }, ...groups.map(g => ({ value: g, label: g }))]} />
      </Flex>
      <Table rowKey="id" size="middle" loading={students === null} dataSource={shown} columns={columns} pagination={false} />
    </>
  )
}

// StudentPage is the grade sheet of one student: every assignment of the course, with the columns, edits and publishing
// of the sheet of an assignment.
export function StudentPage({ courseId, studentId }: { courseId: number; studentId: number }) {
  const { message } = App.useApp()
  const [student, setStudent] = useState<Student | null>(null), [rows, setRows] = useState<Submission[] | null>(null)
  const [settings, setSettings] = useState<CourseSettings | null>(null), [course, setCourse] = useState<Course | null>(null)
  useEffect(() => {
    api.courseSettings(courseId).then(setSettings, () => {})
    api.courses().then(cs => setCourse(cs.find(c => c.id === courseId) ?? null), () => {})
  }, [courseId])

  const load = useCallback(() => {
    api.studentSheet(courseId, studentId).then(r => { setStudent(r.student); setRows(r.submissions ?? []) }, e => message.error(errText(e)))
  }, [courseId, studentId, message])

  const { dirty, selected, setSelected, busy, setBusy, publish, columns: sheetColumns, modals } = useSheet({ load, settings, name: s => s.assignment, pointsTitle: 'Points', perRowMax: true })

  useEffect(load, [load])
  usePoll(load, (rows ?? []).some(s => s.state === 'approved' || s.ai?.status === 'running' || s.ai?.status === 'queued'), 4000)

  const list = rows ?? []
  const count = (f: (s: Submission) => boolean) => list.filter(f).length
  const publishable = selected.filter(id => list.some(s => s.id === id && ready(s)))
  const filesOff = settings?.download_files === false
  const toAnalyze = list.filter(s => submitted(s) && (selected.length === 0 || selected.includes(s.id)))

  // reanalyze checks the chosen works again, or every submitted one, assignment by assignment; a work of an assignment
  // without a checklist is left out: the check would build one the teacher has not seen.
  const reanalyze = async () => {
    const byAssignment = new Map<number, Submission[]>()
    for (const s of toAnalyze.filter(s => s.has_checklist)) byAssignment.set(s.assignment_id, [...(byAssignment.get(s.assignment_id) ?? []), s])
    const skipped = toAnalyze.filter(s => !s.has_checklist).map(s => s.assignment)
    setBusy(true)
    let started = 0, queued = 0
    for (const works of byAssignment.values()) {
      try {
        const r = await api.reassessSelected(works[0].assignment_id, works.map(s => s.id))
        if (r.queued) queued += works.length
        else started += works.length
      } catch (e) { message.error(`${works[0].assignment}: ${errText(e)}`) }
    }

    setBusy(false); setSelected([])
    if (started) message.info(`Analyzing ${started} work(s) again…`)
    if (queued) message.info(`${queued} work(s) are analyzed right after the check of their assignment that is running now`)
    if (skipped.length) message.warning(`No checklist yet, not analyzed: ${skipped.join(', ')}`)
    load()
  }

  const columns: TableColumnsType<Submission> = [
    {
      title: 'Assignment', width: 200, render: (_, s) => <Flex vertical>
        <a href={`#/assignment/${s.assignment_id}`}><Typography.Text strong>{s.assignment}</Typography.Text></a>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>Due {fmtDate(s.due_at)}</Typography.Text>
      </Flex>,
    },
    ...sheetColumns,
  ]

  return (
    <>
      <Breadcrumb style={{ marginBottom: 16 }} items={[{ title: <a href="#/courses">Courses</a> }, { title: <a href={`#/course/${courseId}`}>{course?.name ?? '…'}</a> },
        { title: <a href={`#/course/${courseId}/students`}>Students</a> }, { title: student ? student.short_name || student.name : '…' }]} />
      <Card style={{ marginBottom: 16 }}>
        <Flex gap={12} align="center" wrap style={{ marginBottom: 12 }}>
          <Typography.Title level={4} style={{ margin: 0 }}>{student?.name ?? '…'}</Typography.Title>
          {student?.groups.map(g => <Tag key={g}>{g}</Tag>)}
          {student && !student.enrolled && <Tooltip title="No longer enrolled in the course (e.g. a graduated group)"><Tag>Left the course</Tag></Tooltip>}
        </Flex>
        <Flex gap={40} wrap>
          <Statistic title="Assignments" value={list.length} />
          <Statistic title="Submitted" value={count(submitted)} />
          <Statistic title="Not submitted" value={count(s => !submitted(s))} />
          <Statistic title="Need attention" value={count(attention)} styles={{ content: { color: count(attention) ? '#cf1322' : undefined } }} />
          <Statistic title="Graded in Moodle" value={count(s => gradeInMoodle(s) !== null)} />
          <Statistic title="Drafts ready to publish" value={count(ready)} />
        </Flex>
      </Card>
      {filesOff && <Alert type="info" showIcon style={{ marginBottom: 16 }} title="Files of this course are not downloaded"
        description={<>The sheet shows the grades, comments and attempts from Moodle. Files, the similarity check and the AI check are off: turn on “Download files” in the <a href={`#/course/${courseId}/settings`}>course settings</a> and sync to have them.</>} />}
      <Card>
        <Flex gap={12} wrap align="center" justify="end" style={{ marginBottom: 16 }}>
          <Space>
            <Tooltip title="Drafts nothing asks you to look at: no strong similarity, no flags, an AI check without remarks; and drafts you saved yourself. Others can be ticked one by one."><Button icon={<CheckOutlined />} onClick={() => setSelected(list.filter(s => ready(s) && unremarkable(s) && !dirty(s)).map(s => s.id))}>Select ready</Button></Tooltip>
            {!filesOff && settings?.ai_check !== false && <Popconfirm title={`Analyze ${toAnalyze.length} work(s) again with the AI check?`} description="Works of assignments without a checklist are left out." onConfirm={reanalyze} disabled={!toAnalyze.length}>
              <Button icon={<SyncOutlined />} disabled={!toAnalyze.length} loading={busy}>{selected.length ? `Re-analyze selected (${toAnalyze.length})` : `Re-analyze all (${toAnalyze.length})`}</Button>
            </Popconfirm>}
            <Popconfirm title={`Publish ${publishable.length} grade(s) to Moodle?`} onConfirm={() => publish(publishable)} disabled={!publishable.length}>
              <Button type="primary" icon={<CloudUploadOutlined />} disabled={!publishable.length} loading={busy}>Publish selected ({publishable.length})</Button>
            </Popconfirm>
          </Space>
        </Flex>
        <Table<Submission> className="grades" rowKey="assignment_id" size="middle" loading={rows === null} dataSource={list} columns={columns} pagination={false} tableLayout="fixed" scroll={{ x: 1680 }}
          rowSelection={{ selectedRowKeys: list.filter(s => selected.includes(s.id)).map(s => s.assignment_id), getCheckboxProps: s => ({ disabled: !submitted(s) || dirty(s) }),
            onChange: (_, picked) => setSelected(picked.map(s => s.id)) }} />
      </Card>
      {modals(courseId)}
    </>
  )
}
