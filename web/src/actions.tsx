import { t as tr, tc } from './i18n'
// Every object's actions live here, so context menus, "⋯" buttons, bulk
// bars and the command palette all offer the same things.
import {
  Play, Square, RotateCw, Pause, Zap, Terminal, ScrollText, FolderOpen, Pencil, Copy, Trash2, Star, ExternalLink,
  Network as NetIcon, Layers, Box, HardDrive, Download, Upload, Tag as TagIcon, Image as ImageIcon, Eye, FileCode2,
  PackagePlus, Unplug, PlugZap, RefreshCw, Activity, Save, ArrowUpCircle, ClipboardCopy, Rocket,
  Server, Boxes, KeyRound, FileText, Scaling, Undo2, ShieldCheck, ArrowDownCircle, Tags, ListChecks,
} from 'lucide-react'
import { api, enc } from './lib/api'
import { cname, copy, imageRepo, shortId, stackOf, uniquePorts } from './lib/format'
import { go } from './lib/router'
import { invalidate, peek } from './lib/store'
import type { Container, Image, Network, Service, Stack, SwarmNode, SwarmObject, SwarmStack, Volume } from './lib/types'
import type { MenuEntry } from './ui/ContextMenu'
import { confirm, openDialog, prompt, runJob, toast } from './ui/overlays'
import { ContainerFormDialog } from './components/ContainerForm'
import { askImage } from './components/ImagePicker'
import { LocationDialog } from './pages/Stacks'
import { setPref, getPref } from './lib/prefs'

const I = 15

async function act(p: Promise<any>, ok?: string) {
  try {
    await p
    if (ok) toast.ok(ok)
  } catch (e) {
    toast.err(e)
  } finally {
    invalidate('/containers', '/stacks', '/networks', '/volumes', '/images', '/info', '/services', '/nodes', '/secrets', '/configs', '/swarm')
  }
}

// ---------- favorites ----------
export type FavKind = 'container' | 'stack' | 'image' | 'network' | 'volume' | 'service' | 'node'
export interface Fav {
  ep: string
  kind: FavKind
  id: string
  label: string
}
export function isFav(ep: string, kind: FavKind, id: string) {
  return getPref<Fav[]>('favorites', []).some((f) => f.ep === ep && f.kind === kind && f.id === id)
}
export function toggleFav(ep: string, kind: FavKind, id: string, label: string): MenuEntry {
  const on = isFav(ep, kind, id)
  return {
    label: on ? tr('Remove from favorites') : tr('Add to favorites'),
    icon: <Star size={I} fill={on ? 'currentColor' : 'none'} />,
    onClick: () =>
      setPref<Fav[]>('favorites', (list) =>
        on ? list.filter((f) => !(f.ep === ep && f.kind === kind && f.id === id)) : [...list, { ep, kind, id, label }],
      []),
  }
}

// ---------- host for "open port" ----------
export function endpointHost(ep: string) {
  const eps = peek<any[]>('/endpoints') || []
  const e = eps.find((x) => x.name === ep)
  if (!e || e.url.startsWith('unix://')) return location.hostname
  try {
    return new URL(e.url.replace(/^(tcp|ssh):/, 'http:')).hostname
  } catch {
    return location.hostname
  }
}

