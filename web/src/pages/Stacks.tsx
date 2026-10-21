import { t as tr, tc } from '../i18n'
import { useEffect, useMemo, useState } from 'react'
import { Layers, Plus, Search, ChevronRight, Rocket, Save, Undo2, FolderOpen, Box, Play, Square, RotateCw, RefreshCw, LayoutGrid, LayoutList, Combine, FolderInput } from 'lucide-react'
import { api, enc } from '../lib/api'
import type { Container, Endpoint, Stack, StackFiles, StackLocation } from '../lib/types'
import { useResource, invalidate } from '../lib/store'
import { usePref } from '../lib/prefs'
import { copy, humanStatus } from '../lib/format'
import { go, href } from '../lib/router'
import { Avatar, CodeEditor, DataTable, Empty, MenuButton, Pill, Section, Spinner, Tabs, Tag, Props } from '../ui/common'
import { useMenu } from '../ui/ContextMenu'
import type { MenuEntry } from '../ui/ContextMenu'
import { Modal, confirm, openDialog, runJob, toast } from '../ui/overlays'
import { containerActions, stackActions, stackRun } from '../actions'
import { LogViewer } from '../components/LogViewer'
import { ComposeVisual } from '../components/ComposeVisual'
import type { Known } from '../components/ComposeVisual'
import { parse as parseCompose, services as composeServices, topNames } from '../lib/composeDoc'
import { ContainerName, PortTags } from './Containers'

const SAMPLE = `services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
`

/** Whether an endpoint is a remote node (its files are not on this machine). */
export function useRemote(ep: string) {
  const { data } = useResource<Endpoint[]>('/endpoints')
  const e = data?.find((x) => x.name === ep)
  return !!e && !e.url.startsWith('unix://')
}

export interface Placement {
  mode: 'store' | 'dir'
  dir: string
  env_file: string
  existing: boolean
}

/** Request fields for where a new stack lives. */
export function placementBody(p: Placement, name: string) {
  if (p.mode === 'store') return {}
  return { location: { dir: p.dir || `/opt/${name}`, env_file: p.env_file }, existing: p.existing }
}

/** Store vs. a directory of its own (on the node for remote hosts). */
export function usePlacement(ep: string) {
  const remote = useRemote(ep)
  const [p, setP] = useState<Placement | null>(null)
  // remote hosts default to a directory on the node: relative paths in the
  // compose then resolve there and not in ducktainer's store
  return [p ?? { mode: remote ? 'dir' : 'store', dir: '', env_file: '', existing: false }, setP, remote] as const
}

export function PlacementFields({ name, value: p, onChange, remote, allowExisting }: { name: string; value: Placement; onChange: (p: Placement) => void; remote: boolean; allowExisting?: boolean }) {
  const dir = p.dir || `/opt/${name || tr('<name>')}`
  return (
    <div className="placement">
      <div className="seg seg-text">
        <button className={p.mode === 'store' ? 'on' : ''} onClick={() => onChange({ ...p, mode: 'store', existing: false })}>{tr('ducktainer store')}</button>
        <button className={p.mode === 'dir' ? 'on' : ''} onClick={() => onChange({ ...p, mode: 'dir' })}>{remote ? tr('Directory on the node') : tr('Directory on this host')}</button>
      </div>
      {p.mode === 'store' ? (
        <p className="help">
          {tr('The compose and .env are kept by ducktainer. Relative paths (./data, env_file) resolve from its data directory')}
          {remote && <strong> {tr('on this machine, not on the node')}</strong>}.
        </p>
      ) : (
        <>
          <div className="grid-2">
            <label className="field">
              <span>{tr('Project directory')}</span>
              <input className="mono" value={p.dir} placeholder={dir} onChange={(e) => onChange({ ...p, dir: e.target.value })} />
            </label>
            <label className="field">
              <span>{tr('.env file (optional)')}</span>
              <input className="mono" value={p.env_file} placeholder={`${dir.replace(/\/$/, '')}/.env`} onChange={(e) => onChange({ ...p, env_file: e.target.value })} />
            </label>
          </div>
          {allowExisting && (
            <label className="check">
              <input type="checkbox" checked={p.existing} onChange={(e) => onChange({ ...p, existing: e.target.checked })} /> {tr('Use the compose file already in that directory')}
            </label>
          )}
          <p className="help">
            {tr('compose.yaml and .env are written there and compose runs with that project directory: volumes like ./data, env_file and build contexts resolve inside it.')}
            {remote && ' ' + tr('On remote hosts compose runs on the node itself (in a temporary docker:cli container).')}
          </p>
        </>
      )}
    </div>
  )
}

