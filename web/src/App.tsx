import { t as tr, LANGUAGES, getLang, setLang } from './i18n'
import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Home as HomeIcon, Box, Layers, Image as ImageIcon, Network as NetIcon, HardDrive, Store, Activity as ActIcon, Settings as Cog,
  Search, Plus, ChevronsUpDown, Zap, Languages, Star, Clock, LogOut, Moon, Sun, Server, Check, Download, PanelLeftClose, PanelLeft, Rocket,
  Boxes, KeyRound, FileText, Network as ClusterIcon, Bell,
} from 'lucide-react'
import { api, enc, setUnauthorizedHandler, stream } from './lib/api'
import type { Container, Endpoint, Image, Network, Service, Stack, SwarmNode, SwarmStack, Volume } from './lib/types'
import { invalidate, peek, useResource } from './lib/store'
import { usePref } from './lib/prefs'
import { cname, shortId, stackOf } from './lib/format'
import { go, href, useRoute } from './lib/router'
import { ContextMenuProvider, useMenu } from './ui/ContextMenu'
import type { MenuEntry } from './ui/ContextMenu'
import { OverlayHost, openDialog, toast } from './ui/overlays'
import { Avatar, Kbd, Spinner } from './ui/common'
import { getMe, isViewer, setMe } from './lib/session'
import type { Me } from './lib/session'
import { containerActions, imageActions, networkActions, stackActions, volumeActions, pullImage, serviceActions, nodeActions, nodeName, swarmStackActions, serviceImage } from './actions'
import type { Fav } from './actions'
import { ContainerFormDialog } from './components/ContainerForm'
import { Home, Activity } from './pages/Home'
import { Alerts } from './pages/Alerts'
import { Containers } from './pages/Containers'
import { Jump } from './pages/Jump'
import { ContainerRecord } from './pages/ContainerRecord'
import { Stacks, StackRecord, NewStackDialog } from './pages/Stacks'
import { Images, ImageRecord, Networks, NetworkRecord, Volumes, VolumeRecord } from './pages/Resources'
import { Marketplace } from './pages/Marketplace'
import { NewServiceDialog, NewSwarmStackDialog, NodeRecord, Nodes, ServiceRecord, Services, SwarmObjectRecord, SwarmObjects, SwarmOverview, SwarmStackRecord, SwarmStacks, useSwarm } from './pages/Swarm'
import { Settings, Login } from './pages/Settings'
import { prompt } from './ui/overlays'

const SECTION_LABEL: Record<string, string> = {
  containers: tr('container'), stacks: 'stack', images: tr('image'), networks: tr('network'), volumes: tr('volume'),
  services: tr('service'), nodes: tr('node'), secrets: 'secret', configs: 'config', 'swarm-stacks': 'swarm stack',
}
const RECORD_SECTIONS = ['containers', 'stacks', 'images', 'networks', 'volumes', 'services', 'nodes', 'secrets', 'configs', 'swarm-stacks']

type Recent = { ep: string; section: string; id: string; label: string }

const NAV = [
  { id: 'jump', label: tr('Jump'), icon: Zap },
  { id: 'home', label: tr('Dashboard'), icon: HomeIcon },
  { id: 'containers', label: tr('Containers'), icon: Box },
  { id: 'stacks', label: 'Stacks', icon: Layers },
  { id: 'images', label: tr('Images'), icon: ImageIcon },
  { id: 'networks', label: tr('Networks'), icon: NetIcon },
  { id: 'volumes', label: tr('Volumes'), icon: HardDrive },
] as const

// Shown when the host is part of a swarm (only the overview on workers).
const SWARM_NAV = [
  { id: 'swarm', label: tr('Cluster'), icon: ClusterIcon },
  { id: 'services', label: tr('Services'), icon: Boxes },
  { id: 'swarm-stacks', label: tr('Swarm stacks'), icon: Layers },
  { id: 'nodes', label: tr('Nodes'), icon: Server },
  { id: 'secrets', label: 'Secrets', icon: KeyRound },
  { id: 'configs', label: 'Configs', icon: FileText },
] as const

const NAV2 = [
  { id: 'marketplace', label: 'Marketplace', icon: Store },
  { id: 'activity', label: tr('Activity'), icon: ActIcon },
  { id: 'alerts', label: tr('Alerts'), icon: Bell },
  { id: 'settings', label: tr('Settings'), icon: Cog },
] as const