// ---------- containers ----------
export function containerActions(ep: string, c: Container, opts: { preview?: () => void } = {}): MenuEntry[] {
  const name = cname(c)
  const id = c.Id
  const base = `/e/${enc(ep)}/containers/${enc(id)}`
  const running = c.State === 'running'
  const paused = c.State === 'paused'
  const stack = stackOf(c)
  const ports = uniquePorts(c)
  const host = endpointHost(ep)
  const nets = Object.keys(c.NetworkSettings?.Networks || {})
  const allNets = (peek<Network[]>(`/e/${enc(ep)}/networks`) || []).filter((n) => !nets.includes(n.Name) && !['host', 'none'].includes(n.Name))
  const action = (a: string, label: string, q = '') => () => act(api.post(`${base}/action/${a}${q}`), `${name}: ${label}`)
  return [
    { header: name },
    { label: tr('Open record'), icon: <Box size={I} />, hint: '↵', onClick: () => go(ep, 'containers', id) },
    opts.preview && { label: tr('Quick view'), icon: <Eye size={I} />, onClick: opts.preview },
    'sep',
    paused
      ? { label: tr('Resume'), icon: <Play size={I} />, onClick: action('unpause', tr('resumed')) }
      : { label: tr('Start'), icon: <Play size={I} />, disabled: running, onClick: action('start', tr('started')) },
    { label: tr('Stop'), icon: <Square size={I} />, disabled: !running && !paused, onClick: action('stop', tr('stopped')) },
    { label: tr('Restart'), icon: <RotateCw size={I} />, onClick: action('restart', tr('restarted')) },
    { label: tr('Pause'), icon: <Pause size={I} />, disabled: !running, onClick: action('pause', tr('paused')) },
    {
      disabled: !running && !paused,
      label: tr('Send signal'),
      icon: <Zap size={I} />,
      submenu: ['SIGKILL', 'SIGTERM', 'SIGHUP', 'SIGINT', 'SIGUSR1', 'SIGUSR2'].map((s) => ({
        label: s,
        danger: s === 'SIGKILL',
        onClick: action('kill', s, `?signal=${s}`),
      })),
    },
    'sep',
    { label: tr('Terminal'), icon: <Terminal size={I} />, disabled: !running, onClick: () => go(ep, 'containers', id, 'terminal') },
    { label: tr('Live logs'), icon: <ScrollText size={I} />, onClick: () => go(ep, 'containers', id, 'logs') },
    { label: tr('Browse files'), icon: <FolderOpen size={I} />, onClick: () => go(ep, 'containers', id, 'files') },
    { label: tr('Performance'), icon: <Activity size={I} />, disabled: !running, onClick: () => go(ep, 'containers', id, 'stats') },
    'sep',
    { label: tr('Edit configuration'), icon: <Pencil size={I} />, onClick: () => go(ep, 'containers', id, 'config') },
    {
      label: tr('Update image and recreate'),
      icon: <ArrowUpCircle size={I} />,
      onClick: async () => {
        try {
          const spec = await api.get(`${base}/spec`)
          const image = await askImage({
            ep,
            title: tr('Update {0}', name),
            value: spec.image || c.Image,
            confirm: tr('Update'),
            help: tr('This image and tag will be pulled and the container recreated, keeping its configuration and volumes. Change the tag to move to another version.'),
          })
          if (!image) return
          await runJob(tr('Update {0}', name), 'POST', `${base}/recreate`, { spec: { ...spec, image }, pull: true })
        } catch (e) {
          toast.err(e)
        }
        invalidate('/containers')
      },
    },
    {
      label: tr('Duplicate…'),
      icon: <Copy size={I} />,
      onClick: async () => {
        try {
          const spec = await api.get(`${base}/spec`)
          spec.name = name + '-copy'
          spec.ports = []
          openDialog((close) => <ContainerFormDialog ep={ep} initial={spec} mode="duplicate" sourceId={id} onClose={close} />)
        } catch (e) {
          toast.err(e)
        }
      },
    },
    {
      label: tr('Rename…'),
      icon: <Pencil size={I} />,
      onClick: async () => {
        const n = await prompt({ title: tr('Rename container'), label: tr('New name'), value: name, confirm: tr('Rename') })
        if (n && n !== name) act(api.post(`${base}/rename`, { name: n }), tr('Renamed to {0}', n))
      },
    },
    {
      label: tr('Save as image…'),
      icon: <Save size={I} />,
      onClick: async () => {
        const ref = await prompt({ title: tr('Commit container'), label: tr('Target image (repo:tag)'), value: `${name}:snapshot`, confirm: tr('Create image'), help: tr('Creates an image from the current state of the container\'s filesystem.') })
        if (!ref) return
        act(api.post(`${base}/commit`, { repo: imageRepo(ref), tag: ref.slice(imageRepo(ref).length + 1) || 'latest' }), tr('Image {0} created', ref))
      },
    },
    'sep',
    {
      label: tr('Open port'),
      icon: <ExternalLink size={I} />,
      disabled: ports.length === 0,
      hint: ports.length === 0 ? tr('no ports') : undefined,
      submenu: ports.map((p) => ({
        label: `${p.PublicPort} → ${p.PrivatePort}/${p.Type}`,
        hint: p.IP && p.IP !== '0.0.0.0' && p.IP !== '::' ? p.IP : undefined,
        onClick: () => window.open(`${p.PrivatePort === 443 ? 'https' : 'http'}://${host}:${p.PublicPort}`, '_blank'),
      })),
    },
    {
      label: tr('Networks'),
      icon: <NetIcon size={I} />,
      submenu: [
        ...nets.map((n) => ({
          label: n,
          submenu: [
            { label: tr('View network'), icon: <Eye size={I} />, onClick: () => go(ep, 'networks', c.NetworkSettings.Networks[n].NetworkID) },
            { label: tr('Copy IP'), icon: <ClipboardCopy size={I} />, hint: c.NetworkSettings.Networks[n].IPAddress, onClick: () => copy(c.NetworkSettings.Networks[n].IPAddress) },
            { label: tr('Disconnect'), icon: <Unplug size={I} />, danger: true, onClick: () => act(api.post(`/e/${enc(ep)}/networks/${enc(n)}/disconnect`, { container: id }), tr('{0} disconnected from {1}', name, n)) },
          ],
        })),
        allNets.length > 0 && 'sep',
        allNets.length > 0 && {
          label: tr('Connect to…'),
          icon: <PlugZap size={I} />,
          submenu: allNets.map((n) => ({ label: n.Name, hint: n.Driver, onClick: () => act(api.post(`/e/${enc(ep)}/networks/${enc(n.Id)}/connect`, { container: id }), tr('{0} connected to {1}', name, n.Name)) })),
        },
      ],
    },
    {
      label: tr('Copy'),
      icon: <ClipboardCopy size={I} />,
      submenu: [
        { label: tr('Name'), onClick: () => copy(name) },
        { label: 'ID', hint: shortId(id), onClick: () => copy(id) },
        { label: tr('Image'), onClick: () => copy(c.Image) },
        ...Object.entries(c.NetworkSettings?.Networks || {})
          .filter(([, n]) => n.IPAddress)
          .map(([k, n]) => ({ label: `IP (${k})`, hint: n.IPAddress, onClick: () => copy(n.IPAddress) })),
        'sep',
        { label: tr('docker exec command'), onClick: () => copy(`docker exec -it ${name} sh`) },
        { label: tr('ducktainer sh command'), onClick: () => copy(`ducktainer sh -e ${ep} ${name}`) },
        { label: tr('docker logs command'), onClick: () => copy(`docker logs -f --tail 200 ${name}`) },
      ],
    },
    'sep',
    c.Labels?.['com.docker.swarm.service.id']
      ? { label: tr('Go to service {0}', c.Labels['com.docker.swarm.service.name']), icon: <Boxes size={I} />, onClick: () => go(ep, 'services', c.Labels['com.docker.swarm.service.id']) }
      : { label: stack ? tr('Go to stack {0}', stack) : tr('Go to stack'), icon: <Layers size={I} />, disabled: !stack, hint: stack ? undefined : 'suelto', onClick: () => go(ep, 'stacks', stack) },
    { label: tr('Go to image'), icon: <ImageIcon size={I} />, onClick: () => go(ep, 'images', c.ImageID) },
    toggleFav(ep, 'container', name, name),
    'sep',
    {
      label: tr('Delete…'),
      icon: <Trash2 size={I} />,
      danger: true,
      onClick: () => removeContainers(ep, [c]),
    },
  ]
}