export function NewStackDialog({ ep, onClose, initial }: { ep: string; onClose: () => void; initial?: { name?: string; compose?: string; env?: string; note?: string } }) {
  const [name, setName] = useState(initial?.name ?? '')
  const [compose, setCompose] = useState(initial?.compose ?? SAMPLE)
  const [env, setEnv] = useState(initial?.env ?? '')
  const [deploy, setDeploy] = useState(true)
  const [mode, setMode] = usePref<'yaml' | 'visual'>('compose.mode', 'visual')
  const [busy, setBusy] = useState(false)
  const [place, setPlace, remote] = usePlacement(ep)
  const existing = place.mode === 'dir' && place.existing
  const submit = async () => {
    setBusy(true)
    try {
      await runJob(tr('Create stack {0}', name), 'POST', `/e/${enc(ep)}/stacks`, { name, compose: existing ? '' : compose, env: existing ? '' : env, deploy, ...placementBody(place, name) })
      onClose()
      invalidate('/stacks', '/containers')
      go(ep, 'stacks', name)
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      wide="xl"
      title={tr('New stack (compose)')}
      icon={<Layers size={18} />}
      onClose={onClose}
      footer={
        <>
          <label className="check">
            <input type="checkbox" checked={deploy} onChange={(e) => setDeploy(e.target.checked)} /> {tr('Deploy now (up -d)')}
          </label>
          <span className="grow" />
          <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
          <button className="btn btn-primary" disabled={busy || !name || (!existing && !compose.trim())} onClick={submit}>
            <Rocket size={14} /> {tr('Create stack')}
          </button>
        </>
      }
    >
      {initial?.note && <div className="alert" dangerouslySetInnerHTML={{ __html: initial.note }} />}
      <label className="field">
        <span>{tr('Project name')}</span>
        <input value={name} placeholder="mi-stack" onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, '-'))} autoFocus />
      </label>
      <div className="field">
        <span>{tr('Location')}</span>
        <PlacementFields name={name} value={place} onChange={setPlace} remote={remote} allowExisting />
      </div>
      {!existing && <div className={mode === 'visual' ? 'stack-new-visual' : 'grid-editor'}>
        <div>
          <div className="label row gap">
            compose.yaml <span className="grow" />
            <div className="seg seg-text">
              <button className={mode === 'visual' ? 'on' : ''} onClick={() => setMode('visual')}>Visual</button>
              <button className={mode === 'yaml' ? 'on' : ''} onClick={() => setMode('yaml')}>YAML</button>
            </div>
          </div>
          {mode === 'yaml' ? <CodeEditor value={compose} onChange={setCompose} minRows={22} /> : <ComposeVisual text={compose} onChange={setCompose} envText={env} />}
        </div>
        <div>
          <div className="label">.env</div>
          <CodeEditor value={env} onChange={setEnv} minRows={22} placeholder={tr('VARIABLE=value')} />
        </div>
      </div>}
    </Modal>
  )
}

