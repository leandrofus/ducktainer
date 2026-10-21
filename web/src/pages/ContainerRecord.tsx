import { t as tr } from '../i18n'
import { useEffect, useMemo, useState } from 'react'
import {
  Play, Square, RotateCw, Terminal as TermIcon, ScrollText, Box, Layers, Image as ImageIcon, Network as NetIcon, HardDrive,
  ChevronRight, Cpu, MemoryStick, ArrowDownUp, Clock, AlertTriangle, Maximize2, Save, Undo2, ClipboardCopy, Search,
} from 'lucide-react'
import { api, enc, stream } from '../lib/api'
import type { Container, DockerEvent, Spec, Stats } from '../lib/types'
import { useResource, invalidate } from '../lib/store'
import { ago, bytes, cname, copy, dateTime, healthOf, shortId, stackOf, humanStatus } from '../lib/format'
import { go, href } from '../lib/router'
import { Avatar, Empty, MenuButton, Pill, Props, Section, Sparkline, Spinner, Tabs, Tag } from '../ui/common'
import { useMenu } from '../ui/ContextMenu'
import { runJob, toast, confirm } from '../ui/overlays'
import { containerActions } from '../actions'
import { LogViewer } from '../components/LogViewer'
import { TerminalView } from '../components/TerminalView'
import { FileBrowser } from '../components/FileBrowser'
import { ContainerForm, emptySpec } from '../components/ContainerForm'
import { PortTags, StackTag } from './Containers'

function useContainer(ep: string, id: string) {
  const list = useResource<Container[]>(`/e/${enc(ep)}/containers`, { poll: 10000 })
  const inspect = useResource<any>(`/e/${enc(ep)}/containers/${enc(id)}`)
  const c = list.data?.find((x) => x.Id === id || x.Id.startsWith(id) || cname(x) === id)
  return { c, inspect: inspect.data, error: inspect.error, list }
}

export function useStats(ep: string, id: string, enabled: boolean, keep = 60) {
  const [samples, setSamples] = useState<Stats[]>([])
  useEffect(() => {
    if (!enabled) return
    setSamples([])
    const ac = new AbortController()
    stream('GET', `/e/${enc(ep)}/containers/${enc(id)}/stats`, undefined, (s) => {
      if (s.read) setSamples((p) => [...p.slice(-(keep - 1)), s])
    }, ac.signal).catch(() => {})
    return () => ac.abort()
  }, [ep, id, enabled, keep])
  return samples
}

function rate(samples: Stats[], f: (s: Stats) => number) {
  if (samples.length < 2) return 0
  const a = samples[samples.length - 2]
  const b = samples[samples.length - 1]
  const dt = (Date.parse(b.read) - Date.parse(a.read)) / 1000 || 1
  return Math.max(0, (f(b) - f(a)) / dt)
}

function StatTiles({ samples }: { samples: Stats[] }) {
  const last = samples[samples.length - 1]
  const netRates = samples.map((_, i) => (i === 0 ? 0 : rate(samples.slice(0, i + 1), (s) => s.net_rx + s.net_tx)))
  return (
    <div className="tiles">
      <div className="tile">
        <div className="tile-label"><Cpu size={14} /> CPU</div>
        <div className="tile-value">{last ? `${last.cpu.toFixed(1)}%` : '—'}</div>
        <Sparkline values={samples.map((s) => s.cpu)} max={Math.max(100, ...samples.map((s) => s.cpu))} />
      </div>
      <div className="tile">
        <div className="tile-label"><MemoryStick size={14} /> {tr('Memory')}</div>
        <div className="tile-value">
          {last ? bytes(last.mem) : '—'}
          {last && <span className="muted small"> / {bytes(last.mem_limit)}</span>}
        </div>
        <Sparkline values={samples.map((s) => s.mem)} max={Math.max(...samples.map((s) => s.mem), 1) * 1.2} tone="violet" />
      </div>
      <div className="tile">
        <div className="tile-label"><ArrowDownUp size={14} /> {tr('Network')}</div>
        <div className="tile-value">{last ? `${bytes(rate(samples, (s) => s.net_rx + s.net_tx))}/s` : '—'}</div>
        <Sparkline values={netRates} tone="teal" />
      </div>
      <div className="tile">
        <div className="tile-label"><HardDrive size={14} /> {tr('Disk I/O')}</div>
        <div className="tile-value">{last ? `${bytes(rate(samples, (s) => s.blk_read + s.blk_write))}/s` : '—'}</div>
        <div className="muted small">{last ? tr('{0} processes · read {1}', last.pids, bytes(last.blk_read)) : ''}</div>
      </div>
    </div>
  )
}

