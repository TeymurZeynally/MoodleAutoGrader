import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, App, Breadcrumb, Button, Card, Collapse, Empty, Flex, Input, Popconfirm, Segmented, Select, Space, Statistic, Table, Tag, Tooltip, Typography } from 'antd'
import type { TableColumnsType } from 'antd'
import { CheckOutlined, CloudUploadOutlined, FileExcelOutlined, SyncOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { api, AssignmentHeader, CourseSettings, errText, Submission } from '../api'
import { usePoll } from '../App'
import { AssignmentDescription, ChecklistCard } from './AICheck'
import { gradeInMoodle } from './Attempts'
import { attention, clean, ready, submitted, unremarkable, useSheet } from './Sheet'

type Filter = 'all' | 'notgraded' | 'submitted' | 'missing' | 'attention'

const filters: { value: Filter; label: string }[] = [
  { value: 'all', label: 'All' }, { value: 'notgraded', label: 'Not graded' }, { value: 'submitted', label: 'Submitted' }, { value: 'missing', label: 'Not submitted' }, { value: 'attention', label: 'Need attention' },
]

// The chosen tab is remembered per course, in this browser.
const filterKey = (courseId: number) => `assignment-filter:${courseId}`
const savedFilter = (courseId: number) => filters.find(f => f.value === localStorage.getItem(filterKey(courseId)))?.value ?? 'all'

const groupOf = (s: Submission) => s.groups.join(', ') || 'No group'

export function AssignmentPage({ id }: { id: number }) {
  const { message } = App.useApp()
  const [all, setAll] = useState<Submission[] | null>(null), [head, setHead] = useState<AssignmentHeader | null>(null), [myGroups, setMyGroups] = useState<string[]>([])
  const [group, setGroup] = useState('mine'), [filter, setFilter] = useState<Filter>('all'), [query, setQuery] = useState('')
  const [settings, setSettings] = useState<CourseSettings | null>(null)
  const [hasChecklist, setHasChecklist] = useState(false)
  useEffect(() => { api.checklist(id).then(c => { setSettings(c.settings); setHasChecklist(!!c.checklist) }, () => {}) }, [id])

  const load = useCallback(() => {
    api.submissions(id).then(r => { setAll(r.submissions); setHead(r.assignment); setMyGroups(r.my_groups) }, e => message.error(errText(e)))
  }, [id, message])

  const max = head?.max_grade ?? null
  const { dirty, selected, setSelected, busy, setBusy, publish, penalty, columns: sheetColumns, modals } = useSheet({ load, settings, name: s => s.student, pointsTitle: `Points${max !== null ? ` (max ${max})` : ''}` })

  useEffect(load, [load])
  usePoll(load, (all ?? []).some(s => s.state === 'approved' || s.ai?.status === 'running' || s.ai?.status === 'queued'), 4000)

  const courseId = head?.course_id ?? null
  useEffect(() => { if (courseId !== null) setFilter(savedFilter(courseId)) }, [courseId])

  const pickFilter = (f: Filter) => {
    setFilter(f)
    if (courseId !== null) localStorage.setItem(filterKey(courseId), f)
  }

  const inGroup = useCallback((s: Submission) => group === 'all' || (group === 'mine' ? myGroups.length === 0 || s.groups.some(g => myGroups.includes(g)) : groupOf(s) === group), [group, myGroups])
  const scope = useMemo(() => (all ?? []).filter(inGroup), [all, inGroup])

  const shown = scope.filter(s => (filter === 'all' || (filter === 'notgraded' ? submitted(s) && gradeInMoodle(s) === null && s.state !== 'published' : filter === 'submitted' ? submitted(s) : filter === 'missing' ? !submitted(s) : attention(s)))
    && (!query || s.student.toLowerCase().includes(query.toLowerCase())))

  const groups = [...new Set((all ?? []).map(groupOf))].sort()

  const reanalyze = async (ids: number[]) => {
    setBusy(true)
    try {
      const r = await api.reassessSelected(id, ids)
      setSelected([]); message.info(r.queued ? `A check of this assignment is running; the ${ids.length} chosen work(s) are analyzed right after it` : `Analyzing ${ids.length} chosen work(s) again…`)
    } catch (e) { message.error(errText(e)) } finally { setBusy(false) }

    load()
  }

  const publishable = selected.filter(sid => all?.some(s => s.id === sid && ready(s)))
  const toFill = scope.filter(s => clean(s, settings?.require_code ?? true) && s.points === null && s.state === 'draft' && !dirty(s))
  const extrasOn = settings?.extras_enabled ?? false
  const fillMax = async () => {
    if (max === null) return
    setBusy(true)
    let failed = 0
    for (const s of toFill) await api.saveDraft(s.id, Math.max(0, max - penalty(s)), s.comment, s.version).catch(() => failed++)
    setBusy(false)
    if (failed) message.warning(`${failed} draft(s) changed meanwhile and were skipped`)
    else message.success(`Set ${max} points for ${toFill.length} clean works. Review and publish them.`)

    load()
  }

  const columns: TableColumnsType<Submission> = [
    {
      title: 'Student', width: 200, render: (_, s) => <Flex vertical>
        <Tooltip title={`${s.student}: every assignment of this student`}><Typography.Link strong href={`#/course/${head?.course_id}/student/${s.student_id}`}>{s.short_name || s.student}</Typography.Link></Tooltip>
        {!s.enrolled && <Tooltip title="No longer enrolled in the course (e.g. a graduated group); kept for plagiarism checks"><Tag style={{ width: 'fit-content' }}>Left the course</Tag></Tooltip>}
      </Flex>,
    },
    ...sheetColumns,
  ]

  const count = (f: (s: Submission) => boolean) => scope.filter(f).length
  const sections = groups.filter(g => shown.some(s => groupOf(s) === g))
  return (
    <>
      <Breadcrumb style={{ marginBottom: 16 }} items={[{ title: <a href="#/courses">Courses</a> }, { title: head ? <a href={`#/course/${head.course_id}`}>{head.course_name}</a> : '…' }, { title: head?.name ?? '…' }]} />
      <AssignmentDescription assignmentId={id} />
      <Card style={{ marginBottom: 16 }}>
        <Typography.Title level={4} style={{ marginTop: 0 }}>{head?.name}</Typography.Title>
        <Flex gap={40} wrap>
          <Statistic title="Students" value={scope.length} />
          <Statistic title="Submitted" value={count(submitted)} />
          <Statistic title="Not submitted" value={count(s => !submitted(s))} />
          <Statistic title="Need attention" value={count(attention)} styles={{ content: { color: count(attention) ? '#cf1322' : undefined } }} />
          <Statistic title="Graded in Moodle" value={count(s => gradeInMoodle(s) !== null)} />
          <Statistic title="Drafts ready to publish" value={count(ready)} />
          {extrasOn && <Statistic title="★ Extra task done" value={count(s => !!s.ai?.extra)} styles={{ content: { color: count(s => !!s.ai?.extra) ? '#d48806' : undefined } }} />}
        </Flex>
      </Card>
      <ChecklistCard assignmentId={id} onChanged={load} queued={(all ?? []).filter(s => s.ai?.status === 'queued' || s.ai?.status === 'running').length}
        failed={(all ?? []).filter(s => s.ai?.status === 'failed').length} courseId={head?.course_id ?? null} />
      {settings?.download_files === false && <Alert type="info" showIcon style={{ marginBottom: 16 }} title="Files of this course are not downloaded"
        description={<>The sheet shows the grades, comments and attempts from Moodle. Files, the similarity check and the AI check are off: turn on “Download files” in the <a href={`#/course/${head?.course_id}/settings`}>course settings</a> and sync to have them.</>} />}
      <Card>
        <Flex gap={12} wrap align="center" style={{ marginBottom: 16 }}>
          <Select value={group} onChange={setGroup} style={{ minWidth: 240 }} options={[
            { value: 'mine', label: `My groups${myGroups.length ? ` (${myGroups.join(', ')})` : ''}` }, { value: 'all', label: 'All groups' }, ...groups.map(g => ({ value: g, label: g })),
          ]} />
          <Segmented<Filter> value={filter} onChange={pickFilter} options={filters} />
          <Input.Search placeholder="Find a student" allowClear value={query} onChange={e => setQuery(e.target.value)} style={{ width: 220 }} />
          <span style={{ flex: 1 }} />
          <Space>
            <Tooltip title="The submitted works as an Excel file for the teacher of the course: originality, a column per criterion, the drafts and comments, the reasons of the AI check, a link to each work in Moodle"><Button icon={<FileExcelOutlined />} href={`/api/assignments/${id}/export`} download>Export</Button></Tooltip>
            {max !== null && settings?.download_files !== false && <Popconfirm title={`Set ${max} points for ${toFill.length} works?`} description="Only works that are original, have code and no flags, and have no points yet (no AI check result)." onConfirm={fillMax} disabled={!toFill.length}>
              <Tooltip title="Without the AI review nobody checked these works against the assignment: this gives them full points as drafts for you to review">
                <Button icon={<ThunderboltOutlined />} disabled={!toFill.length} loading={busy}>Full points for clean works ({toFill.length})</Button>
              </Tooltip>
            </Popconfirm>}
            <Tooltip title="Drafts nothing asks you to look at: no strong similarity, no flags, an AI check without remarks; and drafts you saved yourself. Others can be ticked one by one."><Button icon={<CheckOutlined />} onClick={() => setSelected(scope.filter(s => ready(s) && unremarkable(s) && !dirty(s)).map(s => s.id))}>Select ready</Button></Tooltip>
            {selected.length > 0 && hasChecklist && settings?.ai_check !== false && head?.ai_check !== false && <Popconfirm title={`Analyze ${selected.length} chosen work(s) again with the AI check?`} onConfirm={() => reanalyze(selected)}>
              <Button icon={<SyncOutlined />} loading={busy}>Re-analyze selected ({selected.length})</Button>
            </Popconfirm>}
            <Popconfirm title={`Publish ${publishable.length} grade(s) to Moodle?`} onConfirm={() => publish(publishable)} disabled={!publishable.length}>
              <Button type="primary" icon={<CloudUploadOutlined />} disabled={!publishable.length} loading={busy}>Publish selected ({publishable.length})</Button>
            </Popconfirm>
          </Space>
        </Flex>
        {all !== null && !all.some(submitted) && <Alert type="info" showIcon title="No submitted works yet" description="Run a sync on the Courses page to load submissions from Moodle." style={{ marginBottom: 16 }} />}
        {all === null ? <Table loading columns={columns} dataSource={[]} /> : sections.length === 0 ? <Empty description="No students match the filter" /> : (
          <Collapse defaultActiveKey={groups} items={sections.map(g => {
            const rows = shown.filter(s => groupOf(s) === g)
            return {
              key: g,
              label: <Flex gap={12} align="center"><Typography.Text strong>{g}</Typography.Text>
                <Typography.Text type="secondary">{rows.length} students · {rows.filter(submitted).length} submitted · {rows.filter(s => gradeInMoodle(s) !== null).length} graded in Moodle</Typography.Text>
                {rows.some(attention) && <Tag color="red">{rows.filter(attention).length} need attention</Tag>}</Flex>,
              children: <Table<Submission> className="grades" rowKey="student_id" size="middle" dataSource={rows} columns={columns} pagination={false} tableLayout="fixed" scroll={{ x: 1650 }}
                rowSelection={{ selectedRowKeys: rows.filter(s => selected.includes(s.id)).map(s => s.student_id), getCheckboxProps: s => ({ disabled: !submitted(s) || dirty(s) }),
                  onChange: (_, picked) => setSelected(prev => [...prev.filter(x => !rows.some(r => r.id === x)), ...picked.map(s => s.id)]) }} />,
            }
          })} />
        )}
      </Card>
      {modals(head?.course_id ?? null)}
    </>
  )
}
