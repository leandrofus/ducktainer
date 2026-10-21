import { t as tr, LANGUAGES, getLang, setLang } from '../i18n'
import { useEffect, useState } from 'react'
import { Settings as Cog, Server, KeyRound, Shield, Store, Plus, Trash2, Pencil, RefreshCw, ClipboardCopy, Check, Terminal } from 'lucide-react'
import { api, enc } from '../lib/api'
import type { Endpoint, Registry } from '../lib/types'
import { useResource, invalidate } from '../lib/store'
import { copy, dateTime } from '../lib/format'
import { getMe, isViewer } from '../lib/session'
import { AccessSettings } from './AccessSettings'
import { CloudflareSettings } from '../components/Expose'
import { go } from '../lib/router'
import { Empty, Spinner, Tabs, Tag } from '../ui/common'
import { useMenu } from '../ui/ContextMenu'
import { Modal, confirm, openDialog, toast } from '../ui/overlays'

interface AgentInstall {
  install: string
  manual: string
  tls: boolean
  candidates: { url: string; label: string }[]
  loopback_only: boolean
  /** os/arch pairs the server has an agent binary for */
  targets: string[]
}

type NodeOS = 'linux' | 'windows'

const agentInstall = (name: string, server: string, os: NodeOS = 'linux') =>
  api.get<AgentInstall>(`/endpoints/${enc(name)}/agent-install?server=${enc(server)}&os=${os}`)

/** The commands with an address the node can reach: the browser's, unless it is localhost. */
async function agentInstallBest(name: string) {
  const first = await agentInstall(name, window.location.origin)
  if (isLoopbackURL(window.location.origin) && first.candidates.length) return agentInstall(name, first.candidates[0].url)
  return first
}

const isLoopbackURL = (u: string) => {
  try {
    const h = new URL(u).hostname
    return h === 'localhost' || h === '::1' || h === '[::1]' || h.startsWith('127.')
  } catch {
    return false
  }
}