export async function removeContainers(ep: string, list: Container[]) {
  const names = list.map(cname)
  const ok = await confirm({
    title: list.length === 1 ? tr('Delete {0}', names[0]) : tr('Delete {0} containers', list.length),
    body: list.length === 1 ? tr('The container will be stopped and deleted.') : names.join(', '),
    danger: true,
    confirm: tr('Delete'),
    check: tr('Also delete its anonymous volumes'),
  })
  if (!ok) return
  await act(Promise.all(list.map((c) => api.del(`/e/${enc(ep)}/containers/${enc(c.Id)}?force=1&v=${ok.checked ? 1 : 0}`))), list.length === 1 ? tr('{0} deleted', names[0]) : tr('{0} containers deleted', list.length))
}

export function bulkContainerActions(ep: string, list: Container[]): MenuEntry[] {
  const each = (a: string, label: string) => () =>
    act(Promise.all(list.map((c) => api.post(`/e/${enc(ep)}/containers/${enc(c.Id)}/action/${a}`))), tr('{0} containers: {1}', list.length, label))
  return [
    { label: tr('Start all'), icon: <Play size={I} />, onClick: each('start', tc('pl', 'started')) },
    { label: tr('Stop all'), icon: <Square size={I} />, onClick: each('stop', tc('pl', 'stopped')) },
    { label: tr('Restart all'), icon: <RotateCw size={I} />, onClick: each('restart', tc('pl', 'restarted')) },
    { label: tr('Pause all'), icon: <Pause size={I} />, onClick: each('pause', tc('pl', 'paused')) },
    'sep',
    { label: tr('Copy names'), icon: <ClipboardCopy size={I} />, onClick: () => copy(list.map(cname).join('\n')) },
    'sep',
    { label: tr('Delete {0}…', list.length), icon: <Trash2 size={I} />, danger: true, onClick: () => removeContainers(ep, list) },
  ]
}

// ---------- stacks ----------
export function stackRun(ep: string, name: string, action: string, label: string) {
  return runJob(`${name}: ${label}`, 'POST', `/e/${enc(ep)}/stacks/${enc(name)}/action/${action}`)
    .catch((e) => toast.err(e))
    .finally(() => invalidate('/containers', '/stacks'))
}