/** Moves a store stack to its own directory or repoints a located one. */
export function LocationDialog({ ep, stack, onClose, swarm }: { ep: string; stack: Pick<Stack, 'name' | 'location' | 'total'> & { managed?: boolean }; onClose: () => void; swarm?: boolean }) {
  const remote = useRemote(ep)
  const [loc, setLoc] = useState<StackLocation>({ dir: stack.location?.dir ?? '', env_file: stack.location?.env_file ?? '' })
  const [busy, setBusy] = useState(false)
  const stored = !stack.location && stack.managed !== false
  const save = async () => {
    setBusy(true)
    try {
      await api.put(`/e/${enc(ep)}${swarm ? '/swarm' : ''}/stacks/${enc(stack.name)}/location`, { dir: loc.dir || `/opt/${stack.name}`, env_file: loc.env_file })
      toast.ok(tr('Location of {0} updated', stack.name))
      invalidate(swarm ? '/swarm' : '/stacks')
      onClose()
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  const dir = loc.dir || `/opt/${stack.name}`
  return (
    <Modal
      title={tr('Location of {0}', stack.name)}
      icon={<FolderInput size={18} />}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
          <button className="btn btn-primary" disabled={busy} onClick={save}>{stored ? tr('Move files') : tr('Save')}</button>
        </>
      }
    >
      <div className="grid-2">
        <label className="field">
          <span>{remote ? tr('Directory on the node') : tr('Project directory')}</span>
          <input className="mono" value={loc.dir} placeholder={dir} onChange={(e) => setLoc({ ...loc, dir: e.target.value })} autoFocus />
        </label>
        <label className="field">
          <span>{tr('.env file (optional)')}</span>
          <input className="mono" value={loc.env_file} placeholder={`${dir.replace(/\/$/, '')}/.env`} onChange={(e) => setLoc({ ...loc, env_file: e.target.value })} />
        </label>
      </div>
      <p className="help">
        {stored
          ? tr('compose.yaml and .env are copied there and removed from the ducktainer store. The directory must not contain a compose file yet.')
          : tr('The directory must already contain the compose file; nothing is copied.')}
      </p>
      {stack.total > 0 && <div className="alert alert-warn">{tr('Relative bind mounts (./data) will point to the new directory on the next deploy: move their data there first.')}</div>}
    </Modal>
  )
}

export function Stacks({ ep }: { ep: string }) {
  const { data, error } = useResource<Stack[]>(`/e/${enc(ep)}/stacks`, { poll: 10000 })
  const [q, setQ] = useState('')
  const [layout, setLayout] = usePref<'cards' | 'table'>('stacks.layout', 'cards')
  const [filter, setFilter] = usePref('stacks.filter', 'all')
  const menu = useMenu()
  const all = data || []
  const rows = all.filter((s) => (!q || s.name.includes(q.toLowerCase())) && (filter === 'all' || (filter === 'managed' ? s.managed : filter === s.status)))
  const newStack = () => openDialog((close) => <NewStackDialog ep={ep} onClose={close} />)
  return (
    <div
      className="page"
      onContextMenu={(e) =>
        (e.target as HTMLElement).closest('.page-body') &&
        menu(() => [
          { label: tr('New stack…'), icon: <Plus size={14} />, onClick: newStack },
          { label: tr('Go to the marketplace'), onClick: () => go(ep, 'marketplace') },
          { label: tr('Reload'), onClick: () => invalidate('/stacks') },
          'sep',
          { label: tr('Cards'), checked: layout === 'cards', onClick: () => setLayout('cards') },
          { label: tr('Table'), checked: layout === 'table', onClick: () => setLayout('table') },
        ])(e)
      }
    >
      <div className="page-head">
        <div className="page-title">
          <Layers size={20} />
          <h1>Stacks</h1>
          <span className="count">{all.length}</span>
        </div>
        <div className="page-actions">
          <div className="search">
            <Search size={14} />
            <input placeholder={tr('Search stack…')} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <div className="seg">
            <button className={layout === 'cards' ? 'on' : ''} onClick={() => setLayout('cards')}><LayoutGrid size={15} /></button>
            <button className={layout === 'table' ? 'on' : ''} onClick={() => setLayout('table')}><LayoutList size={15} /></button>
          </div>
          <button className="btn btn-primary needs-admin" onClick={newStack}>
            <Plus size={15} /> {tr('New stack')}
          </button>
        </div>
      </div>
      <Tabs
        active={filter}
        onChange={setFilter}
        tabs={[
          { id: 'all', label: tr('All'), count: all.length },
          { id: 'running', label: tr('Running'), count: all.filter((s) => s.status === 'running').length },
          { id: 'partial', label: tc('pl', 'Partial'), count: all.filter((s) => s.status === 'partial').length },
          { id: 'stopped', label: tc('pl', 'Stopped'), count: all.filter((s) => s.status === 'stopped' || s.status === 'empty').length },
          { id: 'managed', label: tc('pl', 'Created with ducktainer'), count: all.filter((s) => s.managed).length },
        ]}
      />
      <div className="page-body">
        {error && <div className="alert alert-bad">{error.message}</div>}
        {!data && !error && <div className="center"><Spinner /></div>}
        {data && rows.length === 0 && <Empty icon={<Layers size={28} />} title={tr('No stacks')} action={<button className="btn btn-primary" onClick={newStack}><Plus size={15} /> {tr('Create stack')}</button>}>{tr('Create one by writing a compose file or from the marketplace.')}</Empty>}
        {layout === 'cards' && (
          <div className="cards">
            {rows.map((s) => (
              <div key={s.name} className="stack-card" onClick={() => go(ep, 'stacks', s.name)} onContextMenu={menu(() => stackActions(ep, s))}>
                <div className="row gap">
                  <Avatar name={s.name} size={34} square />
                  <div className="grow">
                    <strong>{s.name}</strong>
                    <div className="muted small ellipsis" title={s.working_dir}>{s.managed && !s.location ? tr('managed by ducktainer') : s.working_dir || tr('external')}</div>
                  </div>
                  <MenuButton items={() => stackActions(ep, s)} />
                </div>
                <div className="row gap">
                  <Pill state={s.status} />
                  <span className="muted small">{s.running}/{s.total} {tr('containers')}</span>
                </div>
                <div className="svc-dots">
                  {s.containers.map((c) => (
                    <span key={c.id} className={`svc-dot tone-${c.state === 'running' ? 'ok' : 'idle'}`} title={`${c.service}: ${c.status}`} onClick={(e) => (e.stopPropagation(), go(ep, 'containers', c.id))} onContextMenu={menu(() => [{ header: c.name }, { label: tr('Open container'), onClick: () => go(ep, 'containers', c.id) }, { label: tr('Logs'), onClick: () => go(ep, 'containers', c.id, 'logs') }, { label: tr('Terminal'), disabled: c.state !== 'running', onClick: () => go(ep, 'containers', c.id, 'terminal') }])}>
                      {c.service || c.name}
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
        {layout === 'table' && rows.length > 0 && (
          <DataTable
            rows={rows}
            rowKey={(s) => s.name}
            onOpen={(s) => go(ep, 'stacks', s.name)}
            rowMenu={(s) => stackActions(ep, s)}
            defaultSort={{ key: 'name', dir: 1 }}
            columns={[
              { key: 'name', label: 'Stack', sort: (s) => s.name, render: (s) => <span className="cell-name"><Avatar name={s.name} size={24} square /><strong>{s.name}</strong></span> },
              { key: 'status', label: tr('Status'), width: 150, sort: (s) => s.status, render: (s) => <Pill state={s.status} /> },
              { key: 'n', label: tr('Containers'), width: 120, sort: (s) => s.total, render: (s) => `${s.running}/${s.total}` },
              { key: 'svc', label: tr('Services'), render: (s) => <span className="tags">{s.services.map((x) => <Tag key={x}>{x}</Tag>)}</span> },
              { key: 'dir', label: tr('Origin'), render: (s) => <span className="muted small ellipsis">{s.managed && !s.location ? 'ducktainer' : s.working_dir}</span> },
              { key: 'm', label: '', width: 36, render: (s) => <MenuButton items={() => stackActions(ep, s)} /> },
            ]}
          />
        )}
      </div>
    </div>
  )
}

export function StackRecord({ ep, name, tab = 'services' }: { ep: string; name: string; tab?: string }) {
  const { data, error } = useResource<{ stack: Stack; files?: StackFiles; files_error?: string }>(`/e/${enc(ep)}/stacks/${enc(name)}`, { poll: 8000 })
  const { data: containers } = useResource<Container[]>(`/e/${enc(ep)}/containers`)
  const menu = useMenu()
  if (error) return <div className="page"><Empty icon={<Layers size={28} />} title={tr('Stack not found')}>{error.message}</Empty></div>
  if (!data) return <div className="page center"><Spinner /></div>
  const s = data.stack
  const full = (containers || []).filter((c) => s.containers.some((x) => x.id === c.Id))
  const setTab = (t: string) => go(ep, 'stacks', name, t === 'services' ? undefined : t)
  return (
    <div className="page record">
      <div className="breadcrumbs">
        <a href={href(ep, 'stacks')}>Stacks</a>
        <ChevronRight size={13} />
        <span>{s.name}</span>
      </div>
      <header className="record-head" onContextMenu={menu(() => stackActions(ep, s))}>
        <Avatar name={s.name} size={52} square />
        <div className="record-title">
          <h1>{s.name}</h1>
          <div className="row gap wrap">
            <Pill state={s.status} />
            <span className="muted small">{s.running}/{s.total} {tr('containers ·')} {s.services.length} {tr('services')}</span>
            {s.managed ? <Tag tone="accent">ducktainer</Tag> : <Tag>{tr('external')}</Tag>}
          </div>
        </div>
        <div className="record-actions needs-admin">
          <button className="btn btn-primary" disabled={!s.editable} title={s.reason} onClick={() => stackRun(ep, s.name, 'up', 'up')}>
            <Rocket size={14} /> {tr('Deploy')}
          </button>
          {s.running > 0 ? (
            <button className="btn" onClick={() => stackRun(ep, s.name, 'stop', 'stop')}><Square size={14} /> {tr('Stop')}</button>
          ) : (
            s.total > 0 && <button className="btn" onClick={() => stackRun(ep, s.name, 'start', 'start')}><Play size={14} /> {tr('Start')}</button>
          )}
          {s.total > 0 && <button className="btn" onClick={() => stackRun(ep, s.name, 'restart', 'restart')}><RotateCw size={14} /> {tr('Restart')}</button>}
          <button className="btn" disabled={!s.editable} onClick={() => stackRun(ep, s.name, 'redeploy', 'redeploy')} title={s.reason || 'pull + up --force-recreate'}><RefreshCw size={14} /> {tr('Update')}</button>
          <MenuButton items={() => stackActions(ep, s)} className="btn" />
        </div>
      </header>
      <div className="record-body">
        <aside className="record-side">
          <Section title={tr('Properties')}>
            <Props
              items={[
                [tr('Project'), s.name],
                [tr('Origin'), s.managed ? tr('Created with ducktainer') : tr('External (docker compose)')],
                s.managed && [tr('Location'), s.location ? (s.node ? tr('Directory on the node') : tr('Own directory')) : tr('ducktainer store'), () => [{ label: tr('Change location…'), onClick: () => openDialog((close) => <LocationDialog ep={ep} stack={s} onClose={close} />) }]],
                s.working_dir && [tr('Directory'), <span className="mono small">{s.working_dir}</span>, () => [{ label: tr('Copy directory'), onClick: () => copy(s.working_dir!) }]],
                s.location?.env_file && ['.env', <span className="mono small">{s.location.env_file}</span>],
                data.files && [tr('Files'), <span className="mono small">{data.files.files.map((f) => f.name).join(' + ')}</span>, () => data.files!.files.map((f) => ({ label: tr('Copy path of {0}', f.name), onClick: () => copy(f.path) }))],
                ['Editable', s.editable ? tr('Yes') : <span title={s.reason}>{tr('No ·')} {s.reason}</span>],
              ]}
            />
          </Section>
          <Section title={tr('Services · {0}', s.services.length)}>
            <ul className="rel-list">
              {s.containers.map((c) => (
                <li key={c.id} onClick={() => go(ep, 'containers', c.id)} onContextMenu={menu(() => {
                  const fc = full.find((x) => x.Id === c.id)
                  return fc ? containerActions(ep, fc) : []
                })}>
                  <span className={`svc-state tone-${c.state === 'running' ? 'ok' : 'idle'}`} />
                  <span className="grow">{c.service}</span>
                  <span className="muted small">{c.state}</span>
                </li>
              ))}
            </ul>
          </Section>
        </aside>
        <main className="record-main">
          <Tabs
            active={tab}
            onChange={setTab}
            tabs={[
              { id: 'services', label: tr('Containers'), count: s.total },
              { id: 'compose', label: 'Compose' },
              { id: 'logs', label: tr('Combined logs') },
            ]}
          />
          <div className="tab-body">
            {tab === 'services' && (
              <DataTable
                rows={full}
                rowKey={(c) => c.Id}
                onOpen={(c) => go(ep, 'containers', c.Id)}
                rowMenu={(c) => containerActions(ep, c)}
                empty={<Empty icon={<Box size={28} />} title={tr('No containers')}>{tr('The stack is not deployed.')} {s.editable && tr('Press “Deploy”.')}</Empty>}
                columns={[
                  { key: 'svc', label: tr('Service'), render: (c) => <ContainerName c={c} sub /> },
                  { key: 'st', label: tr('Status'), width: 150, render: (c) => <Pill state={c.State} title={c.Status} /> },
                  { key: 'p', label: tr('Ports'), render: (c) => <PortTags ep={ep} c={c} /> },
                  { key: 'status', label: tr('Detail'), render: (c) => <span className="muted small">{humanStatus(c.Status)}</span> },
                  { key: 'm', label: '', width: 36, render: (c) => <MenuButton items={() => containerActions(ep, c)} /> },
                ]}
              />
            )}
            {tab === 'compose' && <ComposeEditor ep={ep} stack={s} files={data.files} filesError={data.files_error} />}
            {tab === 'logs' && (s.total > 0 ? <LogViewer alert={{ ep, container: (svc) => (svc ? `${name}?${svc}?*` : `${name}?*`) }} path={`/e/${enc(ep)}/stacks/${enc(name)}/logs`} multi height="calc(100vh - 290px)" /> : <Empty title={tr('The stack has no containers')} />)}
          </div>
        </main>
      </div>
    </div>
  )
}

function ComposeEditor({ ep, stack, files, filesError }: { ep: string; stack: Stack; files?: StackFiles; filesError?: string }) {
  // polling returns new objects every few seconds: only reset the editor when the files on disk really change
  const sig = JSON.stringify(files?.files ?? [])
  const orig = useMemo(() => Object.fromEntries((files?.files || []).map((f) => [f.path, f.content])), [sig])
  const [contents, setContents] = useState<Record<string, string>>(orig)
  const [env, setEnv] = useState(files?.env ?? '')
  const [active, setActive] = useState(files?.files[0]?.path ?? '')
  const [merged, setMerged] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [pull, setPull] = useState(false)
  const menu = useMenu()
  const [mode, setMode] = usePref<'yaml' | 'visual'>('compose.mode', 'visual')
  const multi = (files?.files.length ?? 0) > 1
  // names defined in the other files of the project (base/override) so the
  // visual editor doesn't flag them as missing
  const knownElsewhere = (path: string): Known => {
    const k: Known = { services: [], volumes: [], networks: [] }
    for (const f of files?.files ?? []) {
      if (f.path === path) continue
      const d = parseCompose(contents[f.path] ?? f.content).doc
      if (!d) continue
      k.services.push(...composeServices(d))
      k.volumes.push(...topNames(d, 'volumes'))
      k.networks.push(...topNames(d, 'networks'))
    }
    return k
  }
  useEffect(() => {
    setContents(orig)
    setEnv(files?.env ?? '')
    if (!files?.files.some((f) => f.path === active) && active !== '.env' && active !== '__merged') setActive(files?.files[0]?.path ?? '')
  }, [sig, files?.env])
  useEffect(() => {
    if (active !== '__merged') return
    setMerged(null)
    api.get(`/e/${enc(ep)}/stacks/${enc(stack.name)}/merged`).then((r) => setMerged(r.compose)).catch((e) => setMerged('# ' + e.message))
  }, [active, ep, stack.name, sig])
  const changed = (files?.files || []).filter((f) => contents[f.path] !== f.content)
  const dirty = !!files && (changed.length > 0 || env !== files.env)
  if (!files)
    return (
      <div>
        <div className="alert">{filesError}</div>
        <p className="muted">
          {tr('You can')} <strong>{tr('adopt it')}</strong> {tr('by pasting its compose: ducktainer will keep a copy and you will be able to edit and redeploy it from here.')}
        </p>
        <button className="btn" onClick={() => openDialog((close) => <AdoptDialog ep={ep} name={stack.name} onClose={close} />)}>
          <FolderOpen size={14} /> {tr('Adopt by pasting the compose')}
        </button>
      </div>
    )
  const save = async (deploy: boolean) => {
    setBusy(true)
    try {
      const edited = Object.fromEntries(files.files.map((f) => [f.path, contents[f.path]]))
      await runJob(`${deploy ? tr('Save and deploy') : tr('Save')} ${stack.name}`, 'PUT', `/e/${enc(ep)}/stacks/${enc(stack.name)}`, { files: edited, env, deploy, pull })
      invalidate(`/stacks`, '/containers')
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  const merge = async () => {
    if (dirty) return toast.info(tr('Save or discard the changes before merging'))
    const base = files.files[0]
    const ok = await confirm({
      title: tr('Merge into a single file'),
      body: tr('The merged result will be written to {0} and {1} will be set aside (.ducktainer-bak copies of every original are kept). {VARIABLES} are kept, but comments and formatting are lost. Containers are not recreated: the resulting configuration is the same.', base.name, files.files.slice(1).map((f) => f.name).join(', ')),
      confirm: tr('Merge'),
    })
    if (!ok) return
    try {
      const r = await api.post(`/e/${enc(ep)}/stacks/${enc(stack.name)}/merge`)
      toast.ok(tr('Merged into {0}', r.file.split('/').pop()))
      setActive(base.path)
      invalidate('/stacks')
    } catch (e) {
      toast.err(e)
    }
  }
  const fileTabs = [
    ...files.files.map((f, i) => ({
      id: f.path,
      label: (
        <>
          {f.name}
          {contents[f.path] !== f.content && <span className="dirty-dot" />}
          {multi && <span className="muted small">{i === 0 ? 'base' : 'override'}</span>}
        </>
      ),
      menu: (): MenuEntry[] => [
        { header: f.name },
        { label: tr('Copy path'), onClick: () => copy(f.path) },
        { label: tr('Copy content'), onClick: () => copy(contents[f.path]) },
        contents[f.path] !== f.content && { label: tr('Discard changes to this file'), onClick: () => setContents({ ...contents, [f.path]: f.content }) },
        multi && 'sep',
        multi && { label: tr('View merged result'), onClick: () => setActive('__merged') },
        multi && !files.managed && { label: tr('Merge into a single file…'), onClick: merge },
      ],
    })),
    { id: '.env', label: <>.env{env !== files.env && <span className="dirty-dot" />}</>, menu: (): MenuEntry[] => [{ label: tr('Copy path'), onClick: () => copy(files.env_path) }] },
    ...(multi ? [{ id: '__merged', label: <><Combine size={13} /> {tr('Merged')}</>, menu: (): MenuEntry[] => [{ label: tr('Copy result'), onClick: () => merged && copy(merged) }, !files.managed && { label: tr('Merge into a single file…'), onClick: merge }] }] : []),
  ]
  const current = files.files.find((f) => f.path === active)
  return (
    <div className="compose-editor">
      {!files.managed && (
        <div className="alert">
          {files.node ? tr('Editing on the node:') : tr('Editing in place:')} <code>{files.dir}</code>
          {multi && <> · {files.files.length} {tr('files that compose merges in order')}</>}
        </div>
      )}
      <div className="file-tabs-row">
        <Tabs active={active} onChange={setActive} tabs={fileTabs} />
        {current && (
          <div className="seg seg-text">
            <button className={mode === 'visual' ? 'on' : ''} onClick={() => setMode('visual')}>Visual</button>
            <button className={mode === 'yaml' ? 'on' : ''} onClick={() => setMode('yaml')}>YAML</button>
          </div>
        )}
        {multi && !files.managed && (
          <button className="btn btn-sm" onClick={merge} title={tr('Merge base + overrides into a single file')}>
            <Combine size={14} /> {tr('Merge files')}
          </button>
        )}
      </div>
      <div className="file-tab-body" onContextMenu={menu(() => [
        current && { label: tr('Copy content'), onClick: () => copy(contents[current.path]) },
        current && contents[current.path] !== current.content && { label: tr('Discard changes to this file'), onClick: () => setContents({ ...contents, [current.path]: current.content }) },
        multi && { label: tr('View merged result'), onClick: () => setActive('__merged') },
      ])}>
        {current && mode === 'yaml' && <CodeEditor value={contents[current.path] ?? ''} onChange={(v) => setContents({ ...contents, [current.path]: v })} minRows={28} />}
        {current && mode === 'visual' && (
          <ComposeVisual key={current.path} text={contents[current.path] ?? ''} onChange={(v) => setContents((c) => ({ ...c, [current.path]: v }))} envText={env} known={knownElsewhere(current.path)} />
        )}
        {active === '.env' && (
          <>
            <p className="help mono">{files.env_path}</p>
            <CodeEditor value={env} onChange={setEnv} minRows={20} placeholder={tr('VARIABLE=value')} />
          </>
        )}
        {active === '__merged' && (
          <>
            <p className="help">{tr('Result of')} <code>docker compose config --no-interpolate</code>{tr(': what compose actually applies when merging the files. Read-only.')}</p>
            {merged === null ? <Spinner /> : <CodeEditor value={merged} readOnly minRows={28} />}
          </>
        )}
      </div>
      <div className={`savebar${dirty ? ' dirty' : ''}`}>
        <span>{dirty ? `${tr('Unsaved changes')} (${[...changed.map((f) => f.name), ...(env !== files.env ? ['.env'] : [])].join(', ')})` : tr('No changes')}</span>
        <label className="check">
          <input type="checkbox" checked={pull} onChange={(e) => setPull(e.target.checked)} /> {tr('pull when deploying')}
        </label>
        <span className="grow" />
        <button className="btn" disabled={!dirty || busy} onClick={() => (setContents(orig), setEnv(files.env))}>
          <Undo2 size={14} /> {tr('Discard')}
        </button>
        <button className="btn" disabled={!dirty || busy} onClick={() => save(false)}>
          <Save size={14} /> {tr('Save')}
        </button>
        <button className="btn btn-primary" disabled={busy} onClick={() => save(true)}>
          <Rocket size={14} /> {tr('Save and deploy')}
        </button>
      </div>
    </div>
  )
}

function AdoptDialog({ ep, name, onClose }: { ep: string; name: string; onClose: () => void }) {
  const [compose, setCompose] = useState('')
  const [env, setEnv] = useState('')
  return (
    <Modal
      wide="xl"
      title={tr('Adopt {0}', name)}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
          <button
            className="btn btn-primary"
            disabled={!compose.trim()}
            onClick={async () => {
              try {
                await api.post(`/e/${enc(ep)}/stacks/${enc(name)}/adopt`, { compose, env })
                toast.ok(tr('{0} adopted', name))
                invalidate('/stacks')
                onClose()
              } catch (e) {
                toast.err(e)
              }
            }}
          >
            {tr('Adopt')}
          </button>
        </>
      }
    >
      <div className="grid-editor">
        <div>
          <div className="label">compose.yaml</div>
          <CodeEditor value={compose} onChange={setCompose} minRows={20} />
        </div>
        <div>
          <div className="label">.env</div>
          <CodeEditor value={env} onChange={setEnv} minRows={20} />
        </div>
      </div>
    </Modal>
  )
}