/** Enrollment commands for a saved agent host (token in a header, server key pinned). */
function AgentCommands({ name }: { name: string }) {
  const [cmds, setCmds] = useState<AgentInstall | null>(null)
  const [err, setErr] = useState('')
  // the address the node will use to reach this server: the browser's own address
  // is useless when it is localhost, so the server's reachable ones are offered
  const [server, setServer] = useState(window.location.origin)
  const [picked, setPicked] = useState(false)
  const [os, setOs] = useState<NodeOS>('linux')
  useEffect(() => {
    const h = setTimeout(() => {
      agentInstall(name, server, os)
        .then((c) => {
          setCmds(c)
          setErr('')
          if (!picked && isLoopbackURL(server) && c.candidates.length) setServer(c.candidates[0].url)
        })
        .catch((e) => setErr(e.message))
    }, 300)
    return () => clearTimeout(h)
  }, [name, server, os])
  if (err && !cmds) return <div className="alert alert-bad">{err}</div>
  if (!cmds) return <Spinner />
  const box = (cmd: string) => (
    <div style={{ background: 'var(--bg-code, #18181b)', padding: '8px 12px', borderRadius: 6, border: '1px solid var(--border, #27272a)' }}>
      <code className="mono small" style={{ wordBreak: 'break-all', userSelect: 'all' }}>{cmd}</code>
    </div>
  )
  return (
    <div style={{ marginTop: 12 }}>
      <div className="row gap" style={{ marginBottom: 8 }}>
        <span className="small muted">{tr('Node:')}</span>
        {(['linux', 'windows'] as NodeOS[]).map((o) => {
          const available = cmds.targets.some((t) => t.startsWith(o + '/'))
          return (
            <button key={o} type="button" className={`btn btn-sm${os === o ? ' btn-primary' : ''}`} disabled={!available && os !== o} title={available ? undefined : tr('This server has no agent binary for {0}', o)} onClick={() => setOs(o)}>
              {o === 'linux' ? 'Linux' : 'Windows'}
            </button>
          )
        })}
      </div>
      <label className="field">
        <span>{tr('Server address, as the node will reach it')}</span>
        <input className="mono" list="agent-server-addrs" value={server} onChange={(e) => (setPicked(true), setServer(e.target.value.trim()))} />
        <datalist id="agent-server-addrs">
          {cmds.candidates.map((c) => <option key={c.url} value={c.url}>{c.label}</option>)}
        </datalist>
      </label>
      {cmds.loopback_only && (
        <div className="alert alert-warn" style={{ margin: '8px 0' }}>
          {tr('ducktainer listens only on 127.0.0.1, which no node can reach. Start it with --listen 0.0.0.0:9900 (and allow the node\'s network), or with --agent-listen :9901.')}
        </div>
      )}
      {!cmds.loopback_only && isLoopbackURL(server) && (
        <div className="alert alert-warn" style={{ margin: '8px 0' }}>
          {tr('On the node, localhost is the node itself: use one of this server\'s addresses.')}
        </div>
      )}
      <div className="row gap" style={{ marginBottom: 4 }}>
        <span className="small muted grow"><strong>{os === 'windows' ? tr('Run this in an elevated PowerShell (Run as administrator) on the node:') : tr('Run this command on the remote node:')}</strong></span>
        <button type="button" className="btn btn-sm" onClick={() => { copy(cmds.install); toast.ok(tr('Command copied')) }}><ClipboardCopy size={13} /> {tr('Copy')}</button>
      </div>
      {box(cmds.install)}
      <details style={{ marginTop: 8 }}>
        <summary className="small muted" style={{ cursor: 'pointer' }}>{tr('Or standalone manual command (without script)')}</summary>
        <div style={{ marginTop: 4 }}>{box(cmds.manual)}</div>
      </details>
      {os === 'windows' && <p className="help">{tr('Needs Windows PowerShell 5.1 or PowerShell 7 and the Docker engine on the node (named pipe docker_engine). It installs the ducktainer-agent Windows service.')}</p>}
      <p className="help">{tr('The installer checks the node first (architecture, that it reaches this server, that the binary runs with its glibc) and installs nothing if a check fails.')}</p>
      <p className="help">{cmds.tls
        ? tr('The commands pin this server\'s certificate: a node cannot be fed a different binary or server. The token only works for this host and is kept on the node in /etc/ducktainer-agent/agent.env (root only).')
        : tr('This server is not using TLS: the token and the binary travel in clear. Serve ducktainer with TLS (or use --agent-listen) before enrolling nodes over an untrusted network.')}</p>
    </div>
  )
}