export function stackActions(ep: string, s: Stack): MenuEntry[] {
  // Same entries for every stack; what doesn't apply is disabled with the reason.
  const any = s.total > 0
  const multi = (s.config_files?.length ?? 0) > 1
  const noFiles = s.editable ? undefined : tr('no files')
  return [
    { header: `stack ${s.name}` },
    { label: tr('Open record'), icon: <Layers size={I} />, onClick: () => go(ep, 'stacks', s.name) },
    { label: s.editable ? tr('Edit compose') : tr('View compose'), icon: <FileCode2 size={I} />, hint: multi ? tr('{0} files', s.config_files!.length) : undefined, onClick: () => go(ep, 'stacks', s.name, 'compose') },
    { label: tr('Combined logs'), icon: <ScrollText size={I} />, disabled: !any, onClick: () => go(ep, 'stacks', s.name, 'logs') },
    'sep',
    { label: tr('Deploy (up -d)'), icon: <Rocket size={I} />, disabled: !s.editable, hint: noFiles, onClick: () => stackRun(ep, s.name, 'up', 'up') },
    { label: tr('Update images and redeploy'), icon: <RefreshCw size={I} />, disabled: !s.editable, hint: noFiles, onClick: () => stackRun(ep, s.name, 'redeploy', 'redeploy') },
    { label: tr('Pull images'), icon: <Download size={I} />, disabled: !s.editable, hint: noFiles, onClick: () => stackRun(ep, s.name, 'pull', 'pull') },
    { label: tr('Start'), icon: <Play size={I} />, disabled: !any || s.running === s.total, onClick: () => stackRun(ep, s.name, 'start', 'start') },
    { label: tr('Stop'), icon: <Square size={I} />, disabled: s.running === 0, onClick: () => stackRun(ep, s.name, 'stop', 'stop') },
    { label: tr('Restart'), icon: <RotateCw size={I} />, disabled: !any, onClick: () => stackRun(ep, s.name, 'restart', 'restart') },
    { label: tr('Pause'), icon: <Pause size={I} />, disabled: s.running === 0, onClick: () => stackRun(ep, s.name, 'pause', 'pause') },
    'sep',
    {
      label: tr('Services'),
      icon: <Box size={I} />,
      disabled: s.containers.length === 0,
      submenu: s.containers.map((c) => ({ label: c.service || c.name, hint: c.state, onClick: () => go(ep, 'containers', c.id) })),
    },
    {
      label: tr('Files'),
      icon: <FileCode2 size={I} />,
      submenu: [
        ...(s.config_files || []).map((f) => ({ label: f.split('/').pop()!, hint: tr('copy path'), onClick: () => copy(f) })),
        s.working_dir && { label: tr('Copy directory'), onClick: () => copy(s.working_dir!) },
        multi && 'sep',
        multi && { label: tr('View merged result'), disabled: !s.editable, onClick: () => go(ep, 'stacks', s.name, 'compose') },
        multi && !s.managed && { label: tr('Merge into a single file…'), disabled: !s.editable, onClick: () => mergeStack(ep, s) },
        s.managed && 'sep',
        s.managed && { label: s.location ? tr('Change location…') : tr('Move to a directory…'), onClick: () => openDialog((close) => <LocationDialog ep={ep} stack={s} onClose={close} />) },
      ],
    },
    !s.managed && {
      label: tr('Adopt into ducktainer…'),
      icon: <PackagePlus size={I} />,
      onClick: async () => {
        if (!s.editable) return go(ep, 'stacks', s.name, 'compose')
        if (!(await confirm({ title: tr('Adopt {0}', s.name), body: (multi ? tr('The compose (merging its files) will be copied to the ducktainer store. Note: relative paths (./data) will then resolve from the ducktainer directory.') : tr('ducktainer will track the project in place ({0}): its files stay where they are and relative paths keep working.', s.working_dir || '')), confirm: tr('Adopt') }))) return
        act(api.post(`/e/${enc(ep)}/stacks/${enc(s.name)}/adopt`, {}), tr('{0} adopted', s.name))
      },
    },
    toggleFav(ep, 'stack', s.name, s.name),
    'sep',
    {
      label: tr('Take down (down)…'),
      icon: <Square size={I} />,
      danger: true,
      disabled: !any,
      onClick: async () => {
        if (await confirm({ title: `docker compose down ${s.name}`, body: tr('Stops and removes the stack\'s containers and networks. Volumes are kept.'), danger: true, confirm: tr('Take down') }))
          stackRun(ep, s.name, 'down', 'down')
      },
    },
    {
      label: tr('Delete stack…'),
      icon: <Trash2 size={I} />,
      danger: true,
      onClick: async () => {
        const ok = await confirm({ title: tr('Delete {0}', s.name), body: s.managed && !s.location ? tr('The containers will be taken down and the stack files stored in ducktainer deleted.') : tr('The containers will be taken down (the project files are not touched).'), danger: true, confirm: tr('Delete'), check: tr('Also delete the volumes') })
        if (!ok) return
        runJob(tr('Delete {0}', s.name), 'DELETE', `/e/${enc(ep)}/stacks/${enc(s.name)}?volumes=${ok.checked ? 1 : 0}`)
          .then(() => go(ep, 'stacks'))
          .catch((e) => toast.err(e))
          .finally(() => invalidate('/stacks', '/containers'))
      },
    },
  ]
}

export async function mergeStack(ep: string, s: Stack) {
  const names = (s.config_files || []).map((f) => f.split('/').pop())
  const ok = await confirm({
    title: tr('Merge files of {0}', s.name),
    body: tr('{0} will be merged into {1} (with .ducktainer-bak copies of the originals). Variables are kept; comments and formatting are lost. Containers are not recreated.', names.join(' + '), names[0]),
    confirm: tr('Merge'),
  })
  if (ok) act(api.post(`/e/${enc(ep)}/stacks/${enc(s.name)}/merge`), tr('{0}: files merged', s.name))
}

// ---------- images ----------
export function pullImage(ep: string, image: string) {
  return runJob(`Pull ${image}`, 'POST', `/e/${enc(ep)}/images/pull`, { image })
    .catch((e) => toast.err(e))
    .finally(() => invalidate('/images'))
}

export function imageActions(ep: string, im: Image): MenuEntry[] {
  const tags = (im.RepoTags || []).filter((t) => t !== '<none>:<none>')
  const ref = tags[0] || shortId(im.Id)
  return [
    { header: ref },
    { label: tr('Open record'), icon: <ImageIcon size={I} />, onClick: () => go(ep, 'images', im.Id) },
    {
      label: tr('Create container…'),
      icon: <PackagePlus size={I} />,
      onClick: () => openDialog((close) => <ContainerFormDialog ep={ep} initial={{ image: ref, name: '' }} mode="create" onClose={close} />),
    },
    'sep',
    tags.length > 0 && {
      label: tr('Pull (update)'),
      icon: <Download size={I} />,
      submenu: tags.map((t) => ({ label: t, onClick: () => pullImage(ep, t) })),
    },
    tags.length > 0 && {
      label: 'Push',
      icon: <Upload size={I} />,
      submenu: tags.map((t) => ({
        label: t,
        onClick: () => runJob(`Push ${t}`, 'POST', `/e/${enc(ep)}/images/push`, { image: t }).catch((e) => toast.err(e)),
      })),
    },
    {
      label: tr('Tag…'),
      icon: <TagIcon size={I} />,
      onClick: async () => {
        const t = await prompt({ title: tr('New tag'), label: 'repo:tag', value: tags[0] ? imageRepo(tags[0]) + ':' : '', placeholder: 'registry.example.com/app:1.0', confirm: tr('Tag'), help: tr('To push to another registry, tag with its domain in front.') })
        if (t) act(api.post(`/e/${enc(ep)}/images/tag`, { source: im.Id, target: t }), tr('Tagged as {0}', t))
      },
    },
    'sep',
    {
      label: tr('Copy'),
      icon: <ClipboardCopy size={I} />,
      submenu: [
        ...tags.map((t) => ({ label: t, onClick: () => copy(t) })),
        { label: 'ID', hint: shortId(im.Id), onClick: () => copy(im.Id) },
        ...(im.RepoDigests || []).map((d) => ({ label: 'Digest', hint: d.split('@')[1]?.slice(7, 19), onClick: () => copy(d) })),
      ],
    },
    toggleFav(ep, 'image', im.Id, ref),
    'sep',
    tags.length > 1 && {
      label: tr('Remove tag'),
      icon: <TagIcon size={I} />,
      submenu: tags.map((t) => ({ label: t, danger: true, onClick: () => act(api.del(`/e/${enc(ep)}/images/${t}`), tr('Tag {0} removed', t)) })),
    },
    {
      label: tr('Delete image…'),
      icon: <Trash2 size={I} />,
      danger: true,
      onClick: () => removeImages(ep, [im]),
    },
  ]
}