function labelFor(ep: string, section: string, id: string) {
  const e = enc(ep)
  if (section === 'containers') {
    const c = peek<Container[]>(`/e/${e}/containers`)?.find((x) => x.Id === id)
    return c ? cname(c) : shortId(id)
  }
  if (section === 'networks') return peek<Network[]>(`/e/${e}/networks`)?.find((x) => x.Id === id)?.Name ?? shortId(id)
  if (section === 'images') return peek<Image[]>(`/e/${e}/images`)?.find((x) => x.Id === id)?.RepoTags?.[0] ?? shortId(id)
  if (section === 'services') return peek<Service[]>(`/e/${e}/services`)?.find((x) => x.ID === id)?.Spec.Name ?? shortId(id)
  if (section === 'nodes') {
    const n = peek<SwarmNode[]>(`/e/${e}/nodes`)?.find((x) => x.ID === id)
    return n ? nodeName(n) : shortId(id)
  }
  if (section === 'secrets' || section === 'configs') return peek<{ ID: string; Spec: { Name: string } }[]>(`/e/${e}/${section}`)?.find((x) => x.ID === id)?.Spec.Name ?? shortId(id)
  return id.length > 32 ? shortId(id) : id
}

/** Turns Docker events into cache invalidations so every view stays live. */
function useLiveEvents(ep: string) {
  useEffect(() => {
    if (!ep) return
    let ac = new AbortController()
    let stopped = false
    let timer: any
    const pending = new Set<string>()
    const flush = () => {
      timer = 0
      invalidate(...pending)
      pending.clear()
    }
    const connect = () => {
      ac = new AbortController()
      stream('GET', `/e/${enc(ep)}/events`, undefined, (e) => {
        if (!e.Type || e.Action?.startsWith('exec_') || e.Action?.startsWith('top')) return
        const map: Record<string, string[]> = {
          container: ['/containers', '/stacks', '/networks', '/volumes', '/info', '/services', '/swarm/stacks'],
          image: ['/images'],
          network: ['/networks'],
          volume: ['/volumes'],
          service: ['/services', '/swarm'],
          node: ['/nodes', '/swarm'],
          secret: ['/secrets'],
          config: ['/configs'],
        }
        ;(map[e.Type] || []).forEach((k) => pending.add(k))
        if (!timer) timer = setTimeout(flush, 600)
      }, ac.signal)
        .catch(() => {})
        .finally(() => !stopped && setTimeout(connect, 3000))
    }
    connect()
    return () => {
      stopped = true
      ac.abort()
      clearTimeout(timer)
    }
  }, [ep])
}

function EndpointSwitcher({ ep, collapsed }: { ep: string; collapsed: boolean }) {
  const { data } = useResource<Endpoint[]>('/endpoints', { poll: 60000 })
  const menu = useMenu()
  const cur = data?.find((e) => e.name === ep)
  const items = (): MenuEntry[] => [
    { header: tr('Hosts') },
    ...(data || []).map((e) => ({
      label: e.name,
      icon: <span className={`dot dot-${e.online ? 'open' : 'closed'}`} />,
      hint: e.online ? `${e.running}/${e.total}` : 'offline',
      checked: undefined,
      onClick: () => go(e.name, 'jump'),
    })),
    'sep',
    { label: tr('Add host…'), icon: <Plus size={14} />, onClick: () => go(ep, 'settings', 'hosts') },
    { label: tr('Manage hosts'), icon: <Server size={14} />, onClick: () => go(ep, 'settings', 'hosts') },
  ]
  return (
    <button
      className="workspace"
      onClick={(e) => {
        const r = e.currentTarget.getBoundingClientRect()
        menu(items)({ preventDefault() {}, stopPropagation() {}, clientX: r.left, clientY: r.bottom + 4 } as any)
      }}
      onContextMenu={menu(items)}
      title={cur?.url}
    >
      <span className="brand-mark">{ep.slice(0, 1).toUpperCase() || 'd'}</span>
      {!collapsed && (
        <>
          <span className="workspace-text">
            <strong>{ep || '…'}</strong>
            <span className="muted small ellipsis">{cur ? (cur.online ? `Docker ${cur.version}` : tr('offline')) : 'ducktainer'}</span>
          </span>
          <ChevronsUpDown size={14} className="muted" />
        </>
      )}
    </button>
  )
}