function EndpointDialog({ initial, onClose }: { initial?: Endpoint; onClose: () => void }) {
  const [f, setF] = useState(() => {
    const isNew = !initial
    const url = initial?.url ?? 'ssh://'
    const token = initial?.token || (isNew ? Array.from(crypto.getRandomValues(new Uint8Array(16))).map((b) => b.toString(16).padStart(2, '0')).join('') : '')
    return {
      name: initial?.name ?? '',
      url,
      token,
      tls: initial?.tls ?? false,
      fingerprint: initial?.fingerprint ?? '',
      ssh_key: initial?.ssh_key ?? '',
      docker_socket: initial?.docker_socket ?? '',
      compose_image: initial?.compose_image ?? '',
      no_auto_update: initial?.no_auto_update ?? false,
    }
  })
  const [busy, setBusy] = useState(false)
  const scheme = f.url.split('://')[0]

  const onSchemeChange = (s: string) => {
    if (s === 'agent') {
      const name = f.name || 'node-1'
      setF({ ...f, url: `agent://${name}` })
    } else if (s === 'unix') {
      setF({ ...f, url: 'unix:///var/run/docker.sock' })
    } else if (s === 'tcp') {
      setF({ ...f, url: 'tcp://host:2376' })
    } else if (s === 'wsl') {
      setF({ ...f, url: 'wsl://ducktainer' })
    } else if (s === 'npipe') {
      setF({ ...f, url: 'npipe:////./pipe/docker_engine' })
    } else {
      setF({ ...f, url: 'ssh://user@host' })
    }
  }

  const save = async (test: boolean) => {
    setBusy(true)
    try {
      const res = await api.post<{ fingerprint?: string }>('/endpoints', { ...f, original: initial?.name ?? '', test })
      toast.ok(tr('Host {0} saved', f.name))
      if (res.fingerprint && res.fingerprint !== f.fingerprint) toast.ok(tr('TLS certificate pinned: {0}', res.fingerprint.slice(0, 16) + '…'))
      invalidate('/endpoints')
      onClose()
      // a new agent host: show its install command right away
      if (scheme === 'agent' && !initial) openDialog((c) => <EndpointDialog initial={{ ...f, url: `agent://${f.name}`, token: '••••' } as Endpoint} onClose={c} />)
    } catch (e) {
      toast.err(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={initial ? tr('Edit {0}', initial.name) : tr('Add host')} icon={<Server size={18} />} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
      <button className={`btn ${scheme === 'agent' ? 'btn-primary' : ''}`} disabled={busy || !f.name} onClick={() => save(false)}>{scheme === 'agent' ? tr('Save host') : tr('Save without testing')}</button>
      {scheme !== 'agent' && <button className="btn btn-primary" disabled={busy || !f.name} onClick={() => save(true)}>{busy ? <Spinner /> : <Check size={14} />} {tr('Test and save')}</button>}
    </>}>
      <div className="grid-2">
        <label className="field"><span>{tr('Name')}</span><input value={f.name} onChange={(e) => {
          const newName = e.target.value.replace(/[\s/]/g, '-')
          setF({ ...f, name: newName, url: scheme === 'agent' ? `agent://${newName}` : f.url })
        }} placeholder="produccion" autoFocus /></label>
        <label className="field"><span>{tr('Connection')}</span>
          <select value={scheme} onChange={(e) => onSchemeChange(e.target.value)}>
            <option value="ssh">{tr('SSH (agentless)')}</option>
            <option value="agent">{tr('Agent (outbound / Edge)')}</option>
            <option value="tcp">ducktainer expose (TCP + token)</option>
            <option value="unix">{tr('Local socket')}</option>
            <option value="wsl">{tr('WSL distribution (Windows)')}</option>
            <option value="npipe">{tr('Named pipe (Windows)')}</option>
          </select>
        </label>
        {scheme !== 'agent' && <label className="field span-2"><span>URL</span><input className="mono" value={f.url} onChange={(e) => setF({ ...f, url: e.target.value })} /></label>}
        {scheme === 'ssh' && <label className="field span-2"><span>{tr('SSH key (optional)')}</span><input className="mono" placeholder={tr('~/.ssh/id_ed25519 (defaults to your SSH agent/config)')} value={f.ssh_key} onChange={(e) => setF({ ...f, ssh_key: e.target.value })} /></label>}
        {scheme === 'tcp' && (
          <>
            <label className="field span-2"><span>{tr('Remote host token')}</span><input className="mono" value={f.token} onChange={(e) => setF({ ...f, token: e.target.value })} placeholder={tr('printed by `ducktainer expose` or `ducktainer token --rotate` on the host')} /></label>
            <label className="check"><input type="checkbox" checked={f.tls} onChange={(e) => setF({ ...f, tls: e.target.checked })} /> TLS</label>
            <label className="field"><span>{tr('TLS sha256 fingerprint (pinning)')}</span><input className="mono" value={f.fingerprint} onChange={(e) => setF({ ...f, fingerprint: e.target.value, tls: f.tls || !!e.target.value })} /></label>
          </>
        )}
        {scheme === 'agent' && (
          <div className="span-2">
            <div className="field">
              <label className="check" style={{ marginBottom: 8 }}>
                <input type="checkbox" checked={!f.no_auto_update} onChange={(e) => setF({ ...f, no_auto_update: !e.target.checked })} /> {tr('Update the agent automatically when the server\'s version changes')}
              </label>
              <span>{tr('Agent token')}</span>
              <div className="row gap">
                <input className="mono grow" value={f.token} onChange={(e) => setF({ ...f, token: e.target.value })} placeholder="token" />
                <button type="button" className="btn" title={tr('Generate new token')} onClick={() => setF({ ...f, token: Array.from(crypto.getRandomValues(new Uint8Array(16))).map((b) => b.toString(16).padStart(2, '0')).join('') })}><RefreshCw size={14} /></button>
              </div>
            </div>
            {initial && f.token === '••••'
              ? <AgentCommands name={initial.name} />
              : <p className="help">{initial ? tr('Save to apply the new token, then install the agent again on the node.') : tr('Save the host to get the command that installs the agent on the node.')}</p>}
          </div>
        )}
        {scheme !== 'unix' && scheme !== 'npipe' && (
          <details className="span-2">
            <summary className="small muted" style={{ cursor: 'pointer' }}>{tr('Stacks in directories on the node')}</summary>
            <div className="grid-2" style={{ marginTop: 8 }}>
              <label className="field"><span>{tr('Docker socket on the node')}</span><input className="mono" placeholder="/var/run/docker.sock" value={f.docker_socket} onChange={(e) => setF({ ...f, docker_socket: e.target.value })} /></label>
              <label className="field"><span>{tr('Image with docker compose')}</span><input className="mono" placeholder="docker:cli" value={f.compose_image} onChange={(e) => setF({ ...f, compose_image: e.target.value })} /></label>
            </div>
            <p className="help">{tr('To read, edit and deploy stacks that live in a directory of the node (e.g. /opt/myapp), ducktainer starts short-lived containers from this image there, mounting the directory and the Docker socket. Change the socket for rootless Docker (/run/user/1000/docker.sock) and the image for air-gapped nodes (a mirror of docker:cli).')}</p>
          </details>
        )}
      </div>
      <div className="help">
        {scheme === 'ssh' && <>{tr('Uses your')} <code>ssh</code> {tr('client and runs')} <code>docker system dial-stdio</code> {tr('on the remote:')} <strong>{tr('nothing needs to be installed')}</strong> {tr('there. Requires key-based access (no interactive password) and a user allowed to use Docker.')}</>}
        {scheme === 'agent' && <>{tr('The standalone agent is downloaded directly from this server and connects outbound. Ideal for nodes behind NAT, firewalls or dynamic IPs.')}</>}
        {scheme === 'tcp' && <>{tr('Without a fingerprint, the certificate presented when saving is pinned.')} {tr('On the remote host:')} <code>ducktainer expose --allow &lt;{tr('your-IP')}&gt;</code>{tr('. It prints the token and TLS fingerprint to paste here.')}</>}
        {scheme === 'unix' && <>{tr('Docker socket of the machine running ducktainer.')}</>}
        {(scheme === 'wsl' || scheme === 'npipe') && <>{tr('Docker on this Windows machine. To install it without Docker Desktop:')} <code>ducktainer engine install --mode {scheme === 'wsl' ? 'wsl' : 'native'}</code></>}
      </div>
    </Modal>
  )
}

function Hosts() {
  const { data, reload, loading } = useResource<Endpoint[]>('/endpoints')
  const menu = useMenu()
  const actions = (e: Endpoint) => [
    { header: e.name },
    { label: tr('Open'), onClick: () => go(e.name, 'jump') },
    { label: tr('Edit…'), icon: <Pencil size={14} />, onClick: () => openDialog((c) => <EndpointDialog initial={e} onClose={c} />) },
    { label: tr('Copy URL'), icon: <ClipboardCopy size={14} />, onClick: () => copy(e.url) },
    { label: tr('Copy docker-env command'), icon: <Terminal size={14} />, onClick: () => copy(`eval "$(ducktainer docker-env ${e.name})"`) },
    ...(e.url.startsWith('agent://') ? [{
      label: tr('Copy agent install command'),
      icon: <ClipboardCopy size={14} />,
      onClick: () => agentInstallBest(e.name).then((c) => { copy(c.install); toast.ok(tr('Command copied')) }).catch(toast.err),
    }] : []),
    'sep' as const,
    { label: tr('Delete…'), icon: <Trash2 size={14} />, danger: true, onClick: async () => {
      if (await confirm({ title: tr('Delete host {0}', e.name), body: tr('It is only removed from ducktainer; the host is not touched.'), danger: true, confirm: tr('Delete') }))
        api.del(`/endpoints/${enc(e.name)}`).then(() => invalidate('/endpoints')).catch(toast.err)
    } },
  ]
  return (
    <div>
      <div className="row gap section-head">
        <p className="muted grow">{tr('Each host is a Docker daemon. ducktainer connects over SSH, via outbound agent, or to')} <code>ducktainer expose</code>.</p>
        <button className="btn" onClick={reload}>{loading ? <Spinner /> : <RefreshCw size={14} />}</button>
        <button className="btn btn-primary" onClick={() => openDialog((c) => <EndpointDialog onClose={c} />)}><Plus size={14} /> {tr('Add host')}</button>
      </div>
      <div className="cards">
        {(data || []).map((e) => (
          <div key={e.name} className="host-card" onContextMenu={menu(() => actions(e))} onClick={() => go(e.name, 'jump')}>
            <div className="row gap">
              <span className={`dot dot-${e.online ? 'open' : 'closed'}`} />
              <strong className="grow">{e.name}</strong>
              <Tag>{e.url.split('://')[0]}</Tag>
            </div>
            <div className="mono small muted ellipsis">{e.url}</div>
            {e.online ? (
              <div className="small">{e.os} · Docker {e.version} · {e.running}/{e.total} {tr('containers')}</div>
            ) : (
              <div className="small tone-bad">{e.error}</div>
            )}
            {e.url.startsWith('agent://') && e.agent_version && (
              <div className="small muted">
                {tr('Agent {0}', e.agent_version)}
                {getMe()?.version && getMe()!.version !== 'dev' && e.agent_version !== getMe()!.version && (
                  <> · <span className="tone-warn">{e.no_auto_update ? tr('behind the server ({0}); automatic update is off', getMe()!.version!) : tr('updating to {0}…', getMe()!.version!)}</span></>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function RegistryDialog({ ep, initial, onClose }: { ep: string; initial?: Registry; onClose: () => void }) {
  const [f, setF] = useState({ name: initial?.name ?? '', server: initial?.server ?? 'docker.io', username: initial?.username ?? '', password: initial?.password ?? '', endpoints: initial?.endpoints ?? [] })
  const [busy, setBusy] = useState(false)
  const hosts = useResource<Endpoint[]>('/endpoints').data
  const toggleHost = (h: string, on: boolean) => setF({ ...f, endpoints: on ? [...f.endpoints, h] : f.endpoints.filter((x) => x !== h) })
  return (
    <Modal title={initial ? tr('Edit {0}', initial.name) : tr('Add registry')} icon={<KeyRound size={18} />} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
      <button className="btn btn-primary" disabled={busy || !f.server} onClick={async () => {
        setBusy(true)
        try {
          await api.post('/registries', { ...f, original: initial?.name ?? '', endpoint: ep })
          toast.ok(tr('Credentials verified and saved'))
          invalidate('/registries')
          onClose()
        } catch (e) {
          toast.err(e)
        } finally {
          setBusy(false)
        }
      }}>{busy ? <Spinner /> : <Check size={14} />} {tr('Verify and save')}</button>
    </>}>
      <div className="grid-2">
        <label className="field"><span>{tr('Server')}</span>
          <input className="mono" list="reg-presets" value={f.server} onChange={(e) => setF({ ...f, server: e.target.value })} />
          <datalist id="reg-presets"><option value="docker.io" /><option value="ghcr.io" /><option value="registry.gitlab.com" /><option value="quay.io" /><option value="public.ecr.aws" /></datalist>
        </label>
        <label className="field"><span>{tr('Name')}</span><input value={f.name} placeholder={f.server} onChange={(e) => setF({ ...f, name: e.target.value })} /></label>
        <label className="field"><span>{tr('User')}</span><input value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} /></label>
        <label className="field"><span>{tr('Password / token')}</span><input type="password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} /></label>
      </div>
      <div className="field" style={{ marginTop: 12 }}>
        <span>{tr('Hosts that receive them')}</span>
        <div className="row gap wrap">
          <label className="check"><input type="checkbox" checked={f.endpoints.length === 0} onChange={(e) => e.target.checked && setF({ ...f, endpoints: [] })} /> {tr('All')}</label>
          {(hosts || []).map((h) => (
            <label key={h.name} className="check"><input type="checkbox" checked={f.endpoints.includes(h.name)} onChange={(e) => toggleHost(h.name, e.target.checked)} /> {h.name}</label>
          ))}
        </div>
      </div>
      <p className="help">{tr('They are used automatically when pulling/pushing images from that server and in compose deployments.')} {tr('Compose runs on a node get a copy of the credentials: limit them to the hosts that need them.')}</p>
    </Modal>
  )
}

function Registries({ ep }: { ep: string }) {
  const { data } = useResource<Registry[]>('/registries')
  const menu = useMenu()
  return (
    <div>
      <div className="row gap section-head">
        <p className="muted grow">{tr('Credentials for private registries (Docker Hub, GHCR, GitLab, Harbor…).')}</p>
        <button className="btn btn-primary" onClick={() => openDialog((c) => <RegistryDialog ep={ep} onClose={c} />)}><Plus size={14} /> {tr('Add registry')}</button>
      </div>
      {data?.length === 0 && <Empty icon={<KeyRound size={28} />} title={tr('No registries')}>{tr('Public images need no credentials.')}</Empty>}
      <div className="table-wrap">
        {!!data?.length && (
          <table className="table">
            <thead><tr><th>{tr('Name')}</th><th>{tr('Server')}</th><th>{tr('User')}</th><th>{tr('Hosts')}</th><th style={{ width: 40 }} /></tr></thead>
            <tbody>
              {data.map((r) => (
                <tr key={r.name} onDoubleClick={() => openDialog((c) => <RegistryDialog ep={ep} initial={r} onClose={c} />)} onContextMenu={menu(() => [
                  { label: tr('Edit…'), icon: <Pencil size={14} />, onClick: () => openDialog((c) => <RegistryDialog ep={ep} initial={r} onClose={c} />) },
                  { label: tr('Copy server'), onClick: () => copy(r.server) },
                  'sep',
                  { label: tr('Delete'), icon: <Trash2 size={14} />, danger: true, onClick: () => api.del(`/registries/${enc(r.name)}`).then(() => invalidate('/registries')).catch(toast.err) },
                ])}>
                  <td><strong>{r.name}</strong></td>
                  <td className="mono small">{r.server}</td>
                  <td>{r.username}</td>
                  <td className="small">{r.endpoints?.length ? r.endpoints.join(', ') : <span className="muted">{tr('All')}</span>}</td>
                  <td><button className="icon-btn" onClick={() => api.del(`/registries/${enc(r.name)}`).then(() => invalidate('/registries')).catch(toast.err)}><Trash2 size={14} /></button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

interface AuthT {
  pam: boolean
  pam_service: string
  token_login: boolean
  admin_groups: string[] | null
  viewer_groups: string[] | null
  admin_users: string[] | null
  viewer_users: string[] | null
  allow_root: boolean
  session_hours: number
}

interface SettingsT {
  allow: string[]
  template_sources: { name: string; url: string; enabled: boolean }[]
  data_dir: string
  auth: AuthT
  auth_info: { pam_built: boolean; mode: string; uid: number }
}


interface AuditE {
  time: string
  user: string
  role?: string
  method: string
  path: string
  status: number
  ip: string
  note?: string
}

function Audit() {
  const { data, reload } = useResource<AuditE[]>('/audit', { poll: 15000 })
  const [q, setQ] = useState('')
  const menu = useMenu()
  if (!data) return <Spinner />
  const rows = data.filter((e) => !q || `${e.user} ${e.method} ${e.path} ${e.ip} ${e.note ?? ''}`.toLowerCase().includes(q.toLowerCase()))
  return (
    <div>
      <div className="row gap section-head">
        <div className="search"><input placeholder={tr('Filter by user, path, IP…')} value={q} onChange={(e) => setQ(e.target.value)} /></div>
        <span className="grow muted small">{tr('Write actions, terminals and sign-ins (last 500). They also go to the service journal.')}</span>
        <button className="btn" onClick={reload}><RefreshCw size={14} /></button>
      </div>
      <div className="table-wrap">
        <table className="table compact">
          <thead><tr><th>{tr('When')}</th><th>{tr('User')}</th><th>{tr('Action')}</th><th>{tr('Path')}</th><th>{tr('Result')}</th><th>IP</th></tr></thead>
          <tbody>
            {rows.map((e, i) => (
              <tr key={i} onContextMenu={menu(() => [
                { label: tr('Filter by {0}', e.user), onClick: () => setQ(e.user) },
                { label: tr('Filter by IP {0}', e.ip), onClick: () => setQ(e.ip) },
                { label: tr('Copy line (JSON)'), onClick: () => copy(JSON.stringify(e)) },
              ])}>
                <td className="small" title={e.time}>{dateTime(e.time)}</td>
                <td><strong>{e.user}</strong> {e.role && <span className="muted small">{e.role}</span>}</td>
                <td><Tag tone={e.method === 'LOGIN' ? 'accent' : e.method === 'DELETE' ? 'bad' : undefined}>{e.method}</Tag></td>
                <td className="mono small cell-clip" title={e.path}>{e.path.replace(/^\/api\/e\//, '')}</td>
                <td><span className={e.status < 400 ? 'tone-ok' : 'tone-bad'}>{e.status}</span> {e.note && <span className="muted small">{e.note}</span>}</td>
                <td className="mono small muted">{e.ip}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {rows.length === 0 && <Empty title={tr('No records')} />}
      </div>
    </div>
  )
}

function Sources() {
  const { data, reload } = useResource<SettingsT>('/settings')
  const [list, setList] = useState<SettingsT['template_sources']>([])
  const [url, setUrl] = useState('')
  useEffect(() => {
    if (data) setList(data.template_sources)
  }, [data])
  const save = async (next: SettingsT['template_sources']) => {
    setList(next)
    try {
      await api.put('/settings', { allow: data!.allow, template_sources: next })
      invalidate('/templates')
      reload()
    } catch (e) {
      toast.err(e)
    }
  }
  if (!data) return <Spinner />
  return (
    <div className="panel">
      <h4><Store size={14} /> {tr('Marketplace sources')}</h4>
      <p className="muted small">{tr('Compatible with the Portainer app template format (v2/v3). Besides these, ducktainer ships its own built-in catalog.')}</p>
      <ul className="source-list">
        {list.map((s, i) => (
          <li key={s.url}>
            <label className="check"><input type="checkbox" checked={s.enabled} onChange={(e) => save(list.map((x, j) => (j === i ? { ...x, enabled: e.target.checked } : x)))} /> <strong>{s.name}</strong></label>
            <span className="mono small muted ellipsis grow">{s.url}</span>
            <button className="icon-btn" onClick={() => save(list.filter((_, j) => j !== i))}><Trash2 size={14} /></button>
          </li>
        ))}
      </ul>
      <form className="row gap" onSubmit={(e) => {
        e.preventDefault()
        if (!url) return
        save([...list, { name: new URL(url).pathname.split('/').slice(1, 3).join('/') || url, url, enabled: true }])
        setUrl('')
      }}>
        <input className="mono grow" placeholder="https://…/templates.json" value={url} onChange={(e) => setUrl(e.target.value)} />
        <button className="btn"><Plus size={14} /> {tr('Add source')}</button>
      </form>
    </div>
  )
}

export function Settings({ ep, tab = 'hosts' }: { ep: string; tab?: string }) {
  return (
    <div className="page">
      <div className="page-head"><div className="page-title"><Cog size={20} /><h1>{tr('Settings')}</h1></div></div>
      <Tabs
        active={tab === 'users' ? 'access' : tab}
        onChange={(t) => go(ep, 'settings', t)}
        tabs={[
          { id: 'hosts', label: tr('Hosts') },
          { id: 'access', label: tr('Access') },
          { id: 'registries', label: 'Registries' },
          { id: 'cloudflare', label: 'Cloudflare' },
          { id: 'audit', label: tr('Audit') },
          { id: 'marketplace', label: 'Marketplace' },
        ]}
      />
      <div className="page-body">
        {isViewer() && tab !== 'hosts' && <Empty icon={<Shield size={28} />} title={tr('Administrators only')} />}
        {tab === 'hosts' && <Hosts />}
        {(tab === 'access' || tab === 'users') && !isViewer() && <AccessSettings />}
        {tab === 'audit' && !isViewer() && <Audit />}
        {!isViewer() && tab === 'registries' && <Registries ep={ep} />}
        {!isViewer() && tab === 'marketplace' && <Sources />}
        {!isViewer() && tab === 'cloudflare' && <CloudflareSettings />}
      </div>
    </div>
  )
}

export function Login({ onDone }: { onDone: () => void }) {
  const [info, setInfo] = useState<{ pam: boolean; token: boolean; os?: string } | null>(null)
  const [mode, setMode] = useState<'user' | 'token'>('user')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [token, setToken] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    api.get('/auth').then((i) => {
      setInfo(i)
      if (!i.pam) setMode('token')
    }).catch(() => setInfo({ pam: false, token: true }))
  }, [])
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      await api.post('/login', mode === 'user' ? { username, password } : { token })
      onDone()
    } catch (e: any) {
      setErr(e.message)
      setPassword('')
    } finally {
      setBusy(false)
    }
  }
  if (!info) return <div className="login"><Spinner /></div>
  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <div className="brand big"><img className="login-mark" src="/ducktainer-mark.svg" alt="" /> ducktainer</div>
        {mode === 'user' ? (
          <>
            <p className="muted">{tr('Sign in with your system user.')}</p>
            <p className="help">{tr('No access yet? Ask an administrator to add your account to the ducktainer-admins (full access) or ducktainer-viewers (read-only) group.')}</p>
            <input autoFocus autoComplete="username" placeholder={tr('user')} value={username} onChange={(e) => setUsername(e.target.value)} />
            <input type="password" autoComplete="current-password" placeholder={tr('password')} value={password} onChange={(e) => setPassword(e.target.value)} />
          </>
        ) : (
          <>
            <p className="muted">{tr('Emergency access token:')} <code>{info?.os === 'windows' ? 'ducktainer token --rotate' : 'sudo ducktainer token --rotate'}</code> {tr('on the server.')}</p>
            <input className="mono" type="password" autoFocus placeholder="token" value={token} onChange={(e) => setToken(e.target.value)} />
          </>
        )}
        {err && <div className="alert alert-bad">{err}</div>}
        <button className="btn btn-primary" disabled={busy || (mode === 'user' ? !username || !password : !token)}>{busy ? <Spinner /> : tr('Sign in')}</button>
        <select className="lang-select" value={getLang()} onChange={(e) => setLang(e.target.value)} aria-label="Language">
          {LANGUAGES.map((l) => <option key={l.id} value={l.id}>{l.label}</option>)}
        </select>
        {info.pam && info.token && (
          <button type="button" className="link small center-link" onClick={() => (setMode(mode === 'user' ? 'token' : 'user'), setErr(''))}>
            {mode === 'user' ? tr('Sign in with the token') : tr('Sign in with a system user')}
          </button>
        )}
      </form>
    </div>
  )
}