export async function removeImages(ep: string, list: Image[]) {
  const ok = await confirm({
    title: list.length === 1 ? tr('Delete {0}', (list[0].RepoTags || [])[0] || shortId(list[0].Id)) : tr('Delete {0} images', list.length),
    danger: true,
    confirm: tr('Delete'),
    check: tr('Force (even if stopped containers use it)'),
  })
  if (!ok) return
  act(Promise.all(list.map((im) => api.del(`/e/${enc(ep)}/images/${im.Id}?force=${ok.checked ? 1 : 0}`))), tr('Images deleted'))
}

// ---------- networks ----------
const builtinNets = ['bridge', 'host', 'none']

export function networkActions(ep: string, n: Network): MenuEntry[] {
  const containers = (peek<Container[]>(`/e/${enc(ep)}/containers`) || []).filter((c) => !n.Attached.some((a) => a.id === c.Id))
  return [
    { header: tr('network {0}', n.Name) },
    { label: tr('Open record'), icon: <NetIcon size={I} />, onClick: () => go(ep, 'networks', n.Id) },
    'sep',
    n.Name !== 'host' && n.Name !== 'none' && containers.length > 0 && {
      label: tr('Connect container'),
      icon: <PlugZap size={I} />,
      submenu: containers.slice(0, 60).map((c) => ({ label: cname(c), hint: c.State === 'running' ? '●' : '', onClick: () => act(api.post(`/e/${enc(ep)}/networks/${enc(n.Id)}/connect`, { container: c.Id }), tr('{0} connected', cname(c))) })),
    },
    n.Attached.length > 0 && {
      label: tr('Disconnect'),
      icon: <Unplug size={I} />,
      submenu: n.Attached.map((a) => ({ label: a.name, hint: a.ipv4?.split('/')[0], onClick: () => act(api.post(`/e/${enc(ep)}/networks/${enc(n.Id)}/disconnect`, { container: a.id, force: true }), tr('{0} disconnected', a.name)) })),
    },
    n.Attached.length > 0 && {
      label: tr('Go to container'),
      icon: <Box size={I} />,
      submenu: n.Attached.map((a) => ({ label: a.name, onClick: () => go(ep, 'containers', a.id) })),
    },
    'sep',
    {
      label: tr('Copy'),
      icon: <ClipboardCopy size={I} />,
      submenu: [
        { label: tr('Name'), onClick: () => copy(n.Name) },
        { label: 'ID', hint: shortId(n.Id), onClick: () => copy(n.Id) },
        ...(n.IPAM?.Config || []).map((c) => ({ label: tr('Subnet'), hint: c.Subnet, onClick: () => copy(c.Subnet || '') })),
      ],
    },
    toggleFav(ep, 'network', n.Id, n.Name),
    'sep',
    !builtinNets.includes(n.Name) && {
      label: tr('Delete network…'),
      icon: <Trash2 size={I} />,
      danger: true,
      onClick: async () => {
        if (await confirm({ title: tr('Delete network {0}', n.Name), body: n.Attached.length ? tr('It has {0} connected containers; disconnect them first.', n.Attached.length) : undefined, danger: true, confirm: tr('Delete') }))
          act(api.del(`/e/${enc(ep)}/networks/${enc(n.Id)}`), tr('Network {0} deleted', n.Name))
      },
    },
  ]
}

// ---------- volumes ----------
export function volumeActions(ep: string, v: Volume): MenuEntry[] {
  return [
    { header: tr('volume {0}', v.Name.length > 24 ? v.Name.slice(0, 24) + '…' : v.Name) },
    { label: tr('Open record'), icon: <HardDrive size={I} />, onClick: () => go(ep, 'volumes', v.Name) },
    v.UsedBy.length > 0 && {
      label: tr('Used by'),
      icon: <Box size={I} />,
      submenu: v.UsedBy.map((a) => ({ label: a.name, hint: a.dest, onClick: () => go(ep, 'containers', a.id) })),
    },
    v.UsedBy.some((a) => a.state === 'running') && {
      label: tr('Browse content'),
      icon: <FolderOpen size={I} />,
      onClick: () => {
        const a = v.UsedBy.find((x) => x.state === 'running')!
        location.hash = `#/${enc(ep)}/containers/${enc(a.id)}/files?path=${enc(a.dest || '/')}`
      },
    },
    'sep',
    {
      label: tr('Copy'),
      icon: <ClipboardCopy size={I} />,
      submenu: [
        { label: tr('Name'), onClick: () => copy(v.Name) },
        { label: tr('Mount point'), hint: 'host', onClick: () => copy(v.Mountpoint) },
      ],
    },
    toggleFav(ep, 'volume', v.Name, v.Name),
    'sep',
    {
      label: tr('Delete volume…'),
      icon: <Trash2 size={I} />,
      danger: true,
      disabled: v.UsedBy.length > 0,
      hint: v.UsedBy.length ? tr('in use') : undefined,
      onClick: async () => {
        if (await confirm({ title: tr('Delete volume {0}', v.Name), body: tr('The data will be lost permanently.'), danger: true, confirm: tr('Delete') }))
          act(api.del(`/e/${enc(ep)}/volumes/${enc(v.Name)}`), tr('Volume deleted'))
      },
    },
  ]
}