function Sidebar({ ep, section, collapsed, setCollapsed }: { ep: string; section: string; collapsed: boolean; setCollapsed: (v: boolean) => void }) {
  const { data: cts } = useResource<Container[]>(`/e/${enc(ep)}/containers`)
  const { data: stacks } = useResource<Stack[]>(`/e/${enc(ep)}/stacks`)
  const swarm = useSwarm(ep)
  const { data: services } = useResource<Service[]>(swarm.manager ? `/e/${enc(ep)}/services` : null)
  const { data: nodes } = useResource<SwarmNode[]>(swarm.manager ? `/e/${enc(ep)}/nodes` : null)
  const swarmNav = swarm.manager ? SWARM_NAV : swarm.active ? SWARM_NAV.slice(0, 1) : []
  const [favs, setFavs] = usePref<Fav[]>('favorites', [])
  const [recent, setRecent] = usePref<Recent[]>('recent', [])
  const menu = useMenu()
  const counts: Record<string, string | undefined> = {
    containers: cts ? `${cts.filter((c) => c.State === 'running').length}/${cts.length}` : undefined,
    stacks: stacks ? String(stacks.length) : undefined,
    services: services ? String(services.length) : undefined,
    nodes: nodes ? `${nodes.filter((n) => n.Status.State === 'ready').length}/${nodes.length}` : undefined,
  }
  const navMenu = (id: string, label: string) =>
    menu(() => [
      { header: label },
      { label: tr('Open'), onClick: () => go(ep, id) },
      { label: tr('Open in another tab'), onClick: () => window.open(href(ep, id), '_blank') },
      id === 'containers' && 'sep',
      id === 'containers' && { label: tr('New container…'), icon: <Plus size={14} />, onClick: () => openDialog((c) => <ContainerFormDialog ep={ep} initial={{}} mode="create" onClose={c} />) },
      id === 'stacks' && 'sep',
      id === 'stacks' && { label: tr('New stack…'), icon: <Plus size={14} />, onClick: () => openDialog((c) => <NewStackDialog ep={ep} onClose={c} />) },
      id === 'services' && 'sep',
      id === 'services' && { label: tr('New service…'), icon: <Plus size={14} />, onClick: () => openDialog((c) => <NewServiceDialog ep={ep} onClose={c} />) },
      id === 'swarm-stacks' && 'sep',
      id === 'swarm-stacks' && { label: tr('New stack…'), icon: <Plus size={14} />, onClick: () => openDialog((c) => <NewSwarmStackDialog ep={ep} onClose={c} />) },
      id === 'images' && 'sep',
      id === 'images' && { label: tr('Pull image…'), icon: <Download size={14} />, onClick: async () => { const i = await prompt({ title: tr('Pull image'), label: tr('Image'), placeholder: 'nginx:alpine' }); if (i) pullImage(ep, i) } },
      { label: tr('Reload data'), onClick: () => invalidate(`/${id}`) },
    ])
  const favMenu = (f: Fav) => {
    const e = enc(ep)
    const base: MenuEntry[] = []
    if (f.kind === 'container') {
      const c = peek<Container[]>(`/e/${e}/containers`)?.find((x) => cname(x) === f.id)
      if (c) return containerActions(ep, c)
    }
    if (f.kind === 'stack') {
      const s = peek<Stack[]>(`/e/${e}/stacks`)?.find((x) => x.name === f.id)
      if (s) return stackActions(ep, s)
    }
    if (f.kind === 'image') {
      const i = peek<Image[]>(`/e/${e}/images`)?.find((x) => x.Id === f.id)
      if (i) return imageActions(ep, i)
    }
    if (f.kind === 'network') {
      const n = peek<Network[]>(`/e/${e}/networks`)?.find((x) => x.Id === f.id)
      if (n) return networkActions(ep, n)
    }
    if (f.kind === 'volume') {
      const v = peek<Volume[]>(`/e/${e}/volumes`)?.find((x) => x.Name === f.id)
      if (v) return volumeActions(ep, v)
    }
    if (f.kind === 'service') {
      const sv = peek<Service[]>(`/e/${e}/services`)?.find((x) => x.ID === f.id)
      if (sv) return serviceActions(ep, sv)
    }
    if (f.kind === 'node') {
      const n = peek<SwarmNode[]>(`/e/${e}/nodes`)?.find((x) => x.ID === f.id)
      if (n) return nodeActions(ep, n)
    }
    return [...base, { label: tr('Remove from favorites'), onClick: () => setFavs(favs.filter((x) => x !== f)) }]
  }
  const favTarget = (f: Fav) => {
    if (f.kind === 'container') {
      const c = cts?.find((x) => cname(x) === f.id)
      return href(f.ep, 'containers', c?.Id ?? f.id)
    }
    return href(f.ep, f.kind + 's', f.id)
  }
  const myFavs = favs.filter((f) => f.ep === ep)
  const myRecent = recent.filter((r) => r.ep === ep).slice(0, 6)
  return (
    <aside className={`sidebar${collapsed ? ' collapsed' : ''}`} onContextMenu={menu(() => [
      { label: collapsed ? tr('Expand sidebar') : tr('Collapse sidebar'), onClick: () => setCollapsed(!collapsed) },
      myRecent.length > 0 && { label: tr('Clear recent'), onClick: () => setRecent(recent.filter((r) => r.ep !== ep)) },
    ])}>
      <EndpointSwitcher ep={ep} collapsed={collapsed} />
      <nav>
        {NAV.map((n) => (
          <a key={n.id} href={href(ep, n.id)} className={`nav-item${section === n.id ? ' active' : ''}`} onContextMenu={navMenu(n.id, n.label)} title={n.label}>
            <n.icon size={16} />
            {!collapsed && <span>{n.label}</span>}
            {!collapsed && counts[n.id] && <span className="nav-count">{counts[n.id]}</span>}
          </a>
        ))}
      </nav>
      {swarmNav.length > 0 && (
        <nav className="nav-section">
          {!collapsed && <div className="nav-title"><ClusterIcon size={12} /> Swarm</div>}
          {swarmNav.map((n) => (
            <a key={n.id} href={href(ep, n.id)} className={`nav-item${section === n.id ? ' active' : ''}`} onContextMenu={navMenu(n.id, n.label)} title={n.label}>
              <n.icon size={16} />
              {!collapsed && <span>{n.label}</span>}
              {!collapsed && counts[n.id] && <span className="nav-count">{counts[n.id]}</span>}
            </a>
          ))}
        </nav>
      )}
      {!collapsed && myFavs.length > 0 && (
        <div className="nav-section">
          <div className="nav-title"><Star size={12} /> {tr('Favorites')}</div>
          {myFavs.map((f) => {
            const c = f.kind === 'container' ? cts?.find((x) => cname(x) === f.id) : undefined
            return (
              <a key={f.kind + f.id} href={favTarget(f)} className="nav-item sub" onContextMenu={menu(() => favMenu(f))}>
                <span className={`dot dot-${c ? (c.State === 'running' ? 'open' : 'closed') : 'idle'}`} />
                <span className="ellipsis">{f.label}</span>
              </a>
            )
          })}
        </div>
      )}
      {!collapsed && myRecent.length > 0 && (
        <div className="nav-section">
          <div className="nav-title"><Clock size={12} /> {tr('Recent')}</div>
          {myRecent.map((r) => (
            <a key={r.section + r.id} href={href(r.ep, r.section, r.id)} className="nav-item sub" onContextMenu={menu(() => [
              { label: tr('Open'), onClick: () => go(r.ep, r.section, r.id) },
              { label: tr('Remove from recent'), onClick: () => setRecent(recent.filter((x) => x !== r)) },
            ])}>
              <span className="ellipsis">{r.label}</span>
              <span className="muted small">{SECTION_LABEL[r.section] ?? r.section}</span>
            </a>
          ))}
        </div>
      )}
      <nav className="nav-bottom">
        {NAV2.map((n) => (
          <a key={n.id} href={href(ep, n.id)} className={`nav-item${section === n.id ? ' active' : ''}`} onContextMenu={navMenu(n.id, n.label)} title={n.label}>
            <n.icon size={16} />
            {!collapsed && <span>{n.label}</span>}
          </a>
        ))}
        <button className="nav-item" onClick={() => setCollapsed(!collapsed)} title={tr('Collapse')}>
          {collapsed ? <PanelLeft size={16} /> : <PanelLeftClose size={16} />}
          {!collapsed && <span>{tr('Collapse')}</span>}
        </button>
      </nav>
    </aside>
  )
}