const ACTION_LABEL: Record<string, string> = {
  create: tr('Created'), start: tr('Started'), stop: tr('Stopped'), die: tr('Exited'), kill: tr('Signal sent'), restart: tr('Restarted'), pause: tr('Paused'),
  unpause: tr('Resumed'), destroy: tr('Removed'), rename: tr('Renamed'), oom: tr('Out of memory (OOM)'), update: tr('Resources updated'),
  'health_status: healthy': tr('Healthy'), 'health_status: unhealthy': tr('Unhealthy'), connect: tr('Connected to network'), disconnect: tr('Disconnected from network'),
  pull: tr('Image pulled'), tag: tr('Tagged'), delete: tr('Deleted'), mount: tr('Volume mounted'), unmount: tr('Volume unmounted'),
}
export const eventLabel = (e: DockerEvent) => ACTION_LABEL[e.Action] ?? ACTION_LABEL[e.Action.split(':')[0]] ?? e.Action

function Timeline({ ep, id }: { ep: string; id: string }) {
  const [events, setEvents] = useState<DockerEvent[] | null>(null)
  useEffect(() => {
    const ac = new AbortController()
    const acc: DockerEvent[] = []
    const now = Math.floor(Date.now() / 1000)
    const filters = JSON.stringify({ container: [id] })
    stream('GET', `/e/${enc(ep)}/events?since=${now - 7 * 86400}&until=${now}&filters=${enc(filters)}`, undefined, (e) => e.Action && !e.Action.startsWith('exec_') && acc.push(e), ac.signal)
      .then(() => setEvents(acc.reverse().slice(0, 50)))
      .catch(() => setEvents([]))
    return () => ac.abort()
  }, [ep, id])
  if (!events) return <Spinner />
  if (!events.length) return <p className="muted small">{tr('No activity in the last 7 days.')}</p>
  return (
    <ol className="timeline">
      {events.map((e, i) => (
        <li key={i} className={`tl-${e.Action.split(':')[0]}`}>
          <span className="tl-dot" />
          <div>
            <strong>{eventLabel(e)}</strong>
            {e.Action === 'die' && e.Actor.Attributes.exitCode && <span className="muted"> {tr('· code')} {e.Actor.Attributes.exitCode}</span>}
            {e.Type === 'network' && <span className="muted"> · {e.Actor.Attributes.name}</span>}
            <div className="muted small" title={dateTime(e.time)}>{ago(e.time)}</div>
          </div>
        </li>
      ))}
    </ol>
  )
}

