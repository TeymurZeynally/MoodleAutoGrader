import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { Alert, Button, Checkbox, Col, Empty, Flex, Modal, Row, Spin, Tooltip, Tree, Typography } from 'antd'
import type { TreeDataNode } from 'antd'
import { DownloadOutlined, EyeInvisibleOutlined, FileImageOutlined, FileOutlined, FilePdfOutlined, FileTextOutlined, FileWordOutlined, FileZipOutlined, FolderOutlined } from '@ant-design/icons'
import { api, Entry, errText, fetchRaw, rawUrl, size, UploadedFile } from '../api'

const PdfView = lazy(() => import('./Pdf'))

// A node is a file in the upload: the uploaded file itself (inner = []) or an entry reached through archives.
interface Node { file: number; inner: string[]; name: string; kind: string; size: number; note?: string }
const keyOf = (n: Node) => JSON.stringify([n.file, n.inner])

// Build output, dependencies and binaries: hidden by default, they only make the tree long.
const binaryExt = /\.(dll|exe|pdb|so|dylib|a|lib|o|obj|class|jar|nupkg|snupkg|cache|bin|dat|db|sqlite|mdf|ldf|pyc|wasm|map|ico|ttf|woff2?|eot)$/i
const junk = (n: Node) => !!n.note || n.kind === 'exe' || binaryExt.test(n.name)

const icon = (kind: string) => ({ pdf: <FilePdfOutlined />, docx: <FileWordOutlined />, image: <FileImageOutlined />, archive: <FileZipOutlined />, code: <FileTextOutlined />, text: <FileTextOutlined /> }[kind] ?? <FileOutlined />)

// FilesModal shows everything a student uploaded: PDFs with their screenshots, archives as a folder tree (listed from
// headers, unpacked one file at a time on the server), code with line numbers, images.
export function FilesModal({ id, student, open, onClose }: { id: number; student: string; open: boolean; onClose: () => void }) {
  const [files, setFiles] = useState<UploadedFile[] | null>(null), [error, setError] = useState('')
  const [listed, setListed] = useState<Record<string, Entry[] | string>>({}), [selected, setSelected] = useState<Node | null>(null), [hideJunk, setHideJunk] = useState(true)
  // Folders and archives open by themselves when they appear; what the teacher collapses stays collapsed.
  const [expanded, setExpanded] = useState<string[]>([]), seen = useRef(new Set<string>())
  useEffect(() => {
    if (!open) return
    setFiles(null); setError(''); setListed({}); setSelected(null); setExpanded([]); seen.current = new Set()
    api.files(id).then(fs => {
      setFiles(fs)
      const first = fs.find(f => f.kind === 'pdf') ?? fs[0]
      if (first) setSelected({ file: first.id, inner: [], name: first.name, kind: first.kind, size: first.size })
    }, e => setError(errText(e)))
  }, [id, open])

  const list = (n: Node) => {
    const k = keyOf(n)
    if (listed[k]) return
    setListed(l => ({ ...l, [k]: [] }))
    api.entries(id, n.file, n.inner).then(es => setListed(l => ({ ...l, [k]: es })), e => setListed(l => ({ ...l, [k]: errText(e) })))
  }

  useEffect(() => { files?.filter(f => f.kind === 'archive').forEach(f => list({ file: f.id, inner: [], name: f.name, kind: 'archive', size: f.size })) }, [files]) // eslint-disable-line react-hooks/exhaustive-deps -- list changes on every render; this runs once per file list

  const { tree, nodes } = useMemo(() => {
    const nodes = new Map<string, Node>()
    const leaf = (n: Node, title: string): TreeDataNode => {
      nodes.set(keyOf(n), n)
      const entries = n.kind === 'archive' ? listed[keyOf(n)] : undefined
      return {
        key: keyOf(n), icon: icon(n.kind), isLeaf: n.kind !== 'archive',
        title: <Flex gap={8} justify="space-between"><span>{title}</span>
          <Typography.Text type="secondary" style={{ fontSize: 12, flex: 'none', whiteSpace: 'nowrap' }}>
            {n.note && <Tooltip title={n.note}><EyeInvisibleOutlined style={{ marginRight: 6, color: '#fa8c16' }} /></Tooltip>}{size(n.size)}
          </Typography.Text></Flex>,
        children: entries === undefined ? undefined : typeof entries === 'string' ? [{ key: keyOf(n) + 'err', title: <Typography.Text type="danger">{entries}</Typography.Text>, isLeaf: true, selectable: false }]
        : folders(entries.map(e => ({ file: n.file, inner: [...n.inner, e.path], name: e.path, kind: e.kind, size: e.size, note: e.note })).filter(x => !hideJunk || !junk(x)), leaf, keyOf(n)),
      }
    }

    return { tree: (files ?? []).map(f => leaf({ file: f.id, inner: [], name: f.name, kind: f.kind, size: f.size }, f.name)), nodes }
  }, [files, listed, hideJunk])

  const hidden = Object.values(listed).reduce((n, es) => n + (Array.isArray(es) ? es.filter(e => junk({ file: 0, inner: [], name: e.path, kind: e.kind, size: e.size, note: e.note })).length : 0), 0)
  useEffect(() => {
    const fresh: string[] = []
    const walk = (ns: TreeDataNode[]) => ns.forEach(n => { if (n.children) { const k = String(n.key); if (!seen.current.has(k) && (k.includes('dir:') || n.children.length)) { seen.current.add(k); fresh.push(k) } walk(n.children) } })
    walk(tree)
    if (fresh.length) setExpanded(e => [...e, ...fresh])
  }, [tree])

  return (
    <Modal open={open} title={`Files of ${student}`} onCancel={onClose} footer={null} width="96vw" style={{ top: 16 }} styles={{ body: { minHeight: '80vh' } }} destroyOnHidden>
      {error && <Alert type="error" showIcon title={error} />}
      {!files && !error ? <Flex justify="center" style={{ padding: 48 }}><Spin /></Flex> : files && (files.length === 0 ? <Empty description="Nothing uploaded" /> : (
        <Row gutter={16} wrap={false}>
          <Col flex="380px">
            <Checkbox checked={hideJunk} onChange={e => setHideJunk(e.target.checked)}>Hide build output and binaries{hidden ? ` (${hidden})` : ''}</Checkbox>
            <div className="file-tree" style={{ maxHeight: '74vh' }}>
              <Tree showIcon blockNode treeData={tree} selectedKeys={selected ? [keyOf(selected)] : []} expandedKeys={expanded}
                onExpand={(keys, info) => { setExpanded(keys.map(String)); const n = nodes.get(String(info.node.key)); if (n?.kind === 'archive') list(n) }}
                onSelect={(keys) => { const n = nodes.get(String(keys[0])); if (n) { setSelected(n); if (n.kind === 'archive') list(n) } }} />
            </div>
          </Col>
          <Col flex="auto" style={{ minWidth: 0 }}>{selected && <Viewer id={id} node={selected} />}</Col>
        </Row>
      ))}
    </Modal>
  )
}