// ---------- command palette ----------
interface Cmd {
  id: string
  label: string
  sub?: string
  icon: React.ReactNode
  group: string
  run: () => void
  menu?: () => MenuEntry[]
}

function Palette({ ep, onClose }: { ep: string; onClose: () => void }) {
  const [q, setQ] = useState('')
  const [i, setI] = useState(0)
  const menu = useMenu()
  const list = useRef<HTMLDivElement>(null)
  const e = enc(ep)
  const cts = peek<Container[]>(`/e/${e}/containers`) || []
  const stacks = peek<Stack[]>(`/e/${e}/stacks`) || []
  const imgs = peek<Image[]>(`/e/${e}/images`) || []
  const nets = peek<Network[]>(`/e/${e}/networks`) || []
  const vols = peek<Volume[]>(`/e/${e}/volumes`) || []
  const svcs = peek<Service[]>(`/e/${e}/services`) || []
  const nodes = peek<SwarmNode[]>(`/e/${e}/nodes`) || []
  const sstacks = peek<SwarmStack[]>(`/e/${e}/swarm/stacks`) || []
  const swarmManager = !!peek<any>(`/e/${e}/swarm`)?.info?.ControlAvailable
  const eps = peek<Endpoint[]>('/endpoints') || []
  const all: Cmd[] = useMemo(() => [
    { id: 'new-c', label: tr('New container'), icon: <Plus size={15} />, group: tr('Actions'), run: () => openDialog((c) => <ContainerFormDialog ep={ep} initial={{}} mode="create" onClose={c} />) },
    { id: 'new-s', label: tr('New stack (compose)'), icon: <Plus size={15} />, group: tr('Actions'), run: () => openDialog((c) => <NewStackDialog ep={ep} onClose={c} />) },
    { id: 'pull', label: tr('Pull image'), icon: <Download size={15} />, group: tr('Actions'), run: async () => { const x = await prompt({ title: tr('Pull image'), label: tr('Image'), placeholder: 'nginx:alpine' }); if (x) pullImage(ep, x) } },
    { id: 'market', label: tr('Deploy an app from the marketplace'), icon: <Rocket size={15} />, group: tr('Actions'), run: () => go(ep, 'marketplace') },
    ...(swarmManager
      ? [
          { id: 'new-svc', label: tr('New swarm service'), icon: <Plus size={15} />, group: tr('Actions'), run: () => openDialog((c) => <NewServiceDialog ep={ep} onClose={c} />) },
          { id: 'new-sst', label: tr('New swarm stack'), icon: <Plus size={15} />, group: tr('Actions'), run: () => openDialog((c) => <NewSwarmStackDialog ep={ep} onClose={c} />) },
        ]
      : [{ id: 'swarm', label: tr('Swarm mode (initialize or join)'), icon: <ClusterIcon size={15} />, group: tr('Actions'), run: () => go(ep, 'swarm') }]),
    ...[...NAV, ...SWARM_NAV, ...NAV2].map((n) => ({ id: 'nav-' + n.id, label: tr('Go to {0}', n.label), icon: <n.icon size={15} />, group: tr('Navigation'), run: () => go(ep, n.id) })),
    ...eps.filter((x) => x.name !== ep).map((x) => ({ id: 'ep-' + x.name, label: tr('Switch to host {0}', x.name), sub: x.url, icon: <Server size={15} />, group: tr('Hosts'), run: () => go(x.name, 'jump') })),
    ...cts.map((c) => ({ id: c.Id, label: cname(c), sub: `${c.State} · ${c.Image}${stackOf(c) ? ' · ' + stackOf(c) : ''}`, icon: <Box size={15} />, group: tr('Containers'), run: () => go(ep, 'containers', c.Id), menu: () => containerActions(ep, c) })),
    ...cts.filter((c) => c.State === 'running').map((c) => ({ id: 'sh-' + c.Id, label: tr('Terminal in {0}', cname(c)), icon: <Box size={15} />, group: tr('Terminal'), run: () => go(ep, 'containers', c.Id, 'terminal') })),
    ...cts.map((c) => ({ id: 'lg-' + c.Id, label: tr('Logs of {0}', cname(c)), icon: <Box size={15} />, group: tr('Logs'), run: () => go(ep, 'containers', c.Id, 'logs') })),
    ...stacks.map((s) => ({ id: 's-' + s.name, label: s.name, sub: `${s.running}/${s.total} · ${s.status}`, icon: <Layers size={15} />, group: 'Stacks', run: () => go(ep, 'stacks', s.name), menu: () => stackActions(ep, s) })),
    ...imgs.flatMap((im) => (im.RepoTags || []).filter((t) => !t.startsWith('<none>')).map((t) => ({ id: 'i-' + t, label: t, icon: <ImageIcon size={15} />, group: tr('Images'), run: () => go(ep, 'images', im.Id), menu: () => imageActions(ep, im) }))),
    ...nets.map((n) => ({ id: 'n-' + n.Id, label: n.Name, sub: n.Driver, icon: <NetIcon size={15} />, group: tr('Networks'), run: () => go(ep, 'networks', n.Id), menu: () => networkActions(ep, n) })),
    ...svcs.map((sv) => ({ id: 'sv-' + sv.ID, label: sv.Spec.Name, sub: `${sv.ServiceStatus?.RunningTasks ?? '?'}/${sv.ServiceStatus?.DesiredTasks ?? '?'} · ${serviceImage(sv)}`, icon: <Boxes size={15} />, group: tr('Services'), run: () => go(ep, 'services', sv.ID), menu: () => serviceActions(ep, sv) })),
    ...svcs.map((sv) => ({ id: 'svl-' + sv.ID, label: tr('Logs of {0}', sv.Spec.Name), icon: <Boxes size={15} />, group: tr('Logs'), run: () => go(ep, 'services', sv.ID, 'logs') })),
    ...sstacks.map((st) => ({ id: 'ss-' + st.name, label: st.name, sub: `swarm · ${st.running}/${st.desired}`, icon: <Layers size={15} />, group: 'Stacks', run: () => go(ep, 'swarm-stacks', st.name), menu: () => swarmStackActions(ep, st) })),
    ...nodes.map((n) => ({ id: 'nd-' + n.ID, label: nodeName(n), sub: `${n.Spec.Role} · ${n.Status.State} · ${n.Spec.Availability}`, icon: <Server size={15} />, group: tr('Nodes'), run: () => go(ep, 'nodes', n.ID), menu: () => nodeActions(ep, n) })),
    ...vols.map((v) => ({ id: 'v-' + v.Name, label: v.Name, icon: <HardDrive size={15} />, group: tr('Volumes'), run: () => go(ep, 'volumes', v.Name), menu: () => volumeActions(ep, v) })),
  ], [ep])
  const results = useMemo(() => {
    const ql = q.toLowerCase().trim()
    if (!ql) return all.filter((c) => [tr('Actions'), tr('Navigation'), tr('Hosts')].includes(c.group) || (c.group === tr('Containers'))).slice(0, 40)
    const words = ql.split(/\s+/)
    return all
      .map((c) => {
        const hay = `${c.label} ${c.sub ?? ''} ${c.group}`.toLowerCase()
        if (!words.every((w) => hay.includes(w))) return null
        const score = (c.label.toLowerCase().startsWith(words[0]) ? 0 : 1) + (c.group === tr('Logs') || c.group === tr('Terminal') ? 0.5 : 0)
        return { c, score }
      })
      .filter(Boolean)
      .sort((a, b) => a!.score - b!.score)
      .slice(0, 60)
      .map((x) => x!.c)
  }, [q, all])
  useEffect(() => setI(0), [q])
  useEffect(() => {
    list.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest' })
  }, [i])
  const run = (c: Cmd) => {
    onClose()
    c.run()
  }
  let lastGroup = ''
  return (
    <div className="modal-backdrop palette-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette">
        <div className="palette-input">
          <Search size={16} />
          <input
            autoFocus
            placeholder={tr('Search containers, stacks, images… or type an action')}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowDown') (e.preventDefault(), setI(Math.min(i + 1, results.length - 1)))
              else if (e.key === 'ArrowUp') (e.preventDefault(), setI(Math.max(i - 1, 0)))
              else if (e.key === 'Enter' && results[i]) run(results[i])
              else if (e.key === 'Escape') onClose()
            }}
          />
          <Kbd>esc</Kbd>
        </div>
        <div className="palette-list" ref={list}>
          {results.length === 0 && <div className="muted center">{tr('No results')}</div>}
          {results.map((c, idx) => {
            const header = c.group !== lastGroup ? (lastGroup = c.group) : null
            return (
              <div key={c.id}>
                {header && <div className="palette-group">{header}</div>}
                <button
                  className={`palette-item${idx === i ? ' active' : ''}`}
                  onMouseEnter={() => setI(idx)}
                  onClick={() => run(c)}
                  onContextMenu={c.menu ? menu(c.menu) : undefined}
                >
                  {c.icon}
                  <span className="grow ellipsis">{c.label}</span>
                  {c.sub && <span className="muted small ellipsis">{c.sub}</span>}
                </button>
              </div>
            )
          })}
        </div>
        <div className="palette-foot muted small">
          <Kbd>↑↓</Kbd> {tr('navigate')} · <Kbd>↵</Kbd> {tr('open')} · {tr('right-click for more actions')}
        </div>
      </div>
    </div>
  )
}