// ---------- swarm ----------
export const stackOfService = (sv: Service) => sv.Spec.Labels?.['com.docker.stack.namespace'] || ''
export const serviceImage = (sv: Service) => sv.Spec.TaskTemplate.ContainerSpec.Image.split('@')[0]
export const serviceMode = (sv: Service) => (sv.Spec.Mode.Global ? 'global' : sv.Spec.Mode.ReplicatedJob ? 'job' : sv.Spec.Mode.GlobalJob ? 'global job' : 'replicated')
export const publishedPorts = (sv: Service) => (sv.Endpoint?.Ports || sv.Spec.EndpointSpec?.Ports || []).filter((p) => p.PublishedPort)

function swarmAct(p: Promise<any>, ok?: string) {
  return act(
    p.then((r) => {
      for (const w of r?.warnings || []) toast.info(w)
      return r
    }),
    ok,
  )
}

export async function scaleService(ep: string, sv: Service) {
  const v = await prompt({ title: tr('Scale {0}', sv.Spec.Name), label: tr('Replicas'), value: String(sv.Spec.Mode.Replicated?.Replicas ?? 1), confirm: tr('Scale') })
  if (v === null) return
  const n = Number(v)
  if (!Number.isInteger(n) || n < 0) return toast.err(tr('Enter a whole number of replicas'))
  swarmAct(api.post(`/e/${enc(ep)}/services/${enc(sv.ID)}/action/scale`, { replicas: n }), tr('{0} scaled to {1}', sv.Spec.Name, n))
}

export async function updateServiceImage(ep: string, sv: Service) {
  const img = await prompt({ title: tr('Update image of {0}', sv.Spec.Name), label: tr('Image'), value: serviceImage(sv), confirm: tr('Update'), help: tr('The tasks are replaced following the service\'s update policy (rolling update).') })
  if (img) swarmAct(api.post(`/e/${enc(ep)}/services/${enc(sv.ID)}/action/image`, { image: img }), tr('{0}: updating to {1}', sv.Spec.Name, img))
}

export async function removeServices(ep: string, list: Service[]) {
  const ok = await confirm({
    title: list.length === 1 ? tr('Delete service {0}', list[0].Spec.Name) : tr('Delete {0} services', list.length),
    body: tr('All its tasks are stopped and removed. Volumes are kept.'),
    danger: true,
    confirm: tr('Delete'),
  })
  if (ok) act(Promise.all(list.map((sv) => api.del(`/e/${enc(ep)}/services/${enc(sv.ID)}`))), list.length === 1 ? tr('{0} deleted', list[0].Spec.Name) : tr('{0} services deleted', list.length))
}

export function serviceActions(ep: string, sv: Service): MenuEntry[] {
  const name = sv.Spec.Name
  const base = `/e/${enc(ep)}/services/${enc(sv.ID)}`
  const replicated = !!sv.Spec.Mode.Replicated
  const ports = publishedPorts(sv)
  const host = endpointHost(ep)
  const stack = stackOfService(sv)
  return [
    { header: tr('service {0}', name) },
    { label: tr('Open record'), icon: <Boxes size={I} />, onClick: () => go(ep, 'services', sv.ID) },
    { label: tr('Live logs'), icon: <ScrollText size={I} />, onClick: () => go(ep, 'services', sv.ID, 'logs') },
    { label: tr('Tasks'), icon: <ListChecks size={I} />, onClick: () => go(ep, 'services', sv.ID, 'tasks') },
    'sep',
    { label: tr('Scale…'), icon: <Scaling size={I} />, disabled: !replicated, hint: replicated ? `${sv.Spec.Mode.Replicated!.Replicas}` : serviceMode(sv), onClick: () => scaleService(ep, sv) },
    { label: tr('Restart (force update)'), icon: <RotateCw size={I} />, onClick: () => swarmAct(api.post(`${base}/action/restart`), tr('{0}: restarting tasks', name)) },
    { label: tr('Update image…'), icon: <ArrowUpCircle size={I} />, onClick: () => updateServiceImage(ep, sv) },
    {
      label: tr('Roll back to the previous version'),
      icon: <Undo2 size={I} />,
      disabled: !sv.PreviousSpec,
      hint: sv.PreviousSpec ? undefined : tr('no previous version'),
      onClick: async () => {
        if (await confirm({ title: tr('Roll back {0}', name), body: tr('The service returns to its previous configuration.'), confirm: tr('Roll back') }))
          swarmAct(api.post(`${base}/action/rollback`), tr('{0}: rolling back', name))
      },
    },
    { label: tr('Edit configuration'), icon: <Pencil size={I} />, onClick: () => go(ep, 'services', sv.ID, 'spec') },
    'sep',
    {
      label: tr('Open port'),
      icon: <ExternalLink size={I} />,
      disabled: ports.length === 0,
      hint: ports.length === 0 ? tr('no ports') : undefined,
      submenu: ports.map((p) => ({
        label: `${p.PublishedPort} → ${p.TargetPort}/${p.Protocol}`,
        hint: p.PublishMode,
        onClick: () => window.open(`${p.TargetPort === 443 ? 'https' : 'http'}://${host}:${p.PublishedPort}`, '_blank'),
      })),
    },
    {
      label: tr('Copy'),
      icon: <ClipboardCopy size={I} />,
      submenu: [
        { label: tr('Name'), onClick: () => copy(name) },
        { label: 'ID', hint: shortId(sv.ID), onClick: () => copy(sv.ID) },
        { label: tr('Image'), onClick: () => copy(serviceImage(sv)) },
        'sep',
        { label: tr('docker service logs command'), onClick: () => copy(`docker service logs -f --tail 200 ${name}`) },
        { label: tr('docker service ps command'), onClick: () => copy(`docker service ps ${name}`) },
      ],
    },
    stack && { label: tr('Go to stack {0}', stack), icon: <Layers size={I} />, onClick: () => go(ep, 'swarm-stacks', stack) },
    toggleFav(ep, 'service', sv.ID, name),
    'sep',
    { label: tr('Delete service…'), icon: <Trash2 size={I} />, danger: true, onClick: () => removeServices(ep, [sv]) },
  ]
}

