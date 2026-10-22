import { t as tr } from '../i18n'
import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { CheckCircle2, ChevronDown, ChevronUp, Loader2, X, XCircle, AlertTriangle, Info } from 'lucide-react'
import { setTaskTracker, stream } from '../lib/api'
import { taskTitle } from '../lib/tasks'
import { bytes } from '../lib/format'

// ---------- tiny global bus ----------
type Listener = () => void
function bus<T>(initial: T) {
  let value = initial
  const ls = new Set<Listener>()
  return {
    get: () => value,
    set: (v: T) => {
      value = v
      ls.forEach((l) => l())
    },
    use(): T {
      const [, f] = useState(0)
      useEffect(() => {
        const l = () => f((n) => n + 1)
        ls.add(l)
        return () => {
          ls.delete(l)
        }
      }, [])
      return value
    },
  }
}

// ---------- toasts ----------
interface ToastT {
  id: number
  kind: 'ok' | 'err' | 'info'
  text: string
}
const toasts = bus<ToastT[]>([])
let tid = 0
function push(kind: ToastT['kind'], text: string, ms = 4500) {
  const id = ++tid
  toasts.set([...toasts.get(), { id, kind, text }])
  setTimeout(() => toasts.set(toasts.get().filter((t) => t.id !== id)), ms)
}
export const toast = {
  ok: (t: string) => push('ok', t),
  err: (t: unknown) => push('err', t instanceof Error ? t.message : String(t), 8000),
  info: (t: string) => push('info', t),
}

function Toaster() {
  const list = toasts.use()
  return (
    <div className="toasts">
      {list.map((t) => (
        <div key={t.id} className={`toast toast-${t.kind}`}>
          {t.kind === 'ok' ? <CheckCircle2 size={16} /> : t.kind === 'err' ? <XCircle size={16} /> : <Info size={16} />}
          <span>{t.text}</span>
          <button className="icon-btn" onClick={() => toasts.set(toasts.get().filter((x) => x.id !== t.id))}>
            <X size={14} />
          </button>
        </div>
      ))}
    </div>
  )
}

// ---------- modal / dialogs ----------
export function Modal({
  title,
  children,
  onClose,
  footer,
  wide,
  icon,
}: {
  title: ReactNode
  children: ReactNode
  onClose: () => void
  footer?: ReactNode
  wide?: boolean | 'xl'
  icon?: ReactNode
}) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (e.key === 'Escape') return onClose()
      if (e.key !== 'Enter' || e.isComposing || e.altKey || e.shiftKey) return
      const modal = box.current
      if (!modal) return
      // only the topmost of the open modals answers
      const open = document.querySelectorAll('.modal')
      if (open[open.length - 1] !== modal) return
      const t = e.target as HTMLElement
      if (t !== document.body && !modal.contains(t)) return
      // buttons, links and menus keep their own Enter; in a text area Enter is a new line (Ctrl/Cmd+Enter confirms)
      const inText = !!t.closest?.('textarea, [contenteditable="true"]')
      if (t.closest?.('button, a, select, .xterm')) return
      if (inText !== (e.ctrlKey || e.metaKey)) return
      const ok = modal.querySelector<HTMLButtonElement>('.modal-foot .btn-primary:not(:disabled)')
      if (!ok) return
      e.preventDefault() // also stops the form's own implicit submit: the button does it once
      ok.click()
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [onClose])
  return createPortal(
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} className={`modal${wide === 'xl' ? ' modal-xl' : wide ? ' modal-wide' : ''}`} role="dialog">
        <div className="modal-head">
          {icon}
          <h3>{title}</h3>
          <button className="icon-btn" onClick={onClose} aria-label={tr('Close')}>
            <X size={16} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}

type DialogFn = (close: () => void) => ReactNode
const dialogs = bus<{ id: number; render: DialogFn }[]>([])
let did = 0

/** Mount an arbitrary modal imperatively. */
export function openDialog(render: DialogFn) {
  const id = ++did
  const close = () => dialogs.set(dialogs.get().filter((d) => d.id !== id))
  dialogs.set([...dialogs.get(), { id, render: () => render(close) }])
  return close
}

export function confirm(opts: { title: string; body?: ReactNode; confirm?: string; danger?: boolean; check?: string }): Promise<false | { checked: boolean }> {
  return new Promise((resolve) => {
    openDialog((close) => <ConfirmDialog {...opts} onDone={(v) => (close(), resolve(v))} />)
  })
}