function UserMenu() {
  const me = getMe()
  const menu = useMenu()
  if (!me) return null
  const label = me.method === 'token' ? 'token' : me.user
  const items = (): MenuEntry[] => [
    { header: me.method === 'token' ? tr('Token access') : tr('User {0}', me.user) },
    { label: me.role === 'admin' ? tr('Role: administrator') : tr('Role: read-only'), disabled: true },
    me.version ? { label: `ducktainer ${me.version}`, disabled: true } : null,
    'sep',
    { label: tr('Language'), icon: <Languages size={14} />, submenu: LANGUAGES.map((l) => ({ label: l.label, checked: getLang() === l.id, onClick: () => setLang(l.id) })) },
    { label: tr('Sign out'), icon: <LogOut size={14} />, onClick: () => api.post('/logout').then(() => location.reload()) },
  ]
  return (
    <button
      className="user-chip"
      onClick={(e) => {
        const r = e.currentTarget.getBoundingClientRect()
        menu(items)({ preventDefault() {}, stopPropagation() {}, clientX: r.right - 220, clientY: r.bottom + 4 } as any)
      }}
      onContextMenu={menu(items)}
    >
      <Avatar name={label} size={26} />
      <span className="user-chip-text">
        <strong>{label}</strong>
        <span className="muted small">{me.role === 'admin' ? 'admin' : tr('read-only')}</span>
      </span>
    </button>
  )
}