/** Compact record used in the preview drawer. */
export function ContainerSummary({ ep, id, onClose }: { ep: string; id: string; onClose: () => void }) {
  const { c } = useContainer(ep, id)
  const samples = useStats(ep, id, c?.State === 'running', 30)
  const menu = useMenu()
  if (!c) return <div className="center"><Spinner /></div>
  return (
    <div className="summary" onContextMenu={menu(() => containerActions(ep, c))}>
      <div className="summary-head">
        <Avatar name={c.Labels?.['com.docker.compose.service'] || cname(c)} size={40} />
        <div className="grow">
          <h2>{cname(c)}</h2>
          <div className="row gap">
            <Pill state={c.State} />
            <StackTag ep={ep} name={stackOf(c)} />
          </div>
        </div>
        <a className="btn btn-sm" href={href(ep, 'containers', c.Id)} onClick={onClose}>
          <Maximize2 size={14} /> {tr('Open record')}
        </a>
        <MenuButton items={() => containerActions(ep, c)} />
      </div>
      <QuickActions ep={ep} c={c} />
      {c.State === 'running' && <StatTiles samples={samples} />}
      <Props
        items={[
          [tr('Image'), <span className="mono small">{c.Image}</span>, () => [{ label: tr('Copy image'), onClick: () => copy(c.Image) }]],
          [tr('Status'), humanStatus(c.Status)],
          [tr('Ports'), <PortTags ep={ep} c={c} max={8} />],
          ['ID', <span className="mono small">{shortId(c.Id)}</span>, () => [{ label: tr('Copy ID'), onClick: () => copy(c.Id) }]],
        ]}
      />
      <div className="summary-logs">
        <LogViewer alert={{ ep, container: () => cname(c) }} path={`/e/${enc(ep)}/containers/${enc(c.Id)}/logs`} height="320px" />
      </div>
    </div>
  )
}

function QuickActions({ ep, c }: { ep: string; c: Container }) {
  const running = c.State === 'running'
  const base = `/e/${enc(ep)}/containers/${enc(c.Id)}`
  const [busy, setBusy] = useState('')
  const run = async (a: string) => {
    setBusy(a)
    try {
      await api.post(`${base}/action/${a}`)
    } catch (e) {
      toast.err(e)
    }
    setBusy('')
    invalidate('/containers', '/stacks', `/containers/${enc(c.Id)}`)
  }
  return (
    <div className="quick">
      {running ? (
        <button className="btn" disabled={!!busy} onClick={() => run('stop')}>
          {busy === 'stop' ? <Spinner /> : <Square size={14} />} {tr('Stop')}
        </button>
      ) : (
        <button className="btn btn-primary" disabled={!!busy} onClick={() => run(c.State === 'paused' ? 'unpause' : 'start')}>
          {busy === 'start' ? <Spinner /> : <Play size={14} />} {tr('Start')}
        </button>
      )}
      <button className="btn" disabled={!!busy} onClick={() => run('restart')}>
        {busy === 'restart' ? <Spinner /> : <RotateCw size={14} />} {tr('Restart')}
      </button>
      <a className={`btn${running ? '' : ' disabled'}`} href={href(ep, 'containers', c.Id, 'terminal')}>
        <TermIcon size={14} /> {tr('Terminal')}
      </a>
      <a className="btn" href={href(ep, 'containers', c.Id, 'logs')}>
        <ScrollText size={14} /> {tr('Logs')}
      </a>
    </div>
  )
}

