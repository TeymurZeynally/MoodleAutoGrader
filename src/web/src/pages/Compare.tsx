import { useEffect, useRef, useState } from 'react'
import { Alert, Col, Empty, Flex, Modal, Row, Spin, Statistic, Switch, Tabs, Tag, Tree, Typography } from 'antd'
import type { TreeDataNode } from 'antd'
import { api, Diff, errText, FileView, Match, pct } from '../api'

// Side-by-side view of two works. Highlighted lines are exactly the shingles behind the similarity numbers;
// grey lines match code that most of the course shares (a template) and do not count.
export function CompareModal({ id, student, match, onClose }: { id: number; student: string; match: Match | null; onClose: () => void }) {
  const [data, setData] = useState<Diff | null>(null), [method, setMethod] = useState(''), [error, setError] = useState(''), [pair, setPair] = useState('0'), [sync, setSync] = useState(true)
  useEffect(() => {
    if (!match) return
    setData(null); setError(''); setPair('0')
    api.compare(id, match.other_id).then(r => { setData(r.diff); setMethod(r.method) }, e => setError(errText(e)))
  }, [id, match])

  if (!match) return null
  const p = data?.pairs[Number(pair)]
  const code = data && (data.pairs.length === 0 ? <Empty description={`No source files in ${student}'s work`} /> : (
    <Row gutter={16} wrap={false}>
      <Col flex="320px">
        <Typography.Text type="secondary">Files of <b>{student}</b>, and the most similar file of <b>{match.student}</b></Typography.Text>
        <div className="file-tree">
          <Tree showLine blockNode defaultExpandAll selectedKeys={[pair]} treeData={fileTree(data)} key={data.pairs.map(x => x.a.path).join('|')}
            onSelect={keys => { const k = String(keys[0] ?? ''); if (!k.startsWith('dir:')) setPair(k) }} />
        </div>
        {data.only_b?.length ? <Typography.Paragraph type="secondary" style={{ marginTop: 12, wordBreak: 'break-all' }}>Only in {match.student}'s work: {data.only_b.join(', ')}</Typography.Paragraph> : null}
      </Col>
      <Col flex="auto" style={{ minWidth: 0 }}>
        {p && <SideBySide a={p.a} b={p.b} nameA={student} nameB={match.student} sync={sync} />}
      </Col>
    </Row>
  ))

  const report = data && <SideBySide a={data.report_a} b={data.report_b} nameA={student} nameB={match.student} sync={sync} />
  return (
    <Modal open title={`${student}  ↔  ${match.student}`} onCancel={onClose} footer={null} width="96vw" style={{ top: 16 }} styles={{ body: { minHeight: '80vh' } }} destroyOnHidden>
      <Flex gap={32} style={{ marginBottom: 12 }} wrap align="end">
        <Statistic title="Code similarity" value={pct(match.code_similarity)} suffix="%" />
        <Statistic title="Report similarity" value={pct(match.report_similarity)} suffix="%" />
        <Statistic title="Identical files" value={match.identical_files} />
        <Flex gap={16} wrap>
          <span><span className="legend-match" />matches the other work</span>
          {method !== 'dolos' && <span><span className="legend-common" />shared by most of the course (not counted)</span>}
          {method && <Tag color={method === 'dolos' ? 'blue' : 'default'}>{method === 'dolos' ? 'Code compared by Dolos (syntax tree)' : 'Code compared by built-in fingerprints'}</Tag>}
          <span><Switch size="small" checked={sync} onChange={setSync} /> scroll both sides together</span>
        </Flex>
      </Flex>
      {error && <Alert type="error" showIcon title={error} />}
      {!data && !error ? <Flex justify="center" style={{ padding: 48 }}><Spin /></Flex> : data && (
        <Tabs items={[{ key: 'code', label: 'Code', children: code }, { key: 'report', label: 'Report', children: report }]} />
      )}
    </Modal>
  )
}

// fileTree turns the paths of work A into folders; each file shows its matched lines and the paired file of work B.
function fileTree(d: Diff): TreeDataNode[] {
  const root: TreeDataNode[] = []
  d.pairs.forEach((x, i) => {
    const parts = x.a.path.split('/').filter(Boolean)
    let level = root, dir = ''
    for (const part of parts.slice(0, -1)) {
      dir += part + '/'
      let node = level.find(n => n.key === `dir:${dir}`)
      if (!node) { node = { key: `dir:${dir}`, title: part, selectable: false, children: [] }; level.push(node) }
      level = node.children!
    }

    level.push({
      key: String(i), isLeaf: true,
      title: <div title={x.b ? `Most similar file of the other work: ${x.b.path}` : 'No similar file in the other work'}>
        <Flex justify="space-between" gap={8}>
          <span>{parts[parts.length - 1]}</span>
          <Tag color={x.a.matched ? 'red' : 'default'} style={{ marginInlineEnd: 0, flex: 'none' }}>{x.a.matched}</Tag>
        </Flex>
        {x.b && <Typography.Text type="secondary" style={{ fontSize: 11 }}>↔ {x.b.path}</Typography.Text>}
      </div>,
    })
  })

  return root
}

// SideBySide scrolls both panes together. With a line map (A line -> B line) the other pane is aligned on the matching
// line, so copied blocks stay side by side even when one file has extra code; elsewhere it scrolls proportionally.
function SideBySide({ a, b, nameA, nameB, sync }: { a: FileView; b: FileView | null; nameA: string; nameB: string; sync: boolean }) {
  const left = useRef<HTMLDivElement>(null), right = useRef<HTMLDivElement>(null), driver = useRef<'a' | 'b' | null>(null)

  const map = a.map ?? []
  const back: number[] = []
  map.forEach((j, i) => { if (j >= 0 && back[j] === undefined) back[j] = i })
  useEffect(() => { left.current?.scrollTo(0, 0); right.current?.scrollTo(0, 0) }, [a, b])

  const onScroll = (side: 'a' | 'b') => {
    if (!sync || !b) return
    if (driver.current && driver.current !== side) return // this event was caused by our own scrollTo
    driver.current = side
    const from = side === 'a' ? left.current : right.current, to = side === 'a' ? right.current : left.current
    if (from && to) {
      const rows = from.children, top = from.scrollTop
      let line = 0
      while (line < rows.length - 1 && (rows[line + 1] as HTMLElement).offsetTop <= top) line++
      const lookup = side === 'a' ? (i: number) => map[i] : (i: number) => back[i]
      let target = -1
      for (let d = 0; d < 40 && target < 0; d++) { // the nearest mapped line at or below the top
        const t = lookup(line + d)
        if (t !== undefined && t >= 0) target = t - d
      }
      const toRows = to.children
      if (target >= 0 && toRows.length) {
        const row = toRows[Math.max(0, Math.min(target, toRows.length - 1))] as HTMLElement
        const offset = top - (rows[line] as HTMLElement).offsetTop
        to.scrollTop = row.offsetTop + offset
      } else {
        to.scrollTop = (top / Math.max(1, from.scrollHeight - from.clientHeight)) * (to.scrollHeight - to.clientHeight)
      }
    }

    requestAnimationFrame(() => { driver.current = null })
  }

  return (
    <Row gutter={12} wrap={false}>
      <Col span={12} style={{ minWidth: 0 }}><Source title={nameA} file={a} box={left} onScroll={() => onScroll('a')} /></Col>
      <Col span={12} style={{ minWidth: 0 }}>{b ? <Source title={nameB} file={b} box={right} onScroll={() => onScroll('b')} /> : <Empty description={`No similar file in ${nameB}'s work`} />}</Col>
    </Row>
  )
}

function Source({ title, file, box, onScroll }: { title: string; file: FileView; box: React.RefObject<HTMLDivElement | null>; onScroll: () => void }) {
  const empty = file.lines.length === 0 || (file.lines.length === 1 && !file.lines[0].text)
  return (
    <>
      <Flex vertical style={{ marginBottom: 6 }}>
        <Typography.Text strong>{title}</Typography.Text>
        <Typography.Text type="secondary" style={{ fontSize: 12, wordBreak: 'break-all' }}>{file.path} · {file.matched} of {file.lines.length} lines match</Typography.Text>
      </Flex>
      <div className="code-view" ref={box} onScroll={onScroll}>
        {empty ? <Empty style={{ padding: 24 }} description="Empty" /> :
        file.lines.map((l, i) => <div key={i} className={`code-line ${l.mark ?? ''}`}><span className="ln">{i + 1}</span><span className="tx">{l.text || ' '}</span></div>)}
      </div>
    </>
  )
}
