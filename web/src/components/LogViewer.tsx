import { t as tr } from '../i18n'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Pause, Play, Search, Trash2, Download, WrapText, Clock, AlertCircle, BellPlus } from 'lucide-react'
import { stream } from '../lib/api'
import type { LogLine } from '../lib/types'
import { copy, hue } from '../lib/format'
import { useMenu } from '../ui/ContextMenu'
import { usePref } from '../lib/prefs'
import { isViewer } from '../lib/session'
import { suggestPattern } from '../lib/regex'
import { openDialog } from '../ui/overlays'
import { RuleDialog } from '../pages/Alerts'

const MAX = 8000

// Minimal ANSI SGR → spans (colors + bold); everything else is stripped.
const ANSI = /\x1b\[([\d;]*)m/g
const PALETTE = ['#4b5563', '#ef4444', '#22c55e', '#eab308', '#3b82f6', '#a855f7', '#06b6d4', '#d1d5db']
const BRIGHT = ['#6b7280', '#f87171', '#4ade80', '#facc15', '#60a5fa', '#c084fc', '#22d3ee', '#f9fafb']
function ansi(text: string): ReactNode {
  if (!text.includes('\x1b')) return text
  const out: ReactNode[] = []
  let last = 0
  let style: React.CSSProperties = {}
  let m: RegExpExecArray | null
  ANSI.lastIndex = 0
  while ((m = ANSI.exec(text))) {
    if (m.index > last) out.push(<span key={last} style={style}>{text.slice(last, m.index)}</span>)
    last = ANSI.lastIndex
    for (const code of (m[1] || '0').split(';').map(Number)) {
      if (code === 0) style = {}
      else if (code === 1) style = { ...style, fontWeight: 600 }
      else if (code >= 30 && code <= 37) style = { ...style, color: PALETTE[code - 30] }
      else if (code >= 90 && code <= 97) style = { ...style, color: BRIGHT[code - 90] }
      else if (code === 39) style = { ...style, color: undefined }
    }
  }
  if (last < text.length) out.push(<span key={last} style={style}>{text.slice(last).replace(/\x1b\[[\d;?]*[A-Za-z]/g, '')}</span>)
  return out
}
const strip = (s: string) => s.replace(/\x1b\[[\d;?]*[A-Za-z]/g, '')

const LEVEL = /\b(ERROR|ERR|FATAL|PANIC|CRIT(ICAL)?|WARN(ING)?)\b/i
const ERRLEVEL = /\b(ERROR|ERR|FATAL|PANIC|CRIT(ICAL)?)\b|"level":"(error|fatal)"/i

export interface LogAlertScope {
  ep: string
  /** container name (or glob) the rule should watch; `source` is the line's label in multi-source logs */
  container: (source?: string) => string
}

export function LogViewer({ path, multi, height, alert }: { path: string; multi?: boolean; height?: string; alert?: LogAlertScope }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [follow, setFollow] = useState(true)
  const [tail, setTail] = usePref('logs.tail', '500')
  const [wrap, setWrap] = usePref('logs.wrap', true)
  const [ts, setTs] = usePref('logs.ts', false)
  const [query, setQuery] = useState('')
  const [onlyErr, setOnlyErr] = useState(false)
  const [only, setOnly] = useState<string | null>(null)
  const [status, setStatus] = useState<'live' | 'ended' | 'error'>('live')
  const [err, setErr] = useState('')
  const box = useRef<HTMLDivElement>(null)
  const atBottom = useRef(true)
  const menu = useMenu()

  useEffect(() => {
    const ac = new AbortController()
    let buf: LogLine[] = []
    let raf = 0
    const flush = () => {
      raf = 0
      const add = buf
      buf = []
      setLines((prev) => {
        const next = prev.concat(add)
        return next.length > MAX ? next.slice(next.length - MAX) : next
      })
    }
    setLines([])
    setStatus('live')
    stream('GET', `${path}${path.includes('?') ? '&' : '?'}tail=${tail}&follow=1`, undefined, (ev) => {
      if (ev.m !== undefined) {
        buf.push(ev)
        if (!raf) raf = requestAnimationFrame(flush)
      }
    }, ac.signal)
      .then(() => setStatus('ended'))
      .catch((e) => {
        if (ac.signal.aborted) return
        setStatus('error')
        setErr(e.message)
      })
    return () => {
      ac.abort()
      cancelAnimationFrame(raf)
    }
  }, [path, tail])

  const visible = useMemo(() => {
    const q = query.toLowerCase()
    return lines.filter(
      (l) => (!only || l.c === only) && (!onlyErr || l.s === 'err' || LEVEL.test(l.m)) && (!q || strip(l.m).toLowerCase().includes(q)),
    )
  }, [lines, query, onlyErr, only])

  useEffect(() => {
    if (follow && atBottom.current && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [visible, follow])

  const download = () => {
    const blob = new Blob([lines.map((l) => `${l.t ?? ''} ${l.c ? `[${l.c}] ` : ''}${strip(l.m)}`).join('\n')], { type: 'text/plain' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `logs-${Date.now()}.txt`
    a.click()
  }

  const sources = useMemo(() => (multi ? [...new Set(lines.map((l) => l.c || ''))].filter(Boolean).sort() : []), [lines, multi])

  // a rule from the selected text (or the whole line), checked against what is on screen
  const createAlert = (l: LogLine, selected: string) => {
    const seed = (selected || strip(l.m)).trim().slice(0, 300)
    const container = alert!.container(l.c)
    openDialog((close) => (
      <RuleDialog
        onClose={close}
        seed={seed}
        sample={visible.map((x) => strip(x.m))}
        initial={{
          id: '',
          name: tr('Log: {0}', seed.slice(0, 40)),
          kind: 'log_match',
          enabled: true,
          endpoints: [alert!.ep],
          containers: container ? [container] : [],
          patterns: [suggestPattern(seed, true)],
          cooldown_minutes: 5,
        }}
      />
    ))
  }

  const lineMenu = (l: LogLine) =>
    menu(() => [
      { label: tr('Copy line'), onClick: () => copy(strip(l.m)) },
      l.t && { label: tr('Copy with timestamp'), onClick: () => copy(`${l.t} ${strip(l.m)}`) },
      { label: tr('Copy everything visible'), hint: `${visible.length}`, onClick: () => copy(visible.map((x) => strip(x.m)).join('\n')) },
      'sep',
      window.getSelection()?.toString() && { label: tr('Search “{0}”', window.getSelection()!.toString().slice(0, 24)), icon: <Search size={14} />, onClick: () => setQuery(window.getSelection()!.toString()) },
      alert && !isViewer() && {
        label: window.getSelection()?.toString().trim() ? tr('Create alert from selection…') : tr('Create alert from this line…'),
        icon: <BellPlus size={14} />,
        onClick: () => createAlert(l, window.getSelection()?.toString() ?? ''),
      },
      l.c && { label: only ? tr('Show all services') : tr('Only {0}', l.c), onClick: () => setOnly(only ? null : l.c!) },
      { label: tr('Errors and warnings only'), checked: onlyErr, onClick: () => setOnlyErr(!onlyErr) },
      { label: tr('Timestamps'), checked: ts, onClick: () => setTs(!ts) },
      { label: tr('Wrap lines'), checked: wrap, onClick: () => setWrap(!wrap) },
      'sep',
      { label: follow ? tr('Pause following') : tr('Follow live'), onClick: () => setFollow(!follow) },
      { label: tr('Download .txt'), icon: <Download size={14} />, onClick: download },
      { label: tr('Clear view'), icon: <Trash2 size={14} />, onClick: () => setLines([]) },
    ])

  return (
    <div className="logs" style={{ height }}>
      <div className="logs-bar">
        <div className="search">
          <Search size={14} />
          <input placeholder={tr('Filter logs…')} value={query} onChange={(e) => setQuery(e.target.value)} />
          {query && <span className="muted small">{visible.length}</span>}
        </div>
        {multi && sources.length > 1 && (
          <select value={only ?? ''} onChange={(e) => setOnly(e.target.value || null)}>
            <option value="">{tr('All services')}</option>
            {sources.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        )}
        <select value={tail} onChange={(e) => setTail(e.target.value)} title={tr('Initial lines')}>
          <option value="100">{tr('last 100')}</option>
          <option value="500">{tr('last 500')}</option>
          <option value="2000">{tr('last 2000')}</option>
          <option value="all">{tr('all')}</option>
        </select>
        <button className={`icon-btn${onlyErr ? ' on' : ''}`} title={tr('Errors only')} onClick={() => setOnlyErr(!onlyErr)}>
          <AlertCircle size={15} />
        </button>
        <button className={`icon-btn${ts ? ' on' : ''}`} title={tr('Timestamps')} onClick={() => setTs(!ts)}>
          <Clock size={15} />
        </button>
        <button className={`icon-btn${wrap ? ' on' : ''}`} title={tr('Wrap lines')} onClick={() => setWrap(!wrap)}>
          <WrapText size={15} />
        </button>
        <button className="icon-btn" title={tr('Download')} onClick={download}>
          <Download size={15} />
        </button>
        <button className="icon-btn" title={tr('Clear')} onClick={() => setLines([])}>
          <Trash2 size={15} />
        </button>
        <button className={`btn btn-sm ${follow ? 'btn-live' : ''}`} onClick={() => setFollow(!follow)}>
          {follow ? <Pause size={13} /> : <Play size={13} />}
          {status === 'live' ? (follow ? tr('Live') : tr('Paused')) : status === 'ended' ? tr('Finished') : tr('Error')}
        </button>
      </div>
      <div
        className={`logs-body${wrap ? ' wrap' : ''}`}
        ref={box}
        onScroll={(e) => {
          const el = e.currentTarget
          atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
        }}
        onContextMenu={menu(() => [
          { label: tr('Copy everything visible'), onClick: () => copy(visible.map((x) => strip(x.m)).join('\n')) },
          { label: tr('Download .txt'), onClick: download },
          { label: tr('Clear view'), onClick: () => setLines([]) },
        ])}
      >
        {status === 'error' && <div className="log-line err">✗ {err}</div>}
        {visible.length === 0 && status !== 'error' && <div className="muted log-empty">{lines.length ? tr('Nothing matches the filter.') : tr('No output yet…')}</div>}
        {visible.map((l, i) => (
          <div key={i} className={`log-line${l.s === 'err' ? ' err' : ''}${ERRLEVEL.test(l.m) ? ' lvl-err' : LEVEL.test(l.m) ? ' lvl' : ''}`} onContextMenu={lineMenu(l)}>
            {ts && l.t && <span className="log-ts">{l.t.slice(5, 23).replace('T', ' ')}</span>}
            {l.c && (
              <span className="log-src" style={{ ['--h' as any]: hue(l.c) }} onClick={() => setOnly(only ? null : l.c!)}>
                {l.c}
              </span>
            )}
            <span className="log-msg">{query ? highlight(strip(l.m), query) : ansi(l.m)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function highlight(text: string, q: string): ReactNode {
  const i = text.toLowerCase().indexOf(q.toLowerCase())
  if (i < 0) return text
  return (
    <>
      {text.slice(0, i)}
      <mark>{text.slice(i, i + q.length)}</mark>
      {text.slice(i + q.length)}
    </>
  )
}