export function ContainerRecord({ ep, id, tab = 'overview', query }: { ep: string; id: string; tab?: string; query: URLSearchParams }) {
  const { c, inspect, error } = useContainer(ep, id)
  const menu = useMenu()
  const running = c?.State === 'running'
  const samples = useStats(ep, c?.Id || id, !!running && (tab === 'overview' || tab === 'stats'), tab === 'stats' ? 120 : 40)

  if (error && !c) return <div className="page"><Empty icon={<Box size={28} />} title={tr('Container not found')}>{error.message}</Empty></div>
  if (!c) return <div className="page center"><Spinner /></div>

  const name = cname(c)
  const stack = stackOf(c)
  const nets = Object.entries(c.NetworkSettings?.Networks || {})
  const h = healthOf(c.Status)
  const restart = inspect?.HostConfig?.RestartPolicy?.Name
  const setTab = (t: string) => go(ep, 'containers', c.Id, t === 'overview' ? undefined : t)

  return (
    <div className="page record">
      <div className="breadcrumbs">
        <a href={href(ep, 'containers')}>{tr('Containers')}</a>
        <ChevronRight size={13} />
        {stack && (
          <>
            <a href={href(ep, 'stacks', stack)}>{stack}</a>
            <ChevronRight size={13} />
          </>
        )}
        <span>{name}</span>
      </div>
      <header className="record-head" onContextMenu={menu(() => containerActions(ep, c))}>
        <Avatar name={c.Labels?.['com.docker.compose.service'] || name} size={52} square />
        <div className="record-title">
          <h1>{name}</h1>
          <div className="row gap wrap">
            <Pill state={c.State} />
            {h && <Tag tone={h === 'healthy' ? 'ok' : h === 'unhealthy' ? 'bad' : 'warn'}>{h}</Tag>}
            <StackTag ep={ep} name={stack} />
            <span className="muted small mono">{c.Image}</span>
          </div>
        </div>
        <div className="record-actions">
          <QuickActions ep={ep} c={c} />
          <MenuButton items={() => containerActions(ep, c)} className="btn" />
        </div>
      </header>

      <div className="record-body">
        <aside className="record-side">
          <Section title={tr('Properties')}>
            <Props
              items={[
                [tr('Status'), humanStatus(c.Status)],
                [tr('Created'), <span title={dateTime(c.Created)}>{ago(c.Created)}</span>],
                inspect && [tr('Started'), inspect.State?.StartedAt?.startsWith('0001') ? '—' : <span title={dateTime(inspect.State?.StartedAt)}>{ago(inspect.State?.StartedAt)}</span>],
                [tr('Restart policy'), restart || 'no'],
                inspect && [tr('Restarts'), inspect.RestartCount],
                [tr('Command'), <span className="mono small">{c.Command}</span>, () => [{ label: tr('Copy command'), onClick: () => copy(c.Command) }]],
                ['ID', <span className="mono small">{shortId(c.Id)}</span>, () => [{ label: tr('Copy full ID'), onClick: () => copy(c.Id) }]],
                inspect?.Config?.Hostname && ['Hostname', <span className="mono small">{inspect.Config.Hostname}</span>],
                inspect?.State?.Pid > 0 && ['PID', inspect.State.Pid],
              ]}
            />
          </Section>
          <Section title={tr('Ports')}>
            {c.Ports.some((p) => p.PublicPort) ? <PortTags ep={ep} c={c} max={20} /> : <span className="muted small">{tr('No published ports')}</span>}
          </Section>
          <Section title={tr('Networks · {0}', nets.length)}>
            <ul className="rel-list">
              {nets.map(([n, info]) => (
                <li
                  key={n}
                  onClick={() => go(ep, 'networks', info.NetworkID)}
                  onContextMenu={menu(() => [
                    { header: n },
                    { label: tr('Open network'), icon: <NetIcon size={14} />, onClick: () => go(ep, 'networks', info.NetworkID) },
                    { label: tr('Copy IP'), hint: info.IPAddress, onClick: () => copy(info.IPAddress) },
                    info.MacAddress && { label: tr('Copy MAC'), onClick: () => copy(info.MacAddress) },
                    'sep',
                    {
                      label: tr('Disconnect'),
                      danger: true,
                      onClick: async () => {
                        try {
                          await api.post(`/e/${enc(ep)}/networks/${enc(info.NetworkID)}/disconnect`, { container: c.Id })
                          invalidate('/containers', '/networks')
                        } catch (e) {
                          toast.err(e)
                        }
                      },
                    },
                  ])}
                >
                  <NetIcon size={14} />
                  <span className="grow">{n}</span>
                  <span className="mono small muted">{info.IPAddress || '—'}</span>
                </li>
              ))}
            </ul>
          </Section>
          <Section title={tr('Mounts · {0}', c.Mounts.length)}>
            <ul className="rel-list">
              {c.Mounts.map((m) => (
                <li
                  key={m.Destination}
                  onClick={() => (m.Type === 'volume' ? go(ep, 'volumes', m.Name!) : undefined)}
                  onContextMenu={menu(() => [
                    { header: m.Destination },
                    m.Type === 'volume' && { label: tr('Open volume'), icon: <HardDrive size={14} />, onClick: () => go(ep, 'volumes', m.Name!) },
                    running && { label: tr('Browse in the container'), onClick: () => (location.hash = href(ep, 'containers', c.Id, 'files') + `?path=${enc(m.Destination)}`) },
                    { label: tr('Copy container path'), onClick: () => copy(m.Destination) },
                    { label: tr('Copy host source'), onClick: () => copy(m.Source) },
                  ])}
                >
                  <HardDrive size={14} />
                  <span className="grow ellipsis" title={m.Source}>
                    <span className="mono small">{m.Destination}</span>
                    <span className="muted small"> ← {m.Type === 'volume' ? (m.Name!.length > 20 ? shortId(m.Name!) : m.Name) : m.Source}</span>
                  </span>
                  {!m.RW && <Tag>ro</Tag>}
                </li>
              ))}
              {c.Mounts.length === 0 && <li className="muted small">{tr('No mounts')}</li>}
            </ul>
          </Section>
          <Section title={tr('Relations')}>
            <ul className="rel-list">
              {stack && (
                <li onClick={() => go(ep, 'stacks', stack)}>
                  <Layers size={14} /> <span className="grow">Stack {stack}</span>
                  <span className="muted small">{c.Labels['com.docker.compose.service']}</span>
                </li>
              )}
              <li onClick={() => go(ep, 'images', c.ImageID)}>
                <ImageIcon size={14} /> <span className="grow ellipsis">{c.Image}</span>
              </li>
            </ul>
          </Section>
        </aside>

        <main className="record-main">
          <Tabs
            active={tab}
            onChange={setTab}
            tabs={[
              { id: 'overview', label: tr('Overview') },
              { id: 'logs', label: tr('Logs') },
              { id: 'terminal', label: tr('Terminal') },
              { id: 'files', label: tr('Files') },
              { id: 'stats', label: tr('Performance') },
              { id: 'processes', label: tr('Processes') },
              { id: 'config', label: tr('Settings') },
              { id: 'inspect', label: tr('Inspect') },
            ].map((t) => ({ ...t, menu: () => [{ label: tr('Open in this tab'), onClick: () => setTab(t.id) }, { label: tr('Open in another browser tab'), onClick: () => window.open(href(ep, 'containers', c.Id, t.id), '_blank') }] }))}
          />
          <div className="tab-body">
            {tab === 'overview' && (
              <div className="overview">
                {running ? <StatTiles samples={samples} /> : <div className="alert"><AlertTriangle size={15} /> {humanStatus(c.Status)}</div>}
                <div className="overview-grid">
                  <div className="panel">
                    <h4>{tr('Latest logs')}</h4>
                    <LogViewer alert={{ ep, container: () => cname(c) }} path={`/e/${enc(ep)}/containers/${enc(c.Id)}/logs`} height="360px" />
                  </div>
                  <div className="panel">
                    <h4><Clock size={14} /> {tr('Activity')}</h4>
                    <Timeline ep={ep} id={c.Id} />
                  </div>
                </div>
              </div>
            )}
            {tab === 'logs' && <LogViewer alert={{ ep, container: () => cname(c) }} path={`/e/${enc(ep)}/containers/${enc(c.Id)}/logs`} height="calc(100vh - 290px)" />}
            {tab === 'terminal' && <TerminalView ep={ep} id={c.Id} disabled={!running} />}
            {tab === 'files' && <FileBrowser ep={ep} id={c.Id} initial={query.get('path') || inspect?.Config?.WorkingDir || '/'} />}
            {tab === 'stats' && (running ? <><StatTiles samples={samples} /><p className="muted small">{tr('Live samples from the last ~2 minutes.')}</p></> : <Empty title={tr('The container is not running')} />)}
            {tab === 'processes' && <Processes ep={ep} id={c.Id} running={running} />}
            {tab === 'config' && <ConfigEditor ep={ep} c={c} />}
            {tab === 'inspect' && <JsonView value={inspect} />}
          </div>
        </main>
      </div>
    </div>
  )
}

