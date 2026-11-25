import { ReactNode, useEffect, useRef, useState } from 'react'
import { Alert, Flex, Spin, Typography } from 'antd'
// The legacy build polyfills recent JavaScript features, so older browsers render PDFs too.
import { getDocument, GlobalWorkerOptions } from 'pdfjs-dist/legacy/build/pdf.mjs'
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url'
import { errText, fetchRaw } from '../api'

GlobalWorkerOptions.workerSrc = workerUrl

// PdfView renders every page to a canvas with pdf.js, so screenshots in reports are visible. The page CSP forbids
// eval, and PDF scripts, forms and links are not run. When the pages cannot be had, fallback is shown instead, if any.
export default function PdfView({ url, fallback }: { url: string; fallback?: ReactNode }) {
  const box = useRef<HTMLDivElement>(null), [error, setError] = useState(''), [pages, setPages] = useState<number | null>(null)
  useEffect(() => {
    let cancelled = false
    const el = box.current
    if (!el) return
    el.replaceChildren(); setError(''); setPages(null)
    const run = async () => {
      const data = new Uint8Array(await (await fetchRaw(url)).arrayBuffer())
      const task = getDocument({ data, enableXfa: false, stopAtErrors: false })
      const doc = await task.promise
      if (cancelled) return
      setPages(doc.numPages)
      const width = el.clientWidth - 24
      for (let i = 1; i <= Math.min(doc.numPages, 200) && !cancelled; i++) {
        const page = await doc.getPage(i)
        const base = page.getViewport({ scale: 1 })
        const viewport = page.getViewport({ scale: Math.min(2, width / base.width) * devicePixelRatio })
        const canvas = document.createElement('canvas')
        canvas.width = viewport.width; canvas.height = viewport.height
        canvas.style.width = `${viewport.width / devicePixelRatio}px`
        canvas.style.boxShadow = '0 1px 4px rgba(0,0,0,.15)'
        el.appendChild(canvas)
        await page.render({ canvas, viewport }).promise
      }

      void task.destroy()
    }

    run().catch(e => !cancelled && setError(errText(e)))
    return () => { cancelled = true }
  }, [url])

  if (error && fallback) return <><Alert type="info" showIcon style={{ marginBottom: 8 }} title="The document could not be shown as pages; its text is shown instead" description={error} />{fallback}</>
  return (
    <>
      {error && <Alert type="error" showIcon title={`Cannot render the PDF: ${error}`} />}
      {pages === null && !error && <Flex justify="center" style={{ padding: 24 }}><Spin /></Flex>}
      {pages !== null && <Typography.Text type="secondary">{pages} pages</Typography.Text>}
      <div ref={box} style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12, maxHeight: '75vh', overflow: 'auto', background: '#f0f2f5', padding: 12, borderRadius: 8 }} />
    </>
  )
}
