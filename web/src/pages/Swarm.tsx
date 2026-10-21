import { t as tr, tc } from '../i18n'
import { useEffect, useMemo, useState } from 'react'
import {
  Boxes, Server, KeyRound, FileText, Layers, Plus, Search, ChevronRight, Rocket, Save, Undo2, Eye, EyeOff, RefreshCw, Copy as CopyIcon,
  LogOut, Scaling, FolderInput, RotateCw, ArrowUpCircle, Trash2, LayoutGrid, LayoutList, Crown, Tags,
} from 'lucide-react'
import { api, enc } from '../lib/api'
import type { Network, Service, ServiceSpec, SwarmInfo, SwarmNode, SwarmObject, SwarmStack, Task } from '../lib/types'
import { useResource, invalidate, peek } from '../lib/store'
import { usePref } from '../lib/prefs'
import { ago, bytes, copy, dateTime, shortId } from '../lib/format'
import { go, href } from '../lib/router'
import { isViewer } from '../lib/session'
import { Avatar, CodeEditor, DataTable, Empty, MenuButton, Pill, Props, Section, Spinner, Tabs, Tag } from '../ui/common'
import { useMenu } from '../ui/ContextMenu'
import type { MenuEntry } from '../ui/ContextMenu'
import { Modal, confirm, openDialog, runJob, toast } from '../ui/overlays'
import {
  drainNode, editNodeLabels, nodeActions, nodeName, publishedPorts, removeServices, scaleService, serviceActions, serviceImage, serviceMode,
  stackOfService, swarmObjectActions, swarmStackActions, swarmStackDeploy, updateServiceImage, parseLabels,
} from '../actions'
import { LogViewer } from '../components/LogViewer'
import { ComposeVisual } from '../components/ComposeVisual'
import { LocationDialog, PlacementFields, placementBody, usePlacement } from './Stacks'
import { JsonView } from './ContainerRecord'

/** The endpoint's swarm state: whether it is a manager we can manage from. */
export function useSwarm(ep: string) {
  const { data } = useResource<SwarmInfo>(`/e/${enc(ep)}/swarm`, { poll: 60000 })
  const state = data?.info.LocalNodeState
  return { data, active: state === 'active', manager: !!data?.info.ControlAvailable }
}

/** Shown instead of a swarm page when the endpoint can't serve it. */
function NeedsManager({ ep, icon, title }: { ep: string; icon: React.ReactNode; title: string }) {
  const { data, active } = useSwarm(ep)
  if (!data) return <div className="page center"><Spinner /></div>
  return (
    <div className="page">
      <Empty icon={icon} title={title} action={<a className="btn btn-primary" href={href(ep, 'swarm')}>{tr('Go to Swarm')}</a>}>
        {active ? tr('This node is a worker: services, nodes and stacks are managed from a manager node. Add a manager as a host.') : tr('Swarm mode is not active on this host.')}
      </Empty>
    </div>
  )
}

function replicasLabel(sv: Service) {
  const st = sv.ServiceStatus
  if (!st) return serviceMode(sv)
  return `${st.RunningTasks}/${st.DesiredTasks}`
}

function replicasTone(sv: Service) {
  const st = sv.ServiceStatus
  if (!st) return 'idle'
  if (st.DesiredTasks === 0) return 'idle'
  return st.RunningTasks >= st.DesiredTasks ? 'running' : st.RunningTasks === 0 ? 'failed' : 'partial'
}

/** A rolling update or rollback is in progress (or stuck paused). */
const updating = (sv: Service) => ['updating', 'paused', 'rollback_started', 'rollback_paused'].includes(sv.UpdateStatus?.State || '')

function Replicas({ sv }: { sv: Service }) {
  const tone = replicasTone(sv)
  return <Pill state={tone} label={replicasLabel(sv)} title={serviceMode(sv)} />
}

function PortList({ sv }: { sv: Service }) {
  const ports = publishedPorts(sv)
  if (!ports.length) return <span className="muted small">—</span>
  return <span className="tags">{ports.map((p) => <Tag key={`${p.PublishedPort}/${p.Protocol}`} mono>{`${p.PublishedPort}→${p.TargetPort}${p.Protocol !== 'tcp' ? '/' + p.Protocol : ''}`}</Tag>)}</span>
}

// ---------- overview ----------

function JoinTokens({ ep }: { ep: string }) {
  const [tokens, setTokens] = useState<{ worker: string; manager: string; addr: string } | null>(null)
  const [show, setShow] = useState(false)
  const load = () => api.get(`/e/${enc(ep)}/swarm/tokens`).then(setTokens).catch(toast.err)
  useEffect(() => {
    if (!isViewer()) load()
  }, [ep])
  if (isViewer()) return null
  if (!tokens) return <Spinner />
  const rotate = async (which: 'worker' | 'manager') => {
    if (!(await confirm({ title: tr('New {0} token', which), body: tr('The current token stops working for new nodes; nodes already joined are not affected.'), confirm: tr('Rotate') }))) return
    api.post(`/e/${enc(ep)}/swarm/tokens/${which}/rotate`).then((t) => (setTokens(t), toast.ok(tr('Token rotated')))).catch(toast.err)
  }
  return (
    <div className="stack-gap">
      {(['worker', 'manager'] as const).map((k) => {
        const cmd = `docker swarm join --token ${tokens[k]} ${tokens.addr}`
        return (
          <div key={k} className="panel">
            <div className="row gap">
              <h4 className="grow">{k === 'worker' ? tr('Add a worker') : tr('Add a manager')}</h4>
              <button className="btn btn-sm" onClick={() => setShow(!show)} title={show ? tr('Hide') : tr('Show')}>{show ? <EyeOff size={14} /> : <Eye size={14} />}</button>
              <button className="btn btn-sm" onClick={() => copy(cmd)}><CopyIcon size={14} /> {tr('Copy command')}</button>
              <button className="btn btn-sm" onClick={() => rotate(k)}><RefreshCw size={14} /> {tr('Rotate')}</button>
            </div>
            <pre className="mono small">{show ? cmd : cmd.replace(tokens[k], tokens[k].slice(0, 14) + '…')}</pre>
          </div>
        )
      })}
      <p className="help">{tr('Run the command on the new node. If it is a host in ducktainer you can also use “Join a swarm” on its Swarm page.')}</p>
    </div>
  )
}