function Topbar({ ep, onSearch }: { ep: string; onSearch: () => void }) {
  const [theme, setTheme] = usePref<'auto' | 'light' | 'dark'>('theme', 'auto')
  const menu = useMenu()
  const swarm = useSwarm(ep)
  useEffect(() => {
    if (theme === 'auto') document.documentElement.removeAttribute('data-theme')
    else document.documentElement.setAttribute('data-theme', theme)
  }, [theme])
  const createItems = (): MenuEntry[] => [
    { label: tr('Container'), icon: <Box size={14} />, onClick: () => openDialog((c) => <ContainerFormDialog ep={ep} initial={{}} mode="create" onClose={c} />) },
    { label: 'Stack (compose)', icon: <Layers size={14} />, onClick: () => openDialog((c) => <NewStackDialog ep={ep} onClose={c} />) },
    { label: tr('From the marketplace'), icon: <Store size={14} />, onClick: () => go(ep, 'marketplace') },
    swarm.manager && 'sep',
    swarm.manager && { label: tr('Swarm service'), icon: <Boxes size={14} />, onClick: () => openDialog((c) => <NewServiceDialog ep={ep} onClose={c} />) },
    swarm.manager && { label: tr('Swarm stack'), icon: <Layers size={14} />, onClick: () => openDialog((c) => <NewSwarmStackDialog ep={ep} onClose={c} />) },
    swarm.manager && { label: 'Secret / config', icon: <KeyRound size={14} />, onClick: () => go(ep, 'secrets') },
    'sep',
    { label: tr('Pull image'), icon: <Download size={14} />, onClick: async () => { const x = await prompt({ title: tr('Pull image'), label: tr('Image'), placeholder: 'nginx:alpine' }); if (x) pullImage(ep, x) } },
    { label: tr('Network'), icon: <NetIcon size={14} />, onClick: () => go(ep, 'networks') },
    { label: tr('Host'), icon: <Server size={14} />, onClick: () => go(ep, 'settings', 'hosts') },
  ]
  return (
    <header className="topbar">
      <button className="searchbar" onClick={onSearch}>
        <Search size={15} />
        <span>{tr('Search or run…')}</span>
        <Kbd>Ctrl K</Kbd>
      </button>
      <span className="grow" />
      {isViewer() && <span className="tag tag-warn">{tr('Read-only')}</span>}
      <button
        className="btn btn-primary btn-sm needs-admin"
        onClick={(e) => {
          const r = e.currentTarget.getBoundingClientRect()
          menu(createItems)({ preventDefault() {}, stopPropagation() {}, clientX: r.left, clientY: r.bottom + 4 } as any)
        }}
      >
        <Plus size={15} /> {tr('Create')}
      </button>
      <button className="icon-btn" title={tr('Theme')} onClick={() => setTheme(theme === 'dark' ? 'light' : theme === 'light' ? 'auto' : 'dark')} onContextMenu={menu(() => [
        { label: tr('Automatic'), checked: theme === 'auto', onClick: () => setTheme('auto') },
        { label: tr('Light'), checked: theme === 'light', onClick: () => setTheme('light') },
        { label: tr('Dark'), checked: theme === 'dark', onClick: () => setTheme('dark') },
      ])}>
        {theme === 'dark' ? <Moon size={16} /> : theme === 'light' ? <Sun size={16} /> : <Check size={16} />}
      </button>
      <UserMenu />
    </header>
  )
}