function ConfirmDialog(props: { title: string; body?: ReactNode; confirm?: string; danger?: boolean; check?: string; onDone: (v: false | { checked: boolean }) => void }) {
  const [checked, setChecked] = useState(false)
  return (
    <Modal
      title={props.title}
      icon={props.danger ? <AlertTriangle size={18} className="tone-bad" /> : undefined}
      onClose={() => props.onDone(false)}
      footer={
        <>
          <button className="btn" onClick={() => props.onDone(false)}>
            {tr('Cancel')}
          </button>
          <button autoFocus className={`btn ${props.danger ? 'btn-danger' : 'btn-primary'}`} onClick={() => props.onDone({ checked })}>
            {props.confirm || tr('Confirm')}
          </button>
        </>
      }
    >
      {props.body && <div className="muted">{props.body}</div>}
      {props.check && (
        <label className="check" style={{ marginTop: 12 }}>
          <input type="checkbox" checked={checked} onChange={(e) => setChecked(e.target.checked)} /> {props.check}
        </label>
      )}
    </Modal>
  )
}

export function prompt(opts: { title: string; label?: string; value?: string; placeholder?: string; confirm?: string; help?: ReactNode }): Promise<string | null> {
  return new Promise((resolve) => {
    openDialog((close) => <PromptDialog {...opts} onDone={(v) => (close(), resolve(v))} />)
  })
}

function PromptDialog(props: { title: string; label?: string; value?: string; placeholder?: string; confirm?: string; help?: ReactNode; onDone: (v: string | null) => void }) {
  const [v, setV] = useState(props.value ?? '')
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    ref.current?.focus()
    ref.current?.select()
  }, [])
  return (
    <Modal
      title={props.title}
      onClose={() => props.onDone(null)}
      footer={
        <>
          <button className="btn" onClick={() => props.onDone(null)}>
            {tr('Cancel')}
          </button>
          <button className="btn btn-primary" disabled={!v.trim()} onClick={() => props.onDone(v.trim())}>
            {props.confirm || tr('OK')}
          </button>
        </>
      }
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (v.trim()) props.onDone(v.trim())
        }}
      >
        <label className="field">
          {props.label && <span>{props.label}</span>}
          <input ref={ref} value={v} placeholder={props.placeholder} onChange={(e) => setV(e.target.value)} />
        </label>
        {props.help && <p className="help">{props.help}</p>}
      </form>
    </Modal>
  )
}

function DialogHost() {
  const list = dialogs.use()
  return (
    <>
      {list.map((d) => (
        <div key={d.id}>{d.render(() => {})}</div>
      ))}
    </>
  )
}

// ---------- background jobs (pull, push, deploy, compose…) ----------
interface Layer {
  status: string
  current?: number
  total?: number
}
export interface Job {
  id: number
  title: string
  state: 'running' | 'done' | 'error'
  lines: string[]
  layers: Record<string, Layer>
  error?: string
  // one-line result shown next to the title (e.g. space reclaimed)
  summary?: string
  open: boolean
  started: number
}
const jobs = bus<Job[]>([])
let jid = 0

function updateJob(id: number, fn: (j: Job) => Job) {
  jobs.set(jobs.get().map((j) => (j.id === id ? fn(j) : j)))
}

/**
 * Runs a streaming API call as a tracked background job. Resolves with the
 * last event carrying an "id" (e.g. created container), rejects on error.
 */
export function runJob(title: string, method: string, path: string, body?: unknown, opts: { quiet?: boolean } = {}): Promise<any> {
  const id = ++jid
  jobs.set([{ id, title, state: 'running' as const, lines: [], layers: {}, open: !opts.quiet, started: Date.now() }, ...jobs.get()].slice(0, 30))
  let result: any = null
  let pending: string[] = []
  let layers: Record<string, Layer> = {}
  let raf = 0
  const flush = () => {
    raf = 0
    const add = pending
    pending = []
    const ls = layers
    updateJob(id, (j) => ({ ...j, lines: [...j.lines, ...add].slice(-800), layers: ls }))
  }
  return stream(method, path, body, (ev) => {
    if (ev.id && ev.log) result = ev
    if (ev.log) pending.push(ev.log)
    else if (ev.status && ev.id && (ev.progressDetail || /Pull|Download|Extract|Wait|Already|Verifying|Pushed|Pushing|Preparing|Layer/.test(ev.status))) {
      layers = { ...layers, [ev.id]: { status: ev.status, current: ev.progressDetail?.current, total: ev.progressDetail?.total } }
    } else if (ev.status) pending.push(ev.id ? `${ev.id}: ${ev.status}` : ev.status)
    else if (ev.stream) pending.push(...String(ev.stream).replace(/\n$/, '').split('\n'))
    else if (ev.aux?.ID) pending.push('ID: ' + ev.aux.ID)
    if (!raf) raf = requestAnimationFrame(flush)
  })
    .then(() => {
      flush()
      updateJob(id, (j) => ({ ...j, state: 'done' }))
      toast.ok(tr('{0}: done', title))
      return result
    })
    .catch((err) => {
      flush()
      updateJob(id, (j) => ({ ...j, state: 'error', error: err.message, open: true }))
      throw err
    })
}