export function nodeName(n: SwarmNode) {
  return n.Spec.Name || n.Description.Hostname
}

function labelsText(l?: Record<string, string>) {
  return Object.entries(l || {}).map(([k, v]) => `${k}=${v}`).join(', ')
}

export function parseLabels(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const part of text.split(/[,\n]/)) {
    const [k, ...v] = part.split('=')
    if (k.trim()) out[k.trim()] = v.join('=').trim()
  }
  return out
}

export async function editNodeLabels(ep: string, n: SwarmNode) {
  const v = await prompt({ title: tr('Labels of {0}', nodeName(n)), label: 'key=value, key2=value2', value: labelsText(n.Spec.Labels), confirm: tr('Save'), help: tr('Use them in placement constraints, e.g. node.labels.zone==a') })
  if (v !== null) act(api.post(`/e/${enc(ep)}/nodes/${enc(n.ID)}/update`, { labels: parseLabels(v) }), tr('Labels of {0} saved', nodeName(n)))
}

export async function drainNode(ep: string, n: SwarmNode) {
  const name = nodeName(n)
  if (await confirm({ title: tr('Drain {0}', name), body: tr('Its {0} running tasks will be rescheduled on other nodes.', n.RunningTasks), confirm: tr('Drain') }))
    act(api.post(`/e/${enc(ep)}/nodes/${enc(n.ID)}/update`, { availability: 'drain' }), tr('{0}: drained', name))
}

export function nodeActions(ep: string, n: SwarmNode): MenuEntry[] {
  const name = nodeName(n)
  const update = (body: any, ok: string) => () => act(api.post(`/e/${enc(ep)}/nodes/${enc(n.ID)}/update`, body), ok)
  const manager = n.Spec.Role === 'manager'
  return [
    { header: tr('node {0}', name) },
    { label: tr('Open record'), icon: <Server size={I} />, onClick: () => go(ep, 'nodes', n.ID) },
    'sep',
    {
      label: tr('Availability'),
      icon: <Activity size={I} />,
      submenu: [
        { label: tr('Active'), checked: n.Spec.Availability === 'active', onClick: update({ availability: 'active' }, tr('{0}: active', name)) },
        { label: tr('Pause (no new tasks)'), checked: n.Spec.Availability === 'pause', onClick: update({ availability: 'pause' }, tr('{0}: paused', name)) },
        { label: tr('Drain (move its tasks away)'), checked: n.Spec.Availability === 'drain', onClick: () => drainNode(ep, n) },
      ],
    },
    manager
      ? { label: tr('Demote to worker'), icon: <ArrowDownCircle size={I} />, disabled: n.ManagerStatus?.Leader, hint: n.ManagerStatus?.Leader ? 'leader' : undefined, onClick: update({ role: 'worker' }, tr('{0} is now a worker', name)) }
      : { label: tr('Promote to manager'), icon: <ShieldCheck size={I} />, onClick: update({ role: 'manager' }, tr('{0} is now a manager', name)) },
    { label: tr('Edit labels…'), icon: <Tags size={I} />, onClick: () => editNodeLabels(ep, n) },
    'sep',
    {
      label: tr('Copy'),
      icon: <ClipboardCopy size={I} />,
      submenu: [
        { label: tr('Hostname'), onClick: () => copy(n.Description.Hostname) },
        { label: 'ID', hint: shortId(n.ID), onClick: () => copy(n.ID) },
        { label: tr('Address'), hint: n.Status.Addr, onClick: () => copy(n.Status.Addr) },
      ],
    },
    toggleFav(ep, 'node', n.ID, name),
    'sep',
    {
      label: tr('Remove from the swarm…'),
      icon: <Trash2 size={I} />,
      danger: true,
      onClick: async () => {
        const ok = await confirm({
          title: tr('Remove node {0}', name),
          body: n.Status.State === 'down' ? tr('The node is down: it is removed from the cluster list.') : tr('A node that is still up must leave the swarm first (docker swarm leave on it), or be removed by force.'),
          danger: true,
          confirm: tr('Remove'),
          check: n.Status.State === 'down' ? undefined : tr('Force'),
        })
        if (ok) act(api.del(`/e/${enc(ep)}/nodes/${enc(n.ID)}?force=${ok.checked ? 1 : 0}`), tr('Node {0} removed', name))
      },
    },
  ]
}