function Processes({ ep, id, running }: { ep: string; id: string; running: boolean }) {
  const { data, error, reload } = useResource<{ Titles: string[]; Processes: string[][] }>(running ? `/e/${enc(ep)}/containers/${enc(id)}/top` : null, { poll: 5000 })
  const menu = useMenu()
  if (!running) return <Empty title={tr('The container is not running')} />
  if (error) return <div className="alert alert-bad">{error.message}</div>
  if (!data) return <Spinner />
  return (
    <div className="table-wrap">
      <table className="table compact">
        <thead>
          <tr>{data.Titles.map((t) => <th key={t}>{t}</th>)}</tr>
        </thead>
        <tbody>
          {data.Processes.map((p, i) => (
            <tr key={i} onContextMenu={menu(() => [
              { label: tr('Copy command'), onClick: () => copy(p[p.length - 1]) },
              { label: tr('Copy PID'), onClick: () => copy(p[1]) },
              { label: tr('Reload'), onClick: reload },
            ])}>
              {p.map((v, j) => <td key={j} className={j === p.length - 1 ? 'mono small' : 'small'}>{v}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ConfigEditor({ ep, c }: { ep: string; c: Container }) {
  const base = `/e/${enc(ep)}/containers/${enc(c.Id)}`
  const [orig, setOrig] = useState<Spec | null>(null)
  const [spec, setSpec] = useState<Spec | null>(null)
  const [pull, setPull] = useState(false)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    api.get<Spec>(`${base}/spec`).then((s) => {
      const full = emptySpec(s)
      setOrig(full)
      setSpec(full)
    }).catch((e) => toast.err(e))
  }, [base])
  const dirty = useMemo(() => JSON.stringify(orig) !== JSON.stringify(spec), [orig, spec])
  if (!spec) return <Spinner />
  const stack = stackOf(c)
  const save = async () => {
    if (stack && !(await confirm({ title: tr('Container managed by compose'), body: tr('This container belongs to stack “{0}”. If you edit it here, the next “docker compose up” will apply the compose again. For permanent changes edit the stack compose.', stack), confirm: tr('Edit anyway') }))) return
    setBusy(true)
    try {
      const res = await runJob(tr('Recreate {0}', cname(c)), 'POST', `${base}/recreate`, { spec, pull })
      invalidate('/containers', '/stacks')
      if (res?.id) go(ep, 'containers', res.id, 'config')
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="config-editor">
      <div className="alert">
        {tr('Docker cannot modify a container in place: when saving, ducktainer creates a new one with this configuration, swaps the names and removes the old one. Volumes are kept, and if the new one does not start the original is restored.')}
      </div>
      <ContainerForm ep={ep} value={spec} onChange={setSpec} />
      <div className={`savebar${dirty ? ' dirty' : ''}`}>
        <span>{dirty ? tr('Unsaved changes') : tr('No changes')}</span>
        <label className="check">
          <input type="checkbox" checked={pull} onChange={(e) => setPull(e.target.checked)} /> {tr('Pull the image first')}
        </label>
        <span className="grow" />
        <button className="btn" disabled={!dirty || busy} onClick={() => setSpec(orig)}>
          <Undo2 size={14} /> {tr('Discard')}
        </button>
        <button className="btn btn-primary" disabled={(!dirty && !pull) || busy} onClick={save}>
          <Save size={14} /> {tr('Save and recreate')}
        </button>
      </div>
    </div>
  )
}

export function JsonView({ value }: { value: any }) {
  const [q, setQ] = useState('')
  const text = useMemo(() => JSON.stringify(value, null, 2) ?? '', [value])
  const lines = useMemo(() => (q ? text.split('\n').filter((l) => l.toLowerCase().includes(q.toLowerCase())) : null), [text, q])
  if (!value) return <Spinner />
  return (
    <div className="json-view">
      <div className="logs-bar">
        <div className="search">
          <Search size={14} />
          <input placeholder={tr('Filter lines…')} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <button className="btn btn-sm" onClick={() => copy(text)}>
          <ClipboardCopy size={14} /> {tr('Copy JSON')}
        </button>
      </div>
      <pre>{lines ? lines.join('\n') : text}</pre>
    </div>
  )
}