/** Shows a plain (non-streaming) request in the tray while it runs. */
export function trackTask<T>(title: string, p: Promise<T>): Promise<T> {
  const id = ++jid
  jobs.set([{ id, title, state: 'running' as const, lines: [], layers: {}, open: false, started: Date.now() }, ...jobs.get()].slice(0, 30))
  p.then(
    (r: any) => {
      // prune results: { SpaceReclaimed } or one per kind
      const freed = r && typeof r === 'object' ? [r, ...Object.values(r)].reduce((a: number, x: any) => a + (x && typeof x === 'object' ? x.SpaceReclaimed || 0 : 0), 0) : 0
      updateJob(id, (j) => ({ ...j, state: 'done', summary: freed ? tr('{0} reclaimed', bytes(freed)) : undefined }))
    },
    (err) => updateJob(id, (j) => ({ ...j, state: 'error', error: err?.message || String(err) })),
  )
  return p
}
setTaskTracker((method, path, p, title) => void trackTask(title || taskTitle(method, path), p))

function JobsTray() {
  const list = jobs.use()
  const [collapsed, setCollapsed] = useState(false)
  const endRefs = useRef<Record<number, HTMLDivElement | null>>({})
  const visible = list.filter((j) => j.state === 'running' || Date.now() - j.started < 10 * 60e3 || j.open)
  useEffect(() => {
    visible.forEach((j) => j.open && endRefs.current[j.id]?.scrollIntoView({ block: 'nearest' }))
  })
  if (!visible.length) return null
  const running = visible.filter((j) => j.state === 'running').length
  return (
    <div className="jobs">
      <div className="jobs-head" onClick={() => setCollapsed(!collapsed)}>
        {running ? <Loader2 size={14} className="spin" /> : <CheckCircle2 size={14} className="tone-ok" />}
        <span>{running ? (running > 1 ? tr('{0} tasks running', running) : tr('{0} task running', running)) : tr('Tasks')}</span>
        <span className="grow" />
        {collapsed ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
        <button
          className="icon-btn"
          title={tr('Clear finished')}
          onClick={(e) => {
            e.stopPropagation()
            jobs.set(jobs.get().filter((j) => j.state === 'running'))
          }}
        >
          <X size={14} />
        </button>
      </div>
      {!collapsed && (
        <div className="jobs-list">
          {visible.map((j) => {
            const ls = Object.entries(j.layers)
            const total = ls.reduce((a, [, l]) => a + (l.total || 0), 0)
            const cur = ls.reduce((a, [, l]) => a + (l.status.match(/complete|Already|Pushed|exists/i) ? l.total || 0 : l.current || 0), 0)
            return (
              <div key={j.id} className={`job job-${j.state}`}>
                <div className="job-title" onClick={() => updateJob(j.id, (x) => ({ ...x, open: !x.open }))}>
                  {j.state === 'running' ? <Loader2 size={14} className="spin" /> : j.state === 'done' ? <CheckCircle2 size={14} className="tone-ok" /> : <XCircle size={14} className="tone-bad" />}
                  <span className="ellipsis">{j.title}</span>
                  {j.summary && <span className="muted small">{j.summary}</span>}
                  {j.state === 'error' && !j.open && j.error && <span className="tone-bad small ellipsis" title={j.error}>{j.error}</span>}
                  {ls.length > 0 && j.state === 'running' && <span className="muted small">{total ? `${bytes(cur)} / ${bytes(total)}` : `${ls.length} capas`}</span>}
                </div>
                {ls.length > 0 && j.state === 'running' && (
                  <div className="progress">
                    <div style={{ width: `${total ? Math.min(100, (cur / total) * 100) : 5}%` }} />
                  </div>
                )}
                {j.open && (
                  <div className="job-log">
                    {ls.map(([id, l]) => (
                      <div key={id} className="job-layer">
                        <code>{id.slice(0, 12)}</code> {l.status}
                        {l.total ? ` ${bytes(l.current)} / ${bytes(l.total)}` : ''}
                      </div>
                    ))}
                    {j.lines.map((l, i) => (
                      <div key={i}>{l}</div>
                    ))}
                    {j.error && <div className="tone-bad">✗ {j.error}</div>}
                    <div ref={(el) => void (endRefs.current[j.id] = el)} />
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ---------- drawer (record preview) ----------
export function Drawer({ children, onClose, width = 560 }: { children: ReactNode; onClose: () => void; width?: number }) {
  useEffect(() => {
    const on = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [onClose])
  return createPortal(
    <div className="drawer-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="drawer" style={{ width }}>
        {children}
      </aside>
    </div>,
    document.body,
  )
}

export function OverlayHost() {
  return (
    <>
      <DialogHost />
      <JobsTray />
      <Toaster />
    </>
  )
}