export function swarmObjectActions(ep: string, kind: 'secrets' | 'configs', o: SwarmObject): MenuEntry[] {
  const secret = kind === 'secrets'
  return [
    { header: `${secret ? 'secret' : 'config'} ${o.Spec.Name}` },
    { label: tr('Open record'), icon: secret ? <KeyRound size={I} /> : <FileText size={I} />, onClick: () => go(ep, kind, o.ID) },
    o.UsedBy.length > 0 && {
      label: tr('Used by'),
      icon: <Boxes size={I} />,
      submenu: o.UsedBy.map((u) => ({ label: u.name, onClick: () => go(ep, 'services', u.id) })),
    },
    'sep',
    {
      label: tr('Copy'),
      icon: <ClipboardCopy size={I} />,
      submenu: [
        { label: tr('Name'), onClick: () => copy(o.Spec.Name) },
        { label: 'ID', hint: shortId(o.ID), onClick: () => copy(o.ID) },
        { label: tr('Compose reference'), onClick: () => copy(`${kind}:\n  ${o.Spec.Name}:\n    external: true\n`) },
      ],
    },
    'sep',
    {
      label: secret ? tr('Delete secret…') : tr('Delete config…'),
      icon: <Trash2 size={I} />,
      danger: true,
      disabled: o.UsedBy.length > 0,
      hint: o.UsedBy.length ? tr('in use') : undefined,
      onClick: async () => {
        if (await confirm({ title: tr('Delete {0}', o.Spec.Name), danger: true, confirm: tr('Delete') }))
          act(api.del(`/e/${enc(ep)}/${kind}/${enc(o.ID)}`), tr('{0} deleted', o.Spec.Name))
      },
    },
  ]
}

/** docker stack deploy; images with build: are built first (pushed if the stack is set to). */
export function swarmStackDeploy(ep: string, name: string, opts: { build?: boolean; push?: boolean } = {}) {
  const push = opts.push ?? getPref(`swarm.push.${ep}.${name}`, false)
  return runJob(`${name}: deploy`, 'POST', `/e/${enc(ep)}/swarm/stacks/${enc(name)}/deploy`, { build: opts.build, push })
    .catch((e) => toast.err(e))
    .finally(() => invalidate('/swarm', '/services', '/images'))
}

export function swarmStackActions(ep: string, st: SwarmStack): MenuEntry[] {
  const noFiles = st.managed ? undefined : tr('no files')
  return [
    { header: `stack ${st.name}` },
    { label: tr('Open record'), icon: <Layers size={I} />, onClick: () => go(ep, 'swarm-stacks', st.name) },
    { label: st.managed ? tr('Edit compose') : tr('Attach compose…'), icon: <FileCode2 size={I} />, onClick: () => go(ep, 'swarm-stacks', st.name, 'compose') },
    { label: tr('Combined logs'), icon: <ScrollText size={I} />, disabled: st.services.length === 0, onClick: () => go(ep, 'swarm-stacks', st.name, 'logs') },
    'sep',
    { label: tr('Deploy (stack deploy)'), icon: <Rocket size={I} />, disabled: !st.managed, hint: noFiles, onClick: () => swarmStackDeploy(ep, st.name) },
    { label: tr('Build, push and deploy'), icon: <Upload size={I} />, disabled: !st.managed, hint: noFiles, onClick: () => swarmStackDeploy(ep, st.name, { build: true, push: true }) },
    { label: tr('Deploy without building'), icon: <Rocket size={I} />, disabled: !st.managed, hint: noFiles, onClick: () => swarmStackDeploy(ep, st.name, { build: false }) },
    {
      label: tr('Services'),
      icon: <Boxes size={I} />,
      disabled: st.services.length === 0,
      submenu: st.services.map((sv) => ({ label: sv.name, hint: `${sv.running}/${sv.desired}`, onClick: () => go(ep, 'services', sv.id) })),
    },
    {
      label: st.location ? tr('Change location…') : st.managed ? tr('Move to a directory…') : tr('Use the compose file in a directory…'),
      icon: <FolderOpen size={I} />,
      hint: st.location?.dir,
      onClick: () => openDialog((close) => <LocationDialog ep={ep} swarm stack={{ name: st.name, location: st.location, total: st.running, managed: st.managed }} onClose={close} />),
    },
    'sep',
    {
      label: tr('Delete stack…'),
      icon: <Trash2 size={I} />,
      danger: true,
      onClick: async () => {
        const ok = await confirm({
          title: tr('Delete {0}', st.name),
          body: tr('docker stack rm removes its services, networks, secrets and configs. Volumes are kept.'),
          danger: true,
          confirm: tr('Delete'),
          check: st.managed ? tr('Keep the compose file in ducktainer') : undefined,
        })
        if (!ok) return
        runJob(tr('Delete {0}', st.name), 'DELETE', `/e/${enc(ep)}/swarm/stacks/${enc(st.name)}?keep=${ok.checked ? 1 : 0}`)
          .then(() => go(ep, 'swarm-stacks'))
          .catch((e) => toast.err(e))
          .finally(() => invalidate('/swarm', '/services'))
      },
    },
  ]
}