// folders groups archive entries by directory.
function folders(items: Node[], leaf: (n: Node, title: string) => TreeDataNode, prefix: string): TreeDataNode[] {
  const root: TreeDataNode[] = []
  for (const n of items) {
    const parts = n.name.split('/').filter(Boolean)
    let level = root, dir = ''
    for (const part of parts.slice(0, -1)) {
      dir += part + '/'
      let node = level.find(x => x.key === `${prefix}dir:${dir}`)
      if (!node) { node = { key: `${prefix}dir:${dir}`, title: part, icon: <FolderOutlined />, selectable: false, children: [] }; level.push(node) }
      level = node.children!
    }
    level.push(leaf(n, parts[parts.length - 1] ?? n.name))
  }

  return root
}

function Viewer({ id, node }: { id: number; node: Node }) {
  const url = rawUrl(id, node.file, node.inner)
  const header = (
    <Flex justify="space-between" align="center" style={{ marginBottom: 8 }} gap={8}>
      <Typography.Text strong style={{ wordBreak: 'break-all' }}>{[...(node.inner.length ? [] : [node.name]), ...node.inner].join(' › ')}</Typography.Text>
      {node.kind !== 'archive' && <Button size="small" icon={<DownloadOutlined />} href={url} download>Download</Button>}
    </Flex>
  )

  if (node.note) return <>{header}<Alert type="warning" showIcon title={node.note} /></>
  switch (node.kind) {
    case 'pdf': return <>{header}<Suspense fallback={<Spin />}><PdfView url={url} /></Suspense></>
    case 'image': return <>{header}<div className="code-view" style={{ maxHeight: '75vh', padding: 8, textAlign: 'center' }}><img src={url} alt={node.name} style={{ maxWidth: '100%' }} /></div></>
    case 'archive': return <>{header}<Empty description="An archive: expand it in the tree on the left" /></>
    case 'exe': return <>{header}<Alert type="error" showIcon title="Executable file: not shown" description="Download it only if you know what you are doing; do not run it." /></>
    case 'docx': return <>{header}<Suspense fallback={<Spin />}><PdfView url={rawUrl(id, node.file, node.inner, 'pdf')} fallback={<TextView url={rawUrl(id, node.file, node.inner, 'text')} />} /></Suspense></>
    default: return <>{header}<TextView url={url} /></>
  }
}

function TextView({ url }: { url: string }) {
  const [text, setText] = useState<string | null>(null), [error, setError] = useState(''), [binary, setBinary] = useState(false)
  useEffect(() => {
    setText(null); setError(''); setBinary(false)
    fetchRaw(url).then(async r => { if (r.headers.get('Content-Type')?.startsWith('text/')) setText(await r.text()); else setBinary(true) }, e => setError(errText(e)))
  }, [url])

  if (error) return <Alert type="error" showIcon title={error} />
  if (binary) return <Empty description="Binary file: use Download to inspect it" />
  if (text === null) return <Spin />
  const lines = text.split('\n')
  return <div className="code-view" style={{ maxHeight: '75vh' }}>{lines.map((l, i) => <div key={i} className="code-line"><span className="ln">{i + 1}</span><span className="tx">{l || ' '}</span></div>)}</div>
}