function Shell() {
  const route = useRoute()
  const { data: endpoints, error } = useResource<Endpoint[]>('/endpoints')
  const [collapsed, setCollapsed] = usePref('sidebar.collapsed', false)
  const [palette, setPalette] = useState(false)
  const [, setRecent] = usePref<Recent[]>('recent', [])
  const ep = route.ep || endpoints?.[0]?.name || ''
  const menu = useMenu()
  useLiveEvents(ep)

  useEffect(() => {
    if (!route.ep && endpoints?.length) location.replace(href(endpoints[0].name, 'jump'))
  }, [route.ep, endpoints])

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPalette((p) => !p)
      } else if (e.key === '/' && !(e.target as HTMLElement).closest('input,textarea,select,.xterm')) {
        e.preventDefault()
        setPalette(true)
      }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [])

  useEffect(() => {
    if (!route.id || !RECORD_SECTIONS.includes(route.section)) return
    const t = setTimeout(() => {
      const label = labelFor(ep, route.section, route.id!)
      setRecent((list) => [{ ep, section: route.section, id: route.id!, label }, ...list.filter((r) => !(r.ep === ep && r.section === route.section && r.id === route.id))].slice(0, 20))
    }, 800)
    return () => clearTimeout(t)
  }, [ep, route.section, route.id])

  if (error) return <div className="center page"><div className="alert alert-bad">{error.message}</div></div>
  if (!ep) return <div className="center page"><Spinner /></div>

  const { section, id, tab, query } = route
  let page: React.ReactNode
  switch (section) {
    case 'containers':
      page = id ? <ContainerRecord ep={ep} id={id} tab={tab} query={query} /> : <Containers ep={ep} />
      break
    case 'stacks':
      page = id ? <StackRecord ep={ep} name={id} tab={tab} /> : <Stacks ep={ep} />
      break
    case 'images':
      page = id ? <ImageRecord ep={ep} id={id} /> : <Images ep={ep} />
      break
    case 'networks':
      page = id ? <NetworkRecord ep={ep} id={id} /> : <Networks ep={ep} />
      break
    case 'volumes':
      page = id ? <VolumeRecord ep={ep} name={id} /> : <Volumes ep={ep} />
      break
    case 'swarm':
      page = <SwarmOverview ep={ep} />
      break
    case 'services':
      page = id ? <ServiceRecord ep={ep} id={id} tab={tab} /> : <Services ep={ep} />
      break
    case 'nodes':
      page = id ? <NodeRecord ep={ep} id={id} /> : <Nodes ep={ep} />
      break
    case 'secrets':
    case 'configs':
      page = id ? <SwarmObjectRecord ep={ep} kind={section} id={id} /> : <SwarmObjects ep={ep} kind={section} />
      break
    case 'swarm-stacks':
      page = id ? <SwarmStackRecord ep={ep} name={id} tab={tab} /> : <SwarmStacks ep={ep} />
      break
    case 'marketplace':
      page = <Marketplace ep={ep} />
      break
    case 'jump':
      page = <Jump ep={ep} />
      break
    case 'home':
      page = <Home ep={ep} />
      break
    case 'activity':
      page = <Activity ep={ep} />
      break
    case 'alerts':
      page = <Alerts tab={id} />
      break
    case 'settings':
      page = <Settings ep={ep} tab={id} />
      break
    default:
      page = <Jump ep={ep} />
  }

  return (
    <div
      className={`app${isViewer() ? ' viewer' : ''}`}
      onContextMenu={(e) => {
        // fallback menu for any empty spot that has no menu of its own
        if ((e.target as HTMLElement).closest('input,textarea,select,.xterm,pre,code')) return
        menu(() => [
          { label: tr('Search or run…'), icon: <Search size={14} />, hint: 'Ctrl K', onClick: () => setPalette(true) },
          { label: tr('Reload data'), onClick: () => invalidate() },
          'sep',
          { label: tr('New container…'), icon: <Box size={14} />, onClick: () => openDialog((c) => <ContainerFormDialog ep={ep} initial={{}} mode="create" onClose={c} />) },
          { label: tr('New stack…'), icon: <Layers size={14} />, onClick: () => openDialog((c) => <NewStackDialog ep={ep} onClose={c} />) },
          'sep',
          { label: tr('Go to'), submenu: [...NAV, ...SWARM_NAV, ...NAV2].map((n) => ({ label: n.label, icon: <n.icon size={14} />, onClick: () => go(ep, n.id) })) },
          { label: tr('Back'), onClick: () => history.back() },
        ])(e)
      }}
    >
      <Sidebar ep={ep} section={section} collapsed={collapsed} setCollapsed={setCollapsed} />
      <div className="main">
        <Topbar ep={ep} onSearch={() => setPalette(true)} />
        <div className="content" key={ep}>{page}</div>
      </div>
      {palette && <Palette ep={ep} onClose={() => setPalette(false)} />}
    </div>
  )
}

export function App() {
  const [auth, setAuth] = useState<'checking' | 'in' | 'out'>('checking')
  const check = () =>
    api
      .get<Me>('/me')
      .then((m) => (setMe(m), setAuth('in')))
      .catch(() => (setMe(null), setAuth('out')))
  useEffect(() => {
    setUnauthorizedHandler(() => (setMe(null), setAuth('out')))
    check()
  }, [])
  return (
    <ContextMenuProvider>
      {auth === 'checking' && <div className="center page"><Spinner /></div>}
      {auth === 'out' && <Login onDone={() => check().then(() => toast.ok(getMe()?.method === 'token' ? tr('Signed in with the token') : `Hola, ${getMe()?.user}`))} />}
      {auth === 'in' && <Shell />}
      <OverlayHost />
    </ContextMenuProvider>
  )
}