function InitJoin({ ep }: { ep: string }) {
  const [mode, setMode] = useState<'init' | 'join'>('init')
  const [f, setF] = useState({ advertise_addr: '', listen_addr: '', remote: '', token: '', pool: '' })
  const [busy, setBusy] = useState(false)
  const set = (p: Partial<typeof f>) => setF({ ...f, ...p })
  const submit = async () => {
    setBusy(true)
    try {
      if (mode === 'init') {
        await api.post(`/e/${enc(ep)}/swarm/init`, { advertise_addr: f.advertise_addr, listen_addr: f.listen_addr, default_addr_pool: f.pool ? f.pool.split(/[\s,]+/).filter(Boolean) : undefined }, { task: tr('Initialize swarm') })
        toast.ok(tr('Swarm initialized: this host is its first manager'))
      } else {
        await api.post(`/e/${enc(ep)}/swarm/join`, { remote: f.remote, token: f.token, advertise_addr: f.advertise_addr, listen_addr: f.listen_addr }, { task: tr('Join swarm') })
        toast.ok(tr('Joined the swarm'))
      }
      invalidate('/swarm', '/info', '/nodes', '/networks')
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="panel swarm-setup needs-admin">
      <div className="seg seg-text">
        <button className={mode === 'init' ? 'on' : ''} onClick={() => setMode('init')}>{tr('Create a new swarm')}</button>
        <button className={mode === 'join' ? 'on' : ''} onClick={() => setMode('join')}>{tr('Join a swarm')}</button>
      </div>
      {mode === 'join' && (
        <div className="grid-2">
          <label className="field"><span>{tr('Manager address')}</span><input className="mono" value={f.remote} placeholder="10.0.0.5:2377" onChange={(e) => set({ remote: e.target.value })} autoFocus /></label>
          <label className="field"><span>{tr('Join token')}</span><input className="mono" value={f.token} placeholder="SWMTKN-1-…" onChange={(e) => set({ token: e.target.value })} /></label>
        </div>
      )}
      <div className="grid-2">
        <label className="field"><span>{tr('Advertised address')}</span><input className="mono" value={f.advertise_addr} placeholder={tr('auto (e.g. eth0 or 10.0.0.4)')} onChange={(e) => set({ advertise_addr: e.target.value })} /></label>
        <label className="field"><span>{tr('Listen address')}</span><input className="mono" value={f.listen_addr} placeholder="0.0.0.0:2377" onChange={(e) => set({ listen_addr: e.target.value })} /></label>
        {mode === 'init' && <label className="field"><span>{tr('Address pool for overlay networks')}</span><input className="mono" value={f.pool} placeholder="10.0.0.0/8" onChange={(e) => set({ pool: e.target.value })} /></label>}
      </div>
      <p className="help">
        {mode === 'init'
          ? tr('This host becomes the first manager. Other nodes need to reach it on TCP 2377, TCP/UDP 7946 and UDP 4789.')
          : tr('Get the token and address from the Swarm page of a manager (or `docker swarm join-token worker`).')}
      </p>
      <div className="row">
        <span className="grow" />
        <button className="btn btn-primary" disabled={busy || (mode === 'join' && (!f.remote || !f.token))} onClick={submit}>
          {mode === 'init' ? tr('Initialize swarm') : tr('Join')}
        </button>
      </div>
    </div>
  )
}

export function SwarmOverview({ ep }: { ep: string }) {
  const { data, error } = useResource<SwarmInfo>(`/e/${enc(ep)}/swarm`, { poll: 15000 })
  const manager = !!data?.info.ControlAvailable
  const { data: nodes } = useResource<SwarmNode[]>(manager ? `/e/${enc(ep)}/nodes` : null, { poll: 15000 })
  const { data: services } = useResource<Service[]>(manager ? `/e/${enc(ep)}/services` : null, { poll: 15000 })
  const { data: stacks } = useResource<SwarmStack[]>(manager ? `/e/${enc(ep)}/swarm/stacks` : null)
  const menu = useMenu()
  if (error) return <div className="page"><div className="alert alert-bad">{error.message}</div></div>
  if (!data) return <div className="page center"><Spinner /></div>
  const info = data.info
  const leave = async () => {
    const lastManager = manager && info.Managers <= 1
    const ok = await confirm({
      title: tr('Leave the swarm'),
      body: lastManager
        ? tr('This is the last manager: leaving destroys the swarm with all its services, secrets and configs.')
        : manager
          ? tr('A manager must be demoted before leaving, or leave by force (it can break the quorum).')
          : tr('Its tasks are moved to other nodes. The node stays in the list as “down” until removed from a manager.'),
      danger: true,
      confirm: tr('Leave'),
      check: manager ? tr('Force') : undefined,
    })
    if (!ok) return
    api.post(`/e/${enc(ep)}/swarm/leave?force=${ok.checked ? 1 : 0}`, {}, { task: tr('Leave swarm') })
      .then(() => toast.ok(tr('The host left the swarm')))
      .catch(toast.err)
      .finally(() => invalidate('/swarm', '/info', '/nodes', '/services'))
  }
  const ready = nodes?.filter((n) => n.Status.State === 'ready').length ?? 0
  const pageMenu = (): MenuEntry[] => [
    manager && { label: tr('New service…'), icon: <Plus size={14} />, onClick: () => openDialog((c) => <NewServiceDialog ep={ep} onClose={c} />) },
    manager && { label: tr('New swarm stack…'), icon: <Layers size={14} />, onClick: () => openDialog((c) => <NewSwarmStackDialog ep={ep} onClose={c} />) },
    { label: tr('Reload'), onClick: () => invalidate('/swarm', '/nodes', '/services') },
    info.LocalNodeState !== 'inactive' && 'sep',
    info.LocalNodeState !== 'inactive' && { label: tr('Leave the swarm…'), icon: <LogOut size={14} />, danger: true, onClick: leave },
  ]
  return (
    <div className="page" onContextMenu={(e) => (e.target as HTMLElement).closest('.page-body') && menu(pageMenu)(e)}>
      <div className="page-head">
        <div className="page-title">
          <Boxes size={20} />
          <h1>Swarm</h1>
          <Pill state={info.LocalNodeState === 'active' ? 'running' : info.LocalNodeState === 'error' ? 'failed' : 'stopped'} label={info.LocalNodeState === 'active' ? (manager ? 'manager' : 'worker') : info.LocalNodeState} />
        </div>
        <div className="page-actions needs-admin">
          {manager && <button className="btn" onClick={() => openDialog((c) => <NewSwarmStackDialog ep={ep} onClose={c} />)}><Layers size={15} /> {tr('New stack')}</button>}
          {manager && <button className="btn btn-primary" onClick={() => openDialog((c) => <NewServiceDialog ep={ep} onClose={c} />)}><Plus size={15} /> {tr('New service')}</button>}
          {info.LocalNodeState !== 'inactive' && <MenuButton items={pageMenu} className="btn" />}
        </div>
      </div>
      <div className="page-body">
        {info.Error && <div className="alert alert-bad">{info.Error}</div>}
        {info.LocalNodeState === 'inactive' && (
          <>
            <p className="muted">{tr('Swarm mode turns several Docker hosts into one cluster: services with replicas spread across nodes, rolling updates, overlay networks, secrets and configs, and stacks deployed with docker stack deploy.')}</p>
            <InitJoin ep={ep} />
          </>
        )}
        {info.LocalNodeState === 'active' && !manager && (
          <div className="alert">
            {tr('This host is a worker node: it runs tasks but the cluster is managed from a manager.')}
            {info.RemoteManagers?.length ? <> {tr('Managers:')} <code>{info.RemoteManagers.map((m) => m.Addr).join(', ')}</code></> : null}
          </div>
        )}
        {manager && (
          <>
            <div className="kpis">
              <a className="kpi" href={href(ep, 'nodes')}><span className="kpi-icon"><Server size={16} /></span><span className="kpi-label">{tr('Nodes')}</span><span className="kpi-value">{nodes ? <>{ready}<small>/{nodes.length}</small></> : info.Nodes}</span><span className="kpi-sub">{tr('{0} managers', info.Managers)}</span></a>
              <a className="kpi" href={href(ep, 'services')}><span className="kpi-icon"><Boxes size={16} /></span><span className="kpi-label">{tr('Services')}</span><span className="kpi-value">{services?.length ?? '…'}</span><span className="kpi-sub">{services ? tr('{0} tasks running', services.reduce((a, x) => a + (x.ServiceStatus?.RunningTasks || 0), 0)) : ''}</span></a>
              <a className="kpi" href={href(ep, 'swarm-stacks')}><span className="kpi-icon"><Layers size={16} /></span><span className="kpi-label">Stacks</span><span className="kpi-value">{stacks?.length ?? '…'}</span><span className="kpi-sub">docker stack</span></a>
            </div>
            <div className="overview-grid">
              <Section title={tr('Cluster')}>
                <Props items={[
                  ['ID', <span className="mono small">{shortId(data.cluster?.ID || '')}</span>, () => [{ label: tr('Copy ID'), onClick: () => copy(data.cluster?.ID || '') }]],
                  [tr('This node'), <a href={href(ep, 'nodes', info.NodeID)}>{info.NodeAddr}</a>],
                  [tc('f', 'Created'), dateTime(data.cluster?.CreatedAt)],
                  data.cluster?.DefaultAddrPool && [tr('Address pool'), <span className="mono small">{data.cluster.DefaultAddrPool.join(', ')} /{data.cluster.SubnetSize}</span>],
                  data.cluster && [tr('Data port (VXLAN)'), data.cluster.DataPathPort],
                  data.cluster?.Spec.Orchestration && [tr('Task history'), data.cluster.Spec.Orchestration.TaskHistoryRetentionLimit],
                  [tr('Autolock'), data.cluster?.Spec.EncryptionConfig?.AutoLockManagers ? tr('Yes') : tr('No')],
                ]} />
              </Section>
              <Section title={tr('Add nodes')}>
                <JoinTokens ep={ep} />
              </Section>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// ---------- services ----------

const overlayNets = (nets?: Network[]) => (nets || []).filter((n) => n.Scope === 'swarm' && n.Driver === 'overlay' && n.Name !== 'ingress')

function lines(text: string) {
  return text.split('\n').map((l) => l.trim()).filter(Boolean)
}

/** "8080:80", "8080:80/udp", "53:53/udp@host" → port configs. */
export function parsePorts(text: string) {
  return lines(text).map((l) => {
    const [main, mode] = l.split('@')
    const [pp, proto = 'tcp'] = main.split('/')
    const [pub, target] = pp.includes(':') ? pp.split(':') : ['', pp]
    return { Protocol: proto as 'tcp', TargetPort: Number(target), PublishedPort: pub ? Number(pub) : undefined, PublishMode: (mode === 'host' ? 'host' : 'ingress') as 'host' | 'ingress' }
  })
}

export function NewServiceDialog({ ep, onClose }: { ep: string; onClose: () => void }) {
  const { data: nets } = useResource<Network[]>(`/e/${enc(ep)}/networks`)
  const { data: secrets } = useResource<SwarmObject[]>(`/e/${enc(ep)}/secrets`)
  const { data: configs } = useResource<SwarmObject[]>(`/e/${enc(ep)}/configs`)
  const [f, setF] = useState({
    name: '', image: '', mode: 'replicated', replicas: 1, ports: '', env: '', command: '', constraints: '', restart: 'any',
    networks: [] as string[], secrets: [] as string[], configs: [] as string[], cpus: '', memory: '', labels: '',
  })
  const [busy, setBusy] = useState(false)
  const set = (p: Partial<typeof f>) => setF({ ...f, ...p })
  const toggle = (k: 'networks' | 'secrets' | 'configs', v: string) => set({ [k]: f[k].includes(v) ? f[k].filter((x) => x !== v) : [...f[k], v] } as any)
  const submit = async () => {
    const file = (name: string) => ({ Name: name, UID: '0', GID: '0', Mode: 0o444 })
    const spec: ServiceSpec = {
      Name: f.name,
      Labels: f.labels ? parseLabels(f.labels) : undefined,
      TaskTemplate: {
        ContainerSpec: {
          Image: f.image,
          Args: f.command ? (f.command.match(/(?:[^\s"']+|"[^"]*"|'[^']*')+/g) || []).map((a) => a.replace(/^["']|["']$/g, '')) : undefined,
          Env: f.env ? lines(f.env) : undefined,
          Secrets: f.secrets.length ? f.secrets.map((id) => { const s = secrets!.find((x) => x.ID === id)!; return { SecretID: id, SecretName: s.Spec.Name, File: file(s.Spec.Name) } }) : undefined,
          Configs: f.configs.length ? f.configs.map((id) => { const c = configs!.find((x) => x.ID === id)!; return { ConfigID: id, ConfigName: c.Spec.Name, File: file('/' + c.Spec.Name) } }) : undefined,
        },
        Placement: f.constraints ? { Constraints: lines(f.constraints) } : undefined,
        RestartPolicy: { Condition: f.restart as 'any' },
        Resources: f.cpus || f.memory ? { Limits: { NanoCPUs: f.cpus ? Math.round(Number(f.cpus) * 1e9) : undefined, MemoryBytes: f.memory ? Math.round(Number(f.memory) * 1024 * 1024) : undefined } } : undefined,
        Networks: f.networks.length ? f.networks.map((n) => ({ Target: n })) : undefined,
      },
      Mode: f.mode === 'global' ? { Global: {} } : { Replicated: { Replicas: Number(f.replicas) } },
      EndpointSpec: f.ports ? { Ports: parsePorts(f.ports) } : undefined,
    }
    setBusy(true)
    try {
      const r = await api.post(`/e/${enc(ep)}/services`, spec, { task: tr('Create service {0}', f.name) })
      for (const w of r?.Warnings || []) toast.info(w)
      toast.ok(tr('Service {0} created', f.name))
      invalidate('/services', '/swarm')
      onClose()
      go(ep, 'services', r.ID)
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  const chips = (k: 'networks' | 'secrets' | 'configs', items: { id: string; label: string }[], none: string) => (
    <div className="tags">
      {items.length === 0 && <span className="muted small">{none}</span>}
      {items.map((i) => <Tag key={i.id} tone={f[k].includes(i.id) ? 'accent' : undefined} onClick={() => toggle(k, i.id)}>{i.label}</Tag>)}
    </div>
  )
  return (
    <Modal
      wide
      title={tr('New service')}
      icon={<Boxes size={18} />}
      onClose={onClose}
      footer={<><span className="grow" /><button className="btn" onClick={onClose}>{tr('Cancel')}</button><button className="btn btn-primary" disabled={busy || !f.name || !f.image} onClick={submit}><Rocket size={14} /> {tr('Create service')}</button></>}
    >
      <div className="grid-2">
        <label className="field"><span>{tr('Name')}</span><input value={f.name} placeholder="web" onChange={(e) => set({ name: e.target.value.replace(/[^a-zA-Z0-9_.-]/g, '-') })} autoFocus /></label>
        <label className="field"><span>{tr('Image')}</span><input className="mono" value={f.image} placeholder="nginx:alpine" onChange={(e) => set({ image: e.target.value })} /></label>
        <label className="field"><span>{tr('Mode')}</span>
          <select value={f.mode} onChange={(e) => set({ mode: e.target.value })}>
            <option value="replicated">{tr('Replicated')}</option>
            <option value="global">{tr('Global (one task per node)')}</option>
          </select>
        </label>
        {f.mode === 'replicated' && <label className="field"><span>{tr('Replicas')}</span><input type="number" min={0} value={f.replicas} onChange={(e) => set({ replicas: Number(e.target.value) })} /></label>}
        <label className="field"><span>{tr('Ports (published:target[/udp][@host])')}</span><textarea className="mono" rows={3} value={f.ports} placeholder={'8080:80\n53:53/udp'} onChange={(e) => set({ ports: e.target.value })} /></label>
        <label className="field"><span>{tr('Environment (KEY=value per line)')}</span><textarea className="mono" rows={3} value={f.env} onChange={(e) => set({ env: e.target.value })} /></label>
        <label className="field"><span>{tr('Command (optional)')}</span><input className="mono" value={f.command} placeholder={tr('image default')} onChange={(e) => set({ command: e.target.value })} /></label>
        <label className="field"><span>{tr('Restart')}</span>
          <select value={f.restart} onChange={(e) => set({ restart: e.target.value })}>
            <option value="any">{tr('always (any)')}</option>
            <option value="on-failure">on-failure</option>
            <option value="none">{tr('never')}</option>
          </select>
        </label>
        <label className="field"><span>{tr('Placement constraints (one per line)')}</span><textarea className="mono" rows={2} value={f.constraints} placeholder={'node.role==worker\nnode.labels.zone==a'} onChange={(e) => set({ constraints: e.target.value })} /></label>
        <label className="field"><span>{tr('Labels')}</span><input className="mono" value={f.labels} placeholder="key=value, key2=value2" onChange={(e) => set({ labels: e.target.value })} /></label>
        <label className="field"><span>{tr('CPU limit')}</span><input className="mono" value={f.cpus} placeholder="0.5" onChange={(e) => set({ cpus: e.target.value })} /></label>
        <label className="field"><span>{tr('Memory limit (MiB)')}</span><input className="mono" value={f.memory} placeholder="256" onChange={(e) => set({ memory: e.target.value })} /></label>
      </div>
      <div className="field"><span>{tr('Overlay networks')}</span>{chips('networks', overlayNets(nets).map((n) => ({ id: n.Id, label: n.Name })), tr('No overlay networks: create one in Networks with the overlay driver.'))}</div>
      <div className="field"><span>Secrets</span>{chips('secrets', (secrets || []).map((s) => ({ id: s.ID, label: s.Spec.Name })), tr('No secrets'))}</div>
      <div className="field"><span>Configs</span>{chips('configs', (configs || []).map((s) => ({ id: s.ID, label: s.Spec.Name })), tr('No configs'))}</div>
      <p className="help">{tr('Secrets are mounted in /run/secrets/<name> and configs in /<name>. Everything else can be changed later in the service\'s configuration.')}</p>
    </Modal>
  )
}

export function Services({ ep }: { ep: string }) {
  const { manager } = useSwarm(ep)
  const { data, error } = useResource<Service[]>(manager ? `/e/${enc(ep)}/services` : null, { poll: 10000 })
  useResource<SwarmStack[]>(manager ? `/e/${enc(ep)}/swarm/stacks` : null)
  const [q, setQ] = useState('')
  const [group, setGroup] = usePref('services.group', true)
  const [sel, setSel] = useState<Set<string>>(new Set())
  const menu = useMenu()
  if (!manager) return <NeedsManager ep={ep} icon={<Boxes size={28} />} title={tr('Services')} />
  const all = data || []
  const rows = all.filter((s) => !q || `${s.Spec.Name} ${serviceImage(s)} ${stackOfService(s)}`.toLowerCase().includes(q.toLowerCase()))
  const newService = () => openDialog((c) => <NewServiceDialog ep={ep} onClose={c} />)
  return (
    <div className="page" onContextMenu={(e) => (e.target as HTMLElement).closest('.page-body') && menu(() => [
      { label: tr('New service…'), icon: <Plus size={14} />, onClick: newService },
      { label: tr('Group by stack'), checked: group, onClick: () => setGroup(!group) },
      { label: tr('Reload'), onClick: () => invalidate('/services') },
    ])(e)}>
      <div className="page-head">
        <div className="page-title"><Boxes size={20} /><h1>{tr('Services')}</h1><span className="count">{all.length}</span></div>
        <div className="page-actions">
          <div className="search"><Search size={14} /><input placeholder={tr('Service, image or stack…')} value={q} onChange={(e) => setQ(e.target.value)} /></div>
          <button className={`btn btn-sm${group ? ' on' : ''}`} onClick={() => setGroup(!group)}>{tr('Group by stack')}</button>
          <button className="btn btn-primary needs-admin" onClick={newService}><Plus size={15} /> {tr('New service')}</button>
        </div>
      </div>
      {sel.size > 0 && (
        <div className="bulkbar">
          <strong>{sel.size} {tr('selected')}</strong>
          <button className="btn btn-sm btn-danger-ghost" onClick={() => removeServices(ep, all.filter((s) => sel.has(s.ID))).then(() => setSel(new Set()))}><Trash2 size={14} /> {tr('Delete')}</button>
          <span className="grow" />
          <button className="btn btn-sm btn-ghost" onClick={() => setSel(new Set())}>{tr('Clear selection')}</button>
        </div>
      )}
      <div className="page-body">
        {error && <div className="alert alert-bad">{error.message}</div>}
        {!data && !error && <div className="center"><Spinner /></div>}
        {data && (
          <DataTable
            rows={rows}
            rowKey={(s) => s.ID}
            selected={sel}
            setSelected={setSel}
            onOpen={(s) => go(ep, 'services', s.ID)}
            rowMenu={(s) => serviceActions(ep, s)}
            bulkMenu={(list) => [{ label: tr('Delete {0} services…', list.length), icon: <Trash2 size={14} />, danger: true, onClick: () => removeServices(ep, list) }]}
            groupBy={group ? (s) => stackOfService(s) || '' : undefined}
            groupHeader={(g, list) => (g ? <><Layers size={14} /> <a href={href(ep, 'swarm-stacks', g)}>{g}</a> <span className="muted small">{list.length}</span></> : <span className="muted">{tr('Standalone services')}</span>)}
            groupMenu={(g, list) => {
              const st = peek<SwarmStack[]>(`/e/${enc(ep)}/swarm/stacks`)?.find((x) => x.name === g)
              return st ? swarmStackActions(ep, st) : [{ label: tr('Delete {0} services…', list.length), icon: <Trash2 size={14} />, danger: true, onClick: () => removeServices(ep, list) }]
            }}
            defaultSort={{ key: 'name', dir: 1 }}
            empty={<Empty icon={<Boxes size={28} />} title={tr('No services')} action={<button className="btn btn-primary needs-admin" onClick={newService}><Plus size={15} /> {tr('New service')}</button>}>{tr('Create one here or deploy a stack.')}</Empty>}
            columns={[
              { key: 'name', label: tr('Service'), sort: (s) => s.Spec.Name, render: (s) => <span className="cell-name"><Avatar name={s.Spec.Name.replace(stackOfService(s) + '_', '')} size={24} square /><strong>{s.Spec.Name}</strong></span> },
              { key: 'rep', label: tr('Replicas'), width: 120, sort: (s) => (s.ServiceStatus?.RunningTasks ?? 0) - (s.ServiceStatus?.DesiredTasks ?? 0), render: (s) => <Replicas sv={s} /> },
              { key: 'mode', label: tr('Mode'), width: 110, render: (s) => <span className="muted small">{serviceMode(s)}</span> },
              { key: 'img', label: tr('Image'), sort: (s) => serviceImage(s), render: (s) => <span className="mono small ellipsis">{serviceImage(s)}</span> },
              { key: 'ports', label: tr('Ports'), render: (s) => <PortList sv={s} /> },
              { key: 'upd', label: tr('Updated'), width: 120, sort: (s) => -Date.parse(s.UpdatedAt), render: (s) => <span className="muted small" title={dateTime(s.UpdatedAt)}>{updating(s) ? <Tag tone="warn" title={s.UpdateStatus?.Message}>{s.UpdateStatus!.State}</Tag> : ago(s.UpdatedAt)}</span> },
              { key: 'm', label: '', width: 36, render: (s) => <MenuButton items={() => serviceActions(ep, s)} /> },
            ]}
          />
        )}
      </div>
    </div>
  )
}

function TaskTable({ ep, tasks, show }: { ep: string; tasks: Task[]; show: 'service' | 'node' }) {
  const [history, setHistory] = usePref('tasks.history', false)
  const rows = history ? tasks : tasks.filter((t) => t.desired_state === 'running' || t.state === 'running')
  return (
    <>
      <div className="row gap">
        <span className="grow muted small">{tr('{0} tasks', rows.length)}</span>
        <label className="check"><input type="checkbox" checked={history} onChange={(e) => setHistory(e.target.checked)} /> {tr('Show finished tasks')}</label>
      </div>
      <DataTable
        rows={rows}
        rowKey={(t) => t.id}
        onOpen={(t) => (show === 'service' ? go(ep, 'nodes', t.node_id) : go(ep, 'services', t.service_id))}
        rowMenu={(t) => [
          { header: `${t.service}.${t.slot || t.node}` },
          { label: tr('Go to node {0}', t.node || shortId(t.node_id)), icon: <Server size={14} />, disabled: !t.node_id, onClick: () => go(ep, 'nodes', t.node_id) },
          { label: tr('Go to service'), icon: <Boxes size={14} />, onClick: () => go(ep, 'services', t.service_id) },
          { label: tr('Copy task ID'), onClick: () => copy(t.id) },
          t.container_id && { label: tr('Copy container ID'), onClick: () => copy(t.container_id!) },
        ]}
        empty={<Empty title={tr('No tasks')} />}
        columns={[
          { key: 'n', label: show === 'service' ? tr('Task') : tr('Service'), render: (t) => <strong className="small">{show === 'service' ? `${t.service}.${t.slot || shortId(t.node_id)}` : t.service}</strong> },
          { key: 's', label: tr('Status'), width: 140, render: (t) => <Pill state={t.state} title={t.error || t.message} /> },
          { key: 'd', label: tr('Desired'), width: 100, render: (t) => <span className="muted small">{t.desired_state}</span> },
          show === 'service'
            ? { key: 'node', label: tr('Node'), width: 160, render: (t) => <a href={href(ep, 'nodes', t.node_id)} onClick={(e) => e.stopPropagation()}>{t.node || shortId(t.node_id)}</a> }
            : { key: 'img', label: tr('Image'), render: (t) => <span className="mono small ellipsis">{t.image.split('@')[0]}</span> },
          { key: 'msg', label: tr('Detail'), render: (t) => <span className={`small ${t.error ? 'text-bad' : 'muted'}`}>{t.error || t.message}</span> },
          { key: 'u', label: tr('Updated'), width: 110, sort: (t) => -Date.parse(t.updated), render: (t) => <span className="muted small" title={dateTime(t.updated)}>{ago(t.updated)}</span> },
        ]}
      />
    </>
  )
}

function SpecEditor({ ep, sv }: { ep: string; sv: Service }) {
  const orig = useMemo(() => JSON.stringify(sv.Spec, null, 2), [sv.ID, sv.UpdatedAt])
  const [text, setText] = useState(orig)
  const [busy, setBusy] = useState(false)
  useEffect(() => setText(orig), [orig])
  const dirty = text !== orig
  const save = async () => {
    let spec
    try {
      spec = JSON.parse(text)
    } catch (e: any) {
      return toast.err(tr('Invalid JSON: {0}', e.message))
    }
    setBusy(true)
    try {
      const r = await api.put(`/e/${enc(ep)}/services/${enc(sv.ID)}`, spec, { task: tr('Update {0}', sv.Spec.Name) })
      for (const w of r?.warnings || []) toast.info(w)
      toast.ok(tr('{0} updated', sv.Spec.Name))
      invalidate('/services')
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="compose-editor">
      <p className="help">{tr('The full service spec as the Engine API takes it (ServiceSpec). Saving starts a rolling update with the service\'s update policy.')}</p>
      <CodeEditor value={text} onChange={setText} minRows={28} />
      <div className={`savebar${dirty ? ' dirty' : ''}`}>
        <span>{dirty ? tr('Unsaved changes') : tr('No changes')}</span>
        <span className="grow" />
        <button className="btn" disabled={!dirty || busy} onClick={() => setText(orig)}><Undo2 size={14} /> {tr('Discard')}</button>
        <button className="btn btn-primary" disabled={!dirty || busy} onClick={save}><Save size={14} /> {tr('Save and update')}</button>
      </div>
    </div>
  )
}

export function ServiceRecord({ ep, id, tab = 'tasks' }: { ep: string; id: string; tab?: string }) {
  const { data, error } = useResource<{ service: Service; tasks: Task[] }>(`/e/${enc(ep)}/services/${enc(id)}`, { poll: 5000 })
  const { data: nets } = useResource<Network[]>(`/e/${enc(ep)}/networks`)
  const menu = useMenu()
  if (error) return <div className="page"><Empty icon={<Boxes size={28} />} title={tr('Service not found')}>{error.message}</Empty></div>
  if (!data) return <div className="page center"><Spinner /></div>
  const sv = data.service
  const cs = sv.Spec.TaskTemplate.ContainerSpec
  const stack = stackOfService(sv)
  const setTab = (t: string) => go(ep, 'services', id, t === 'tasks' ? undefined : t)
  const netName = (nid: string) => nets?.find((n) => n.Id === nid || n.Name === nid)?.Name || shortId(nid)
  const running = data.tasks.filter((t) => t.state === 'running').length
  return (
    <div className="page record">
      <div className="breadcrumbs">
        <a href={href(ep, 'services')}>{tr('Services')}</a><ChevronRight size={13} />
        {stack && <><a href={href(ep, 'swarm-stacks', stack)}>{stack}</a><ChevronRight size={13} /></>}
        <span>{sv.Spec.Name}</span>
      </div>
      <header className="record-head" onContextMenu={menu(() => serviceActions(ep, sv))}>
        <Avatar name={sv.Spec.Name.replace(stack + '_', '')} size={52} square />
        <div className="record-title">
          <h1>{sv.Spec.Name}</h1>
          <div className="row gap wrap">
            <Replicas sv={sv} />
            <span className="muted small">{serviceMode(sv)} · {serviceImage(sv)}</span>
            {stack && <Tag tone="stack">{stack}</Tag>}
            {sv.UpdateStatus && <Tag tone={updating(sv) ? 'warn' : undefined} title={sv.UpdateStatus.Message}>update: {sv.UpdateStatus.State}</Tag>}
          </div>
        </div>
        <div className="record-actions needs-admin">
          {sv.Spec.Mode.Replicated && <button className="btn" onClick={() => scaleService(ep, sv)}><Scaling size={14} /> {tr('Scale')}</button>}
          <button className="btn" onClick={() => updateServiceImage(ep, sv)}><ArrowUpCircle size={14} /> {tr('Image')}</button>
          <button className="btn" onClick={() => api.post(`/e/${enc(ep)}/services/${enc(sv.ID)}/action/restart`).then(() => (toast.ok(tr('{0}: restarting tasks', sv.Spec.Name)), invalidate('/services'))).catch(toast.err)}><RotateCw size={14} /> {tr('Restart')}</button>
          <MenuButton items={() => serviceActions(ep, sv)} className="btn" />
        </div>
      </header>
      <div className="record-body">
        <aside className="record-side">
          <Section title={tr('Properties')}>
            <Props items={[
              ['ID', <span className="mono small">{shortId(sv.ID)}</span>, () => [{ label: tr('Copy ID'), onClick: () => copy(sv.ID) }]],
              [tr('Image'), <span className="mono small">{serviceImage(sv)}</span>, () => [{ label: tr('Copy'), onClick: () => copy(cs.Image) }]],
              [tr('Mode'), sv.Spec.Mode.Replicated ? tr('{0} replicas', sv.Spec.Mode.Replicated.Replicas) : serviceMode(sv)],
              [tr('Tasks'), `${running} ${tr('running')}`],
              publishedPorts(sv).length > 0 && [tr('Ports'), <PortList sv={sv} />],
              cs.Args && ['Args', <span className="mono small">{cs.Args.join(' ')}</span>],
              sv.Spec.TaskTemplate.Networks?.length && [tr('Networks'), <span className="tags">{sv.Spec.TaskTemplate.Networks.map((n) => <Tag key={n.Target} onClick={() => go(ep, 'networks', n.Target)}>{netName(n.Target)}</Tag>)}</span>],
              sv.Spec.TaskTemplate.Placement?.Constraints?.length && [tr('Constraints'), <span className="mono small">{sv.Spec.TaskTemplate.Placement.Constraints.join(', ')}</span>],
              cs.Secrets?.length && ['Secrets', <span className="tags">{cs.Secrets.map((s) => <Tag key={s.SecretID} onClick={() => go(ep, 'secrets', s.SecretID)}>{s.SecretName}</Tag>)}</span>],
              cs.Configs?.length && ['Configs', <span className="tags">{cs.Configs.map((s) => <Tag key={s.ConfigID} onClick={() => go(ep, 'configs', s.ConfigID)}>{s.ConfigName}</Tag>)}</span>],
              sv.Spec.TaskTemplate.RestartPolicy?.Condition && [tr('Restart'), sv.Spec.TaskTemplate.RestartPolicy.Condition],
              [tc('f', 'Created'), dateTime(sv.CreatedAt)],
              [tr('Updated'), dateTime(sv.UpdatedAt)],
            ]} />
          </Section>
          {cs.Env?.length ? (
            <Section title={tr('Environment')}><pre className="mono small">{cs.Env.join('\n')}</pre></Section>
          ) : null}
        </aside>
        <main className="record-main">
          <Tabs active={tab} onChange={setTab} tabs={[
            { id: 'tasks', label: tr('Tasks'), count: running },
            { id: 'logs', label: tr('Logs') },
            { id: 'spec', label: tr('Configuration') },
            { id: 'inspect', label: 'Inspect' },
          ]} />
          <div className="tab-body">
            {tab === 'tasks' && <TaskTable ep={ep} tasks={data.tasks} show="service" />}
            {tab === 'logs' && <LogViewer alert={{ ep, container: () => `${sv.Spec.Name}.*` }} path={`/e/${enc(ep)}/services/${enc(sv.ID)}/logs`} height="calc(100vh - 290px)" />}
            {tab === 'spec' && (isViewer() ? <JsonView value={sv.Spec} /> : <SpecEditor ep={ep} sv={sv} />)}
            {tab === 'inspect' && <JsonView value={sv} />}
          </div>
        </main>
      </div>
    </div>
  )
}

// ---------- nodes ----------

export function Nodes({ ep }: { ep: string }) {
  const { manager } = useSwarm(ep)
  const { data, error } = useResource<SwarmNode[]>(manager ? `/e/${enc(ep)}/nodes` : null, { poll: 10000 })
  const [q, setQ] = useState('')
  const menu = useMenu()
  if (!manager) return <NeedsManager ep={ep} icon={<Server size={28} />} title={tr('Nodes')} />
  const all = data || []
  const rows = all.filter((n) => !q || `${nodeName(n)} ${n.Status.Addr} ${Object.entries(n.Spec.Labels || {}).flat().join(' ')}`.toLowerCase().includes(q.toLowerCase()))
  return (
    <div className="page" onContextMenu={(e) => (e.target as HTMLElement).closest('.page-body') && menu(() => [
      { label: tr('Add nodes…'), icon: <Plus size={14} />, onClick: () => go(ep, 'swarm') },
      { label: tr('Reload'), onClick: () => invalidate('/nodes') },
    ])(e)}>
      <div className="page-head">
        <div className="page-title"><Server size={20} /><h1>{tr('Nodes')}</h1><span className="count">{all.length}</span></div>
        <div className="page-actions">
          <div className="search"><Search size={14} /><input placeholder={tr('Node, address or label…')} value={q} onChange={(e) => setQ(e.target.value)} /></div>
          <a className="btn btn-primary needs-admin" href={href(ep, 'swarm')}><Plus size={15} /> {tr('Add nodes')}</a>
        </div>
      </div>
      <div className="page-body">
        {error && <div className="alert alert-bad">{error.message}</div>}
        {!data && !error && <div className="center"><Spinner /></div>}
        {data && (
          <DataTable
            rows={rows}
            rowKey={(n) => n.ID}
            onOpen={(n) => go(ep, 'nodes', n.ID)}
            rowMenu={(n) => nodeActions(ep, n)}
            defaultSort={{ key: 'name', dir: 1 }}
            columns={[
              { key: 'name', label: tr('Node'), sort: (n) => nodeName(n), render: (n) => <span className="cell-name"><Avatar name={nodeName(n)} size={24} square /><strong>{nodeName(n)}</strong>{n.ManagerStatus?.Leader && <span title="leader"><Crown size={13} className="muted" /></span>}</span> },
              { key: 'role', label: tr('Role'), width: 100, sort: (n) => n.Spec.Role, render: (n) => <Tag tone={n.Spec.Role === 'manager' ? 'accent' : undefined}>{n.Spec.Role}</Tag> },
              { key: 'state', label: tr('Status'), width: 130, sort: (n) => n.Status.State, render: (n) => <Pill state={n.Status.State} title={n.Status.Message} /> },
              { key: 'avail', label: tr('Availability'), width: 130, sort: (n) => n.Spec.Availability, render: (n) => <Pill state={n.Spec.Availability} /> },
              { key: 'addr', label: tr('Address'), width: 140, render: (n) => <span className="mono small">{n.Status.Addr}</span> },
              { key: 'tasks', label: tr('Tasks'), width: 80, className: 'num', sort: (n) => -n.RunningTasks, render: (n) => n.RunningTasks },
              { key: 'res', label: tr('Resources'), width: 150, render: (n) => <span className="muted small">{(n.Description.Resources.NanoCPUs / 1e9).toFixed(0)} CPU · {bytes(n.Description.Resources.MemoryBytes, 0)}</span> },
              { key: 'eng', label: 'Engine', width: 90, render: (n) => <span className="muted small">{n.Description.Engine.EngineVersion}</span> },
              { key: 'labels', label: 'Labels', render: (n) => <span className="tags">{Object.entries(n.Spec.Labels || {}).map(([k, v]) => <Tag key={k} mono>{`${k}=${v}`}</Tag>)}</span> },
              { key: 'm', label: '', width: 36, render: (n) => <MenuButton items={() => nodeActions(ep, n)} /> },
            ]}
          />
        )}
      </div>
    </div>
  )
}

export function NodeRecord({ ep, id }: { ep: string; id: string }) {
  const { data, error } = useResource<{ node: SwarmNode; tasks: Task[] }>(`/e/${enc(ep)}/nodes/${enc(id)}`, { poll: 8000 })
  const { data: list } = useResource<SwarmNode[]>(`/e/${enc(ep)}/nodes`)
  const [tab, setTab] = useState('tasks')
  const menu = useMenu()
  if (error) return <div className="page"><Empty icon={<Server size={28} />} title={tr('Node not found')}>{error.message}</Empty></div>
  if (!data) return <div className="page center"><Spinner /></div>
  const n: SwarmNode = { ...data.node, RunningTasks: list?.find((x) => x.ID === data.node.ID)?.RunningTasks ?? data.tasks.filter((t) => t.state === 'running').length }
  const d = n.Description
  return (
    <div className="page record">
      <div className="breadcrumbs"><a href={href(ep, 'nodes')}>{tr('Nodes')}</a><ChevronRight size={13} /><span>{nodeName(n)}</span></div>
      <header className="record-head" onContextMenu={menu(() => nodeActions(ep, n))}>
        <Avatar name={nodeName(n)} size={52} square />
        <div className="record-title">
          <h1>{nodeName(n)}</h1>
          <div className="row gap wrap">
            <Pill state={n.Status.State} />
            <Pill state={n.Spec.Availability} />
            <Tag tone={n.Spec.Role === 'manager' ? 'accent' : undefined}>{n.Spec.Role}{n.ManagerStatus?.Leader ? ' · leader' : ''}</Tag>
            <span className="muted small">{n.Status.Addr} · {d.Platform.OS}/{d.Platform.Architecture}</span>
          </div>
        </div>
        <div className="record-actions needs-admin">
          {n.Spec.Availability !== 'drain' && <button className="btn" onClick={() => drainNode(ep, n)}>{tr('Drain')}</button>}
          {n.Spec.Availability !== 'active' && <button className="btn" onClick={() => api.post(`/e/${enc(ep)}/nodes/${enc(n.ID)}/update`, { availability: 'active' }).then(() => invalidate('/nodes')).catch(toast.err)}>{tr('Activate')}</button>}
          <button className="btn" onClick={() => editNodeLabels(ep, n)}><Tags size={14} /> Labels</button>
          <MenuButton items={() => nodeActions(ep, n)} className="btn" />
        </div>
      </header>
      <div className="record-body">
        <aside className="record-side">
          <Section title={tr('Properties')}>
            <Props items={[
              ['ID', <span className="mono small">{shortId(n.ID)}</span>, () => [{ label: tr('Copy ID'), onClick: () => copy(n.ID) }]],
              [tr('Hostname'), d.Hostname],
              [tr('Address'), <span className="mono small">{n.Status.Addr}</span>],
              n.ManagerStatus && [tr('Manager'), `${n.ManagerStatus.Reachability} · ${n.ManagerStatus.Addr}`],
              ['CPU', (d.Resources.NanoCPUs / 1e9).toFixed(1)],
              [tr('Memory'), bytes(d.Resources.MemoryBytes)],
              ['Engine', d.Engine.EngineVersion],
              n.Status.Message && [tr('Message'), n.Status.Message],
              [tc('f', 'Created'), dateTime(n.CreatedAt)],
            ]} />
          </Section>
          <Section title="Labels" onContextMenu={menu(() => [{ label: tr('Edit labels…'), onClick: () => editNodeLabels(ep, n) }])}>
            <Props items={Object.entries(n.Spec.Labels || {}).map(([k, v]) => [<span className="mono small">{k}</span>, <span className="mono small">{v}</span>])} />
            {!Object.keys(n.Spec.Labels || {}).length && <p className="muted small">{tr('No labels')}</p>}
            {d.Engine.Labels && Object.keys(d.Engine.Labels).length > 0 && (
              <>
                <h4 className="subhead">{tr('Engine labels')}</h4>
                <Props items={Object.entries(d.Engine.Labels).map(([k, v]) => [<span className="mono small">{k}</span>, <span className="mono small">{v}</span>])} />
              </>
            )}
          </Section>
        </aside>
        <main className="record-main">
          <Tabs active={tab} onChange={setTab} tabs={[{ id: 'tasks', label: tr('Tasks'), count: data.tasks.filter((t) => t.state === 'running').length }, { id: 'inspect', label: 'Inspect' }]} />
          <div className="tab-body">
            {tab === 'tasks' && <TaskTable ep={ep} tasks={data.tasks} show="node" />}
            {tab === 'inspect' && <JsonView value={data.node} />}
          </div>
        </main>
      </div>
    </div>
  )
}

// ---------- secrets and configs ----------

function NewObjectDialog({ ep, kind, onClose }: { ep: string; kind: 'secrets' | 'configs'; onClose: () => void }) {
  const [name, setName] = useState('')
  const [data, setData] = useState('')
  const [labels, setLabels] = useState('')
  const [b64, setB64] = useState(false)
  const secret = kind === 'secrets'
  const submit = async () => {
    try {
      await api.post(`/e/${enc(ep)}/${kind}`, { name, data, base64: b64, labels: labels ? parseLabels(labels) : undefined })
      toast.ok(tr('{0} created', name))
      invalidate(`/${kind}`)
      onClose()
    } catch (e) {
      toast.err(e)
    }
  }
  const readFile = (file?: File) => {
    if (!file) return
    const r = new FileReader()
    r.onload = () => {
      const s = String(r.result)
      setData(s.slice(s.indexOf(',') + 1))
      setB64(true)
      if (!name) setName(file.name.replace(/[^a-zA-Z0-9_.-]/g, '-'))
    }
    r.readAsDataURL(file)
  }
  return (
    <Modal
      wide
      title={secret ? tr('New secret') : tr('New config')}
      icon={secret ? <KeyRound size={18} /> : <FileText size={18} />}
      onClose={onClose}
      footer={<><label className="btn btn-sm">{tr('Load file…')}<input type="file" hidden onChange={(e) => readFile(e.target.files?.[0])} /></label><span className="grow" /><button className="btn" onClick={onClose}>{tr('Cancel')}</button><button className="btn btn-primary" disabled={!name || !data} onClick={submit}>{tr('Create')}</button></>}
    >
      <div className="grid-2">
        <label className="field"><span>{tr('Name')}</span><input className="mono" value={name} onChange={(e) => setName(e.target.value)} autoFocus /></label>
        <label className="field"><span>Labels</span><input className="mono" value={labels} placeholder="key=value" onChange={(e) => setLabels(e.target.value)} /></label>
      </div>
      {b64 ? (
        <div className="alert">{tr('Content loaded from a file ({0}).', bytes((data.length * 3) / 4))} <button className="btn btn-sm" onClick={() => (setData(''), setB64(false))}>{tr('Clear')}</button></div>
      ) : (
        <>
          <div className="label">{tr('Content')}</div>
          <CodeEditor value={data} onChange={setData} minRows={10} />
        </>
      )}
      <p className="help">
        {secret
          ? tr('Secrets are encrypted in the swarm and cannot be read back: the services that use them see them in /run/secrets/<name>. They cannot be edited; to change one, create a new version and point the services at it.')
          : tr('Configs are stored unencrypted and mounted as files in the services. They cannot be edited; create a new version to change one.')}
      </p>
    </Modal>
  )
}

export function SwarmObjects({ ep, kind }: { ep: string; kind: 'secrets' | 'configs' }) {
  const { manager } = useSwarm(ep)
  const { data, error } = useResource<SwarmObject[]>(manager ? `/e/${enc(ep)}/${kind}` : null, { poll: 20000 })
  const [q, setQ] = useState('')
  const menu = useMenu()
  const secret = kind === 'secrets'
  const icon = secret ? <KeyRound size={20} /> : <FileText size={20} />
  if (!manager) return <NeedsManager ep={ep} icon={icon} title={secret ? 'Secrets' : 'Configs'} />
  const all = data || []
  const rows = all.filter((o) => !q || o.Spec.Name.toLowerCase().includes(q.toLowerCase()))
  const create = () => openDialog((c) => <NewObjectDialog ep={ep} kind={kind} onClose={c} />)
  return (
    <div className="page" onContextMenu={(e) => (e.target as HTMLElement).closest('.page-body') && menu(() => [
      { label: secret ? tr('New secret…') : tr('New config…'), icon: <Plus size={14} />, onClick: create },
      { label: tr('Reload'), onClick: () => invalidate(`/${kind}`) },
    ])(e)}>
      <div className="page-head">
        <div className="page-title">{icon}<h1>{secret ? 'Secrets' : 'Configs'}</h1><span className="count">{all.length}</span></div>
        <div className="page-actions">
          <div className="search"><Search size={14} /><input placeholder={tr('Search…')} value={q} onChange={(e) => setQ(e.target.value)} /></div>
          <button className="btn btn-primary needs-admin" onClick={create}><Plus size={15} /> {secret ? tr('New secret') : tr('New config')}</button>
        </div>
      </div>
      <div className="page-body">
        {error && <div className="alert alert-bad">{error.message}</div>}
        {!data && !error && <div className="center"><Spinner /></div>}
        {data && (
          <DataTable
            rows={rows}
            rowKey={(o) => o.ID}
            onOpen={(o) => go(ep, kind, o.ID)}
            rowMenu={(o) => swarmObjectActions(ep, kind, o)}
            defaultSort={{ key: 'name', dir: 1 }}
            empty={<Empty icon={icon} title={secret ? tr('No secrets') : tr('No configs')} />}
            columns={[
              { key: 'name', label: tr('Name'), sort: (o) => o.Spec.Name, render: (o) => <span className="cell-name"><strong className="mono small">{o.Spec.Name}</strong>{o.Spec.Labels?.['com.docker.stack.namespace'] && <Tag tone="stack">{o.Spec.Labels['com.docker.stack.namespace']}</Tag>}</span> },
              { key: 'used', label: tr('Used by'), sort: (o) => -o.UsedBy.length, render: (o) => (o.UsedBy.length ? <span className="tags">{o.UsedBy.map((u) => <Tag key={u.id} onClick={() => go(ep, 'services', u.id)}>{u.name}</Tag>)}</span> : <span className="muted small">{tr('unused')}</span>) },
              { key: 'created', label: tr('Created'), width: 130, sort: (o) => -Date.parse(o.CreatedAt), render: (o) => <span className="muted small" title={dateTime(o.CreatedAt)}>{ago(o.CreatedAt)}</span> },
              { key: 'm', label: '', width: 36, render: (o) => <MenuButton items={() => swarmObjectActions(ep, kind, o)} /> },
            ]}
          />
        )}
      </div>
    </div>
  )
}

function decode(b64?: string) {
  if (!b64) return ''
  try {
    return new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)))
  } catch {
    return b64
  }
}

export function SwarmObjectRecord({ ep, kind, id }: { ep: string; kind: 'secrets' | 'configs'; id: string }) {
  const { data: list } = useResource<SwarmObject[]>(`/e/${enc(ep)}/${kind}`)
  const { data: inspect, error } = useResource<SwarmObject>(`/e/${enc(ep)}/${kind}/${enc(id)}`)
  const menu = useMenu()
  const secret = kind === 'secrets'
  if (error) return <div className="page"><Empty title={tr('Not found')}>{error.message}</Empty></div>
  if (!inspect) return <div className="page center"><Spinner /></div>
  const o: SwarmObject = { ...inspect, UsedBy: list?.find((x) => x.ID === inspect.ID)?.UsedBy || [] }
  return (
    <div className="page record">
      <div className="breadcrumbs"><a href={href(ep, kind)}>{secret ? 'Secrets' : 'Configs'}</a><ChevronRight size={13} /><span>{o.Spec.Name}</span></div>
      <header className="record-head" onContextMenu={menu(() => swarmObjectActions(ep, kind, o))}>
        <span className="net-swatch big" style={{ ['--h' as any]: secret ? 30 : 160 }}>{secret ? <KeyRound size={24} /> : <FileText size={24} />}</span>
        <div className="record-title"><h1 className="mono">{o.Spec.Name}</h1><div className="row gap"><Tag>{secret ? 'secret' : 'config'}</Tag><span className="muted small">{ago(o.CreatedAt)}</span></div></div>
        <div className="record-actions"><MenuButton items={() => swarmObjectActions(ep, kind, o)} className="btn" /></div>
      </header>
      <div className="record-body">
        <aside className="record-side">
          <Section title={tr('Properties')}>
            <Props items={[
              ['ID', <span className="mono small">{shortId(o.ID)}</span>, () => [{ label: tr('Copy ID'), onClick: () => copy(o.ID) }]],
              [tr('Created'), dateTime(o.CreatedAt)],
              [tr('Updated'), dateTime(o.UpdatedAt)],
              ...Object.entries(o.Spec.Labels || {}).map(([k, v]) => [<span className="mono small">{k}</span>, <span className="mono small">{v}</span>] as [React.ReactNode, React.ReactNode]),
            ]} />
          </Section>
          <Section title={tr('Used by · {0}', o.UsedBy.length)}>
            <ul className="rel-list">
              {o.UsedBy.map((u) => <li key={u.id} onClick={() => go(ep, 'services', u.id)}><Boxes size={14} /><span className="grow">{u.name}</span></li>)}
              {!o.UsedBy.length && <li className="muted small">{tr('No service uses it')}</li>}
            </ul>
          </Section>
        </aside>
        <main className="record-main">
          {secret ? (
            <div className="alert">{tr('The content of a secret cannot be read back from the swarm.')}</div>
          ) : (
            <>
              <h4 className="subhead">{tr('Content')}</h4>
              <CodeEditor value={decode(o.Spec.Data)} readOnly minRows={16} />
            </>
          )}
          <h4 className="subhead">Inspect</h4>
          <JsonView value={inspect} />
        </main>
      </div>
    </div>
  )
}

// ---------- stacks (docker stack deploy) ----------

/** The compose builds images (build: under a service). */
export const hasBuild = (text: string) => /^\s+build:/m.test(text)

function PushCheck({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="check" title={tr('After building, push the images to their registry so every node of the swarm can pull them. Without it they only exist on the node that built them.')}>
      <input type="checkbox" checked={value} onChange={(e) => onChange(e.target.checked)} /> {tr('push built images')}
    </label>
  )
}

const SWARM_SAMPLE = `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    deploy:
      replicas: 2
      update_config:
        parallelism: 1
        order: start-first
`

export function NewSwarmStackDialog({ ep, onClose, initial }: { ep: string; onClose: () => void; initial?: { name?: string; compose?: string; env?: string; note?: string } }) {
  const [name, setName] = useState(initial?.name ?? '')
  const [compose, setCompose] = useState(initial?.compose ?? SWARM_SAMPLE)
  const [env, setEnv] = useState(initial?.env ?? '')
  const [deploy, setDeploy] = useState(true)
  const [mode, setMode] = usePref<'yaml' | 'visual'>('compose.mode', 'visual')
  const [busy, setBusy] = useState(false)
  const [place, setPlace, remote] = usePlacement(ep)
  const [push, setPush] = useState(false)
  const existing = place.mode === 'dir' && place.existing
  const submit = async () => {
    setBusy(true)
    try {
      await runJob(tr('Create stack {0}', name), 'POST', `/e/${enc(ep)}/swarm/stacks`, { name, compose: existing ? '' : compose, env: existing ? '' : env, deploy, push, ...placementBody(place, name) })
      onClose()
      invalidate('/swarm', '/services')
      go(ep, 'swarm-stacks', name)
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      wide="xl"
      title={tr('New swarm stack')}
      icon={<Layers size={18} />}
      onClose={onClose}
      footer={
        <>
          <label className="check"><input type="checkbox" checked={deploy} onChange={(e) => setDeploy(e.target.checked)} /> {tr('Deploy now (stack deploy)')}</label>
          {deploy && (existing || hasBuild(compose)) && <PushCheck value={push} onChange={setPush} />}
          <span className="grow" />
          <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
          <button className="btn btn-primary" disabled={busy || !name || (!existing && !compose.trim())} onClick={submit}><Rocket size={14} /> {tr('Create stack')}</button>
        </>
      }
    >
      {initial?.note && <div className="alert" dangerouslySetInnerHTML={{ __html: initial.note }} />}
      <label className="field">
        <span>{tr('Stack name')}</span>
        <input value={name} placeholder="mi-stack" onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, '-'))} autoFocus />
      </label>
      <div className="field">
        <span>{tr('Location')}</span>
        <PlacementFields name={name} value={place} onChange={setPlace} remote={remote} allowExisting />
      </div>
      <p className="help">{tr('Deployed with docker stack deploy: use the deploy: key for replicas, placement and update policy. Services with build: are built in the stack directory before deploying (they need image: to name the result); file-based secrets and configs resolve there too. Variables come from the .env.')}</p>
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

export function SwarmStacks({ ep }: { ep: string }) {
  const { manager } = useSwarm(ep)
  const { data, error } = useResource<SwarmStack[]>(manager ? `/e/${enc(ep)}/swarm/stacks` : null, { poll: 10000 })
  const [q, setQ] = useState('')
  const [layout, setLayout] = usePref<'cards' | 'table'>('stacks.layout', 'cards')
  const menu = useMenu()
  if (!manager) return <NeedsManager ep={ep} icon={<Layers size={28} />} title={tr('Swarm stacks')} />
  const all = data || []
  const rows = all.filter((s) => !q || s.name.includes(q.toLowerCase()))
  const newStack = () => openDialog((c) => <NewSwarmStackDialog ep={ep} onClose={c} />)
  return (
    <div className="page" onContextMenu={(e) => (e.target as HTMLElement).closest('.page-body') && menu(() => [
      { label: tr('New stack…'), icon: <Plus size={14} />, onClick: newStack },
      { label: tr('Go to the marketplace'), onClick: () => go(ep, 'marketplace') },
      { label: tr('Reload'), onClick: () => invalidate('/swarm/stacks') },
      'sep',
      { label: tr('Cards'), checked: layout === 'cards', onClick: () => setLayout('cards') },
      { label: tr('Table'), checked: layout === 'table', onClick: () => setLayout('table') },
    ])(e)}>
      <div className="page-head">
        <div className="page-title"><Layers size={20} /><h1>{tr('Swarm stacks')}</h1><span className="count">{all.length}</span></div>
        <div className="page-actions">
          <div className="search"><Search size={14} /><input placeholder={tr('Search stack…')} value={q} onChange={(e) => setQ(e.target.value)} /></div>
          <div className="seg">
            <button className={layout === 'cards' ? 'on' : ''} onClick={() => setLayout('cards')}><LayoutGrid size={15} /></button>
            <button className={layout === 'table' ? 'on' : ''} onClick={() => setLayout('table')}><LayoutList size={15} /></button>
          </div>
          <button className="btn btn-primary needs-admin" onClick={newStack}><Plus size={15} /> {tr('New stack')}</button>
        </div>
      </div>
      <div className="page-body">
        {error && <div className="alert alert-bad">{error.message}</div>}
        {!data && !error && <div className="center"><Spinner /></div>}
        {data && rows.length === 0 && <Empty icon={<Layers size={28} />} title={tr('No stacks')} action={<button className="btn btn-primary needs-admin" onClick={newStack}><Plus size={15} /> {tr('Create stack')}</button>}>{tr('Deploy a compose file to the swarm with docker stack deploy.')}</Empty>}
        {layout === 'cards' && (
          <div className="cards">
            {rows.map((s) => (
              <div key={s.name} className="stack-card" onClick={() => go(ep, 'swarm-stacks', s.name)} onContextMenu={menu(() => swarmStackActions(ep, s))}>
                <div className="row gap">
                  <Avatar name={s.name} size={34} square />
                  <div className="grow">
                    <strong>{s.name}</strong>
                    <div className="muted small">{s.managed ? tr('managed by ducktainer') : tr('external')}</div>
                  </div>
                  <MenuButton items={() => swarmStackActions(ep, s)} />
                </div>
                <div className="row gap">
                  <Pill state={s.status} />
                  <span className="muted small">{s.running}/{s.desired} {tr('tasks')}</span>
                </div>
                <div className="svc-dots">
                  {s.services.map((sv) => (
                    <span key={sv.id} className={`svc-dot tone-${sv.running >= sv.desired && sv.desired > 0 ? 'ok' : sv.running ? 'warn' : 'idle'}`} title={`${sv.name}: ${sv.running}/${sv.desired}`} onClick={(e) => (e.stopPropagation(), go(ep, 'services', sv.id))}>
                      {sv.name.replace(s.name + '_', '')}
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
            onOpen={(s) => go(ep, 'swarm-stacks', s.name)}
            rowMenu={(s) => swarmStackActions(ep, s)}
            defaultSort={{ key: 'name', dir: 1 }}
            columns={[
              { key: 'name', label: 'Stack', sort: (s) => s.name, render: (s) => <span className="cell-name"><Avatar name={s.name} size={24} square /><strong>{s.name}</strong></span> },
              { key: 'status', label: tr('Status'), width: 150, sort: (s) => s.status, render: (s) => <Pill state={s.status} /> },
              { key: 'n', label: tr('Tasks'), width: 100, render: (s) => `${s.running}/${s.desired}` },
              { key: 'svc', label: tr('Services'), render: (s) => <span className="tags">{s.services.map((x) => <Tag key={x.id}>{x.name.replace(s.name + '_', '')}</Tag>)}</span> },
              { key: 'origin', label: tr('Origin'), width: 120, render: (s) => <span className="muted small">{s.managed ? 'ducktainer' : tr('external')}</span> },
              { key: 'm', label: '', width: 36, render: (s) => <MenuButton items={() => swarmStackActions(ep, s)} /> },
            ]}
          />
        )}
      </div>
    </div>
  )
}

function SwarmComposeEditor({ ep, stack, files }: { ep: string; stack: SwarmStack; files?: { files: { content: string; path: string }[]; env: string; env_path?: string; dir?: string } }) {
  const orig = files?.files[0]?.content ?? ''
  const [text, setText] = useState(orig)
  const [env, setEnv] = useState(files?.env ?? '')
  const [active, setActive] = useState('compose')
  const [prune, setPrune] = useState(true)
  const [push, setPush] = usePref(`swarm.push.${ep}.${stack.name}`, false)
  const [busy, setBusy] = useState(false)
  const [mode, setMode] = usePref<'yaml' | 'visual'>('compose.mode', 'visual')
  useEffect(() => setText(orig), [orig])
  useEffect(() => setEnv(files?.env ?? ''), [files?.env])
  const dirty = text !== orig || env !== (files?.env ?? '')
  const save = async (deploy: boolean) => {
    setBusy(true)
    try {
      await runJob(`${deploy ? tr('Save and deploy') : tr('Save')} ${stack.name}`, 'PUT', `/e/${enc(ep)}/swarm/stacks/${enc(stack.name)}`, { compose: text, env, deploy, prune, push })
      invalidate('/swarm', '/services')
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="compose-editor">
      {!stack.managed && (
        <div className="alert">
          {tr('This stack was deployed outside ducktainer and Docker does not keep its compose file. Paste it here and save to manage the stack from ducktainer.')}{' '}
          <button className="btn btn-sm" onClick={() => openDialog((close) => <LocationDialog ep={ep} swarm stack={{ name: stack.name, total: stack.running, managed: false }} onClose={close} />)}><FolderInput size={14} /> {tr('Use the compose file in a directory…')}</button>
        </div>
      )}
      {stack.location && files?.dir && <div className="alert">{stack.node ? tr('Editing on the node:') : tr('Editing in place:')} <code>{files.dir}</code></div>}
      <div className="file-tabs-row">
        <Tabs active={active} onChange={setActive} tabs={[
          { id: 'compose', label: <>compose.yaml{text !== orig && <span className="dirty-dot" />}</> },
          { id: 'env', label: <>.env{env !== (files?.env ?? '') && <span className="dirty-dot" />}</> },
        ]} />
        {active === 'compose' && (
          <div className="seg seg-text">
            <button className={mode === 'visual' ? 'on' : ''} onClick={() => setMode('visual')}>Visual</button>
            <button className={mode === 'yaml' ? 'on' : ''} onClick={() => setMode('yaml')}>YAML</button>
          </div>
        )}
      </div>
      <div className="file-tab-body">
        {active === 'compose' && (mode === 'yaml' ? <CodeEditor value={text} onChange={setText} minRows={28} /> : <ComposeVisual text={text} onChange={setText} envText={env} />)}
        {active === 'env' && <CodeEditor value={env} onChange={setEnv} minRows={20} placeholder={tr('VARIABLE=value')} />}
      </div>
      <div className={`savebar${dirty ? ' dirty' : ''}`}>
        <span>{dirty ? tr('Unsaved changes') : tr('No changes')}</span>
        <label className="check" title={tr('Remove services that are no longer in the compose file')}><input type="checkbox" checked={prune} onChange={(e) => setPrune(e.target.checked)} /> --prune</label>
        {hasBuild(text) && <PushCheck value={push} onChange={setPush} />}
        <span className="grow" />
        <button className="btn" disabled={!dirty || busy} onClick={() => (setText(orig), setEnv(files?.env ?? ''))}><Undo2 size={14} /> {tr('Discard')}</button>
        <button className="btn" disabled={!dirty || busy || !text.trim()} onClick={() => save(false)}><Save size={14} /> {tr('Save')}</button>
        <button className="btn btn-primary" disabled={busy || !text.trim()} onClick={() => save(true)}><Rocket size={14} /> {tr('Save and deploy')}</button>
      </div>
    </div>
  )
}

export function SwarmStackRecord({ ep, name, tab = 'services' }: { ep: string; name: string; tab?: string }) {
  const { data, error } = useResource<{ stack: SwarmStack; files?: { files: { content: string; path: string }[]; env: string; env_path?: string; dir?: string }; files_error?: string }>(`/e/${enc(ep)}/swarm/stacks/${enc(name)}`, { poll: 8000 })
  const { data: services } = useResource<Service[]>(`/e/${enc(ep)}/services`)
  const menu = useMenu()
  if (error) return <div className="page"><Empty icon={<Layers size={28} />} title={tr('Stack not found')}>{error.message}</Empty></div>
  if (!data) return <div className="page center"><Spinner /></div>
  const s = data.stack
  const full = (services || []).filter((x) => s.services.some((y) => y.id === x.ID))
  const setTab = (t: string) => go(ep, 'swarm-stacks', name, t === 'services' ? undefined : t)
  return (
    <div className="page record">
      <div className="breadcrumbs"><a href={href(ep, 'swarm-stacks')}>{tr('Swarm stacks')}</a><ChevronRight size={13} /><span>{s.name}</span></div>
      <header className="record-head" onContextMenu={menu(() => swarmStackActions(ep, s))}>
        <Avatar name={s.name} size={52} square />
        <div className="record-title">
          <h1>{s.name}</h1>
          <div className="row gap wrap">
            <Pill state={s.status} />
            <span className="muted small">{s.running}/{s.desired} {tr('tasks')} · {s.services.length} {tr('services')}</span>
            {s.managed ? <Tag tone="accent">ducktainer</Tag> : <Tag>{tr('external')}</Tag>}
            <Tag>swarm</Tag>
          </div>
        </div>
        <div className="record-actions needs-admin">
          <button className="btn btn-primary" disabled={!s.managed} title={s.managed ? (data.files && hasBuild(data.files.files[0]?.content ?? '') ? tr('Builds the images, then docker stack deploy') : undefined) : tr('no files')} onClick={() => swarmStackDeploy(ep, s.name)}><Rocket size={14} /> {tr('Deploy')}</button>
          <MenuButton items={() => swarmStackActions(ep, s)} className="btn" />
        </div>
      </header>
      <div className="record-body">
        <aside className="record-side">
          <Section title={tr('Properties')}>
            <Props items={[
              ['Stack', s.name],
              [tr('Origin'), s.managed ? tr('Created with ducktainer') : tr('External (docker stack deploy)')],
              s.managed && [tr('Location'), s.location ? (s.node ? tr('Directory on the node') : tr('Own directory')) : tr('ducktainer store'), () => [{ label: s.location ? tr('Change location…') : tr('Move to a directory…'), onClick: () => openDialog((close) => <LocationDialog ep={ep} swarm stack={{ name: s.name, location: s.location, total: s.running }} onClose={close} />) }]],
              data.files && [tr('Files'), <span className="mono small">{data.files.files[0]?.path}</span>, () => [{ label: tr('Copy path'), onClick: () => copy(data.files!.files[0].path) }]],
              data.files_error && [tr('Files'), <span className="text-bad small">{data.files_error}</span>],
            ]} />
          </Section>
          <Section title={tr('Services · {0}', s.services.length)}>
            <ul className="rel-list">
              {s.services.map((sv) => {
                const fs = full.find((x) => x.ID === sv.id)
                return (
                  <li key={sv.id} onClick={() => go(ep, 'services', sv.id)} onContextMenu={menu(() => (fs ? serviceActions(ep, fs) : []))}>
                    <span className={`svc-state tone-${sv.running >= sv.desired && sv.desired > 0 ? 'ok' : 'idle'}`} />
                    <span className="grow">{sv.name.replace(s.name + '_', '')}</span>
                    <span className="muted small">{sv.running}/{sv.desired}</span>
                  </li>
                )
              })}
            </ul>
          </Section>
        </aside>
        <main className="record-main">
          <Tabs active={tab} onChange={setTab} tabs={[
            { id: 'services', label: tr('Services'), count: s.services.length },
            { id: 'compose', label: 'Compose' },
            { id: 'logs', label: tr('Combined logs') },
          ]} />
          <div className="tab-body">
            {tab === 'services' && (
              <DataTable
                rows={full}
                rowKey={(x) => x.ID}
                onOpen={(x) => go(ep, 'services', x.ID)}
                rowMenu={(x) => serviceActions(ep, x)}
                empty={<Empty icon={<Boxes size={28} />} title={tr('No services')}>{tr('The stack is not deployed.')}</Empty>}
                columns={[
                  { key: 'n', label: tr('Service'), render: (x) => <strong>{x.Spec.Name.replace(s.name + '_', '')}</strong> },
                  { key: 'r', label: tr('Replicas'), width: 120, render: (x) => <Replicas sv={x} /> },
                  { key: 'i', label: tr('Image'), render: (x) => <span className="mono small">{serviceImage(x)}</span> },
                  { key: 'p', label: tr('Ports'), render: (x) => <PortList sv={x} /> },
                  { key: 'm', label: '', width: 36, render: (x) => <MenuButton items={() => serviceActions(ep, x)} /> },
                ]}
              />
            )}
            {tab === 'compose' && (isViewer() ? <CodeEditor value={data.files?.files[0]?.content ?? ''} readOnly minRows={24} /> : <SwarmComposeEditor ep={ep} stack={s} files={data.files} />)}
            {tab === 'logs' && (s.services.length ? <LogViewer path={`/e/${enc(ep)}/swarm/stacks/${enc(name)}/logs`} multi height="calc(100vh - 290px)" /> : <Empty title={tr('The stack has no services')} />)}
          </div>
        </main>
      </div>
    </div>
  )
}
