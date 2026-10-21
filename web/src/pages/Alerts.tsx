import { useState } from 'react'
import { Bell, BellRing, Check, Mail, Pencil, Plus, Send, Trash2, Webhook } from 'lucide-react'
import { t as tr } from '../i18n'
import { api, enc } from '../lib/api'
import { dateTime } from '../lib/format'
import { isViewer } from '../lib/session'
import { invalidate, useResource } from '../lib/store'
import type { ActiveAlert, AlertChannel, AlertEntry, AlertRule, Endpoint } from '../lib/types'
import { Empty, Spinner, Tabs, Tag } from '../ui/common'
import { useMenu } from '../ui/ContextMenu'
import { Modal, openDialog, toast } from '../ui/overlays'

const KIND_LABEL: Record<string, string> = {
  host_down: tr('Host down'),
  agent_offline: tr('Agent offline'),
  log_match: tr('Log pattern'),
}

const ERROR_PATTERN = '(?i)\\b(error|fatal|panic|exception|traceback|critical)\\b'

const refresh = () => invalidate('/alerts')

function ago(iso: string) {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000))
  if (s < 90) return `${s}s`
  if (s < 5400) return `${Math.round(s / 60)} min`
  if (s < 129600) return `${Math.round(s / 3600)} h`
  return `${Math.round(s / 86400)} d`
}

function HostPicker({ value, onChange, hosts, all }: { value: string[]; onChange: (v: string[]) => void; hosts: string[]; all: string }) {
  return (
    <div className="row gap wrap">
      <label className="check"><input type="checkbox" checked={value.length === 0} onChange={(e) => e.target.checked && onChange([])} /> {all}</label>
      {hosts.map((h) => (
        <label key={h} className="check">
          <input type="checkbox" checked={value.includes(h)} onChange={(e) => onChange(e.target.checked ? [...value, h] : value.filter((x) => x !== h))} /> {h}
        </label>
      ))}
    </div>
  )
}

function RuleDialog({ initial, onClose }: { initial?: AlertRule; onClose: () => void }) {
  const hosts = useResource<Endpoint[]>('/endpoints').data || []
  const channels = useResource<{ rules: AlertRule[]; channels: AlertChannel[] }>('/alerts/config').data?.channels || []
  const [f, setF] = useState<AlertRule>(
    initial ?? { id: '', name: '', kind: 'host_down', enabled: true, for_seconds: 60, cooldown_minutes: 5, pattern: ERROR_PATTERN },
  )
  const [busy, setBusy] = useState(false)
  const set = (p: Partial<AlertRule>) => setF({ ...f, ...p })
  const agentHosts = hosts.filter((h) => h.url.startsWith('agent:')).map((h) => h.name)
  const plainHosts = hosts.filter((h) => !h.url.startsWith('agent:')).map((h) => h.name)
  const hostChoices = f.kind === 'agent_offline' ? agentHosts : f.kind === 'host_down' ? plainHosts : hosts.map((h) => h.name)
  return (
    <Modal
      title={initial ? tr('Edit {0}', initial.name) : tr('New alert rule')}
      icon={<BellRing size={18} />}
      wide
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
          <button
            className="btn btn-primary"
            disabled={busy || !f.name.trim() || (f.kind === 'log_match' && !f.pattern?.trim())}
            onClick={async () => {
              setBusy(true)
              try {
                await api.post('/alerts/rules', f)
                refresh()
                onClose()
              } catch (e) {
                toast.err(e)
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? <Spinner /> : <Check size={14} />} {tr('Save')}
          </button>
        </>
      }
    >
      <div className="grid-2">
        <label className="field"><span>{tr('Name')}</span><input value={f.name} onChange={(e) => set({ name: e.target.value })} autoFocus /></label>
        <label className="field">
          <span>{tr('Type')}</span>
          <select value={f.kind} onChange={(e) => set({ kind: e.target.value as AlertRule['kind'], endpoints: [] })}>
            {Object.entries(KIND_LABEL).map(([k, l]) => <option key={k} value={k}>{l}</option>)}
          </select>
        </label>
      </div>
      <div className="field" style={{ marginTop: 12 }}>
        <span>{tr('Hosts')}</span>
        <HostPicker value={f.endpoints || []} onChange={(endpoints) => set({ endpoints })} hosts={hostChoices} all={tr('All')} />
      </div>
      {f.kind === 'log_match' ? (
        <>
          <label className="field" style={{ marginTop: 12 }}>
            <span>{tr('Regular expression')}</span>
            <input className="mono" value={f.pattern || ''} onChange={(e) => set({ pattern: e.target.value })} />
          </label>
          <div className="row gap" style={{ marginTop: 4 }}>
            <button type="button" className="btn btn-sm" onClick={() => set({ pattern: ERROR_PATTERN })}>{tr('Errors preset')}</button>
            <span className="help grow">{tr('Matched against each log line of the running containers. Start it with (?i) to ignore case.')}</span>
          </div>
          <div className="grid-2" style={{ marginTop: 12 }}>
            <label className="field">
              <span>{tr('Containers')}</span>
              <input
                value={(f.containers || []).join(', ')}
                placeholder="web-*, api"
                onChange={(e) => set({ containers: e.target.value.split(',').map((x) => x.trim()).filter(Boolean) })}
              />
            </label>
            <label className="field">
              <span>{tr('Minutes between alerts of the same container')}</span>
              <input type="number" min={1} value={f.cooldown_minutes ?? 5} onChange={(e) => set({ cooldown_minutes: Number(e.target.value) })} />
            </label>
          </div>
          <p className="help">{tr('Container names accept * and ? (empty: all containers).')}</p>
        </>
      ) : (
        <label className="field" style={{ marginTop: 12 }}>
          <span>{tr('Seconds it must stay down before alerting')}</span>
          <input type="number" min={0} value={f.for_seconds ?? 60} onChange={(e) => set({ for_seconds: Number(e.target.value) })} />
        </label>
      )}
      <div className="field" style={{ marginTop: 12 }}>
        <span>{tr('Send to')}</span>
        {channels.length === 0 ? (
          <p className="help">{tr('No channels yet: add one in the Channels tab. Until then alerts only appear here and in the service log.')}</p>
        ) : (
          <HostPicker value={f.channels || []} onChange={(c) => set({ channels: c })} hosts={channels.map((c) => c.id)} all={tr('All')} />
        )}
      </div>
    </Modal>
  )
}

function ChannelDialog({ initial, onClose }: { initial?: AlertChannel; onClose: () => void }) {
  const [f, setF] = useState<AlertChannel>(initial ?? { id: '', name: '', kind: 'webhook', url: '', security: 'starttls', port: 587, to: [] })
  const [busy, setBusy] = useState(false)
  const set = (p: Partial<AlertChannel>) => setF({ ...f, ...p })
  const mail = f.kind === 'email'
  return (
    <Modal
      title={initial ? tr('Edit {0}', initial.name) : tr('New channel')}
      icon={mail ? <Mail size={18} /> : <Webhook size={18} />}
      wide
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>{tr('Cancel')}</button>
          <button
            className="btn"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              try {
                await api.post('/alerts/test', f)
                toast.ok(tr('Test alert sent'))
              } catch (e) {
                toast.err(e)
              } finally {
                setBusy(false)
              }
            }}
          >
            <Send size={14} /> {tr('Send test')}
          </button>
          <button
            className="btn btn-primary"
            disabled={busy || !f.name.trim()}
            onClick={async () => {
              setBusy(true)
              try {
                await api.post('/alerts/channels', f)
                refresh()
                onClose()
              } catch (e) {
                toast.err(e)
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? <Spinner /> : <Check size={14} />} {tr('Save')}
          </button>
        </>
      }
    >
      <div className="grid-2">
        <label className="field"><span>{tr('Name')}</span><input value={f.name} onChange={(e) => set({ name: e.target.value })} autoFocus /></label>
        <label className="field">
          <span>{tr('Type')}</span>
          <select value={f.kind} onChange={(e) => set({ kind: e.target.value as AlertChannel['kind'] })}>
            <option value="webhook">Webhook</option>
            <option value="email">{tr('Email (SMTP)')}</option>
          </select>
        </label>
      </div>
      {!mail ? (
        <>
          <label className="field" style={{ marginTop: 12 }}>
            <span>URL</span>
            <input className="mono" value={f.url || ''} placeholder="https://hooks.slack.com/…" onChange={(e) => set({ url: e.target.value })} />
          </label>
          <p className="help">{tr('A JSON POST with event, rule, kind, host, container, message and time, plus text and content for Slack, Mattermost and Discord. The URL is stored encrypted.')}</p>
        </>
      ) : (
        <>
          <div className="grid-2" style={{ marginTop: 12 }}>
            <label className="field"><span>{tr('SMTP server')}</span><input className="mono" value={f.host || ''} onChange={(e) => set({ host: e.target.value })} /></label>
            <div className="grid-2">
              <label className="field"><span>{tr('Port')}</span><input type="number" value={f.port || ''} onChange={(e) => set({ port: Number(e.target.value) })} /></label>
              <label className="field">
                <span>{tr('Security')}</span>
                <select
                  value={f.security}
                  onChange={(e) => {
                    const security = e.target.value as AlertChannel['security']
                    set({ security, port: { starttls: 587, tls: 465, none: 25 }[security!] })
                  }}
                >
                  <option value="starttls">STARTTLS</option>
                  <option value="tls">TLS</option>
                  <option value="none">{tr('None')}</option>
                </select>
              </label>
            </div>
            <label className="field"><span>{tr('User')}</span><input value={f.username || ''} onChange={(e) => set({ username: e.target.value })} /></label>
            <label className="field"><span>{tr('Password')}</span><input type="password" value={f.password || ''} onChange={(e) => set({ password: e.target.value })} /></label>
            <label className="field"><span>{tr('From')}</span><input value={f.from || ''} placeholder="ducktainer@example.com" onChange={(e) => set({ from: e.target.value })} /></label>
            <label className="field">
              <span>{tr('To')}</span>
              <input value={(f.to || []).join(', ')} placeholder="ops@example.com" onChange={(e) => set({ to: e.target.value.split(',').map((x) => x.trim()).filter(Boolean) })} />
            </label>
          </div>
          <p className="help">{tr('The password is stored encrypted.')}</p>
        </>
      )}
    </Modal>
  )
}

const stateTag = (s: string) =>
  s === 'firing' ? <Tag tone="bad">{tr('firing')}</Tag> : s === 'resolved' ? <Tag tone="ok">{tr('resolved')}</Tag> : <Tag tone="warn">{tr('event')}</Tag>

function Status() {
  const { data } = useResource<{ active: ActiveAlert[]; history: AlertEntry[] }>('/alerts', { poll: 10000 })
  if (!data) return <Spinner />
  return (
    <div>
      {data.active.length === 0 ? (
        <Empty icon={<Bell size={28} />} title={tr('Nothing is firing')}>{tr('Hosts and agents are fine according to your rules.')}</Empty>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{tr('Rule')}</th><th>{tr('Host')}</th><th>{tr('Detail')}</th><th>{tr('Since')}</th></tr></thead>
            <tbody>
              {data.active.map((a) => (
                <tr key={a.key}>
                  <td><strong>{a.rule}</strong> <Tag tone="bad">{KIND_LABEL[a.kind] ?? a.kind}</Tag></td>
                  <td>{a.endpoint}</td>
                  <td className="small">{a.message}</td>
                  <td className="small">{ago(a.since)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <h3 style={{ margin: '20px 0 8px' }}>{tr('History')}</h3>
      {data.history.length === 0 ? (
        <p className="muted">{tr('No alerts have fired yet.')}</p>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{tr('When')}</th><th /><th>{tr('Rule')}</th><th>{tr('Host')}</th><th>{tr('Detail')}</th></tr></thead>
            <tbody>
              {data.history.map((h, i) => (
                <tr key={i}>
                  <td className="small">{dateTime(h.time)}</td>
                  <td>{stateTag(h.state)}</td>
                  <td>{h.rule}</td>
                  <td className="small">{h.endpoint}{h.container ? ` / ${h.container}` : ''}</td>
                  <td className="small mono" style={{ wordBreak: 'break-word' }}>{h.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Rules() {
  const { data } = useResource<{ rules: AlertRule[]; channels: AlertChannel[] }>('/alerts/config')
  const menu = useMenu()
  const save = (r: AlertRule) => api.post('/alerts/rules', r).then(refresh).catch(toast.err)
  const edit = (r?: AlertRule) => openDialog((c) => <RuleDialog initial={r} onClose={c} />)
  const del = (r: AlertRule) => api.del(`/alerts/rules/${enc(r.id)}`).then(refresh).catch(toast.err)
  return (
    <div>
      <div className="row gap section-head">
        <p className="muted grow">{tr('What to watch: hosts that stop answering, agents that disconnect and log lines that match a pattern.')}</p>
        <button className="btn btn-primary" onClick={() => edit()}><Plus size={14} /> {tr('New alert rule')}</button>
      </div>
      {data?.rules.length === 0 && <Empty icon={<BellRing size={28} />} title={tr('No rules')}>{tr('Add a rule to be told when something fails.')}</Empty>}
      {!!data?.rules.length && (
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{tr('Name')}</th><th>{tr('Type')}</th><th>{tr('Hosts')}</th><th>{tr('Detail')}</th><th>{tr('Active')}</th><th style={{ width: 40 }} /></tr></thead>
            <tbody>
              {data.rules.map((r) => (
                <tr
                  key={r.id}
                  onDoubleClick={() => edit(r)}
                  onContextMenu={menu(() => [
                    { label: tr('Edit…'), icon: <Pencil size={14} />, onClick: () => edit(r) },
                    { label: r.enabled ? tr('Disable') : tr('Enable'), onClick: () => save({ ...r, enabled: !r.enabled }) },
                    'sep',
                    { label: tr('Delete'), icon: <Trash2 size={14} />, danger: true, onClick: () => del(r) },
                  ])}
                >
                  <td><strong>{r.name}</strong></td>
                  <td>{KIND_LABEL[r.kind] ?? r.kind}</td>
                  <td className="small">{r.endpoints?.length ? r.endpoints.join(', ') : <span className="muted">{tr('All')}</span>}</td>
                  <td className="small mono">{r.kind === 'log_match' ? r.pattern : tr('after {0}s', String(r.for_seconds ?? 0))}</td>
                  <td>
                    <button className={`switch${r.enabled ? ' on' : ''}`} aria-label={tr('Active')} onClick={() => save({ ...r, enabled: !r.enabled })}><span /></button>
                  </td>
                  <td><button className="icon-btn" onClick={() => del(r)}><Trash2 size={14} /></button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Channels() {
  const { data } = useResource<{ rules: AlertRule[]; channels: AlertChannel[] }>('/alerts/config')
  const menu = useMenu()
  const edit = (c?: AlertChannel) => openDialog((close) => <ChannelDialog initial={c} onClose={close} />)
  const del = (c: AlertChannel) => api.del(`/alerts/channels/${enc(c.id)}`).then(refresh).catch(toast.err)
  return (
    <div>
      <div className="row gap section-head">
        <p className="muted grow">{tr('Where alerts are delivered.')}</p>
        <button className="btn btn-primary" onClick={() => edit()}><Plus size={14} /> {tr('New channel')}</button>
      </div>
      {data?.channels.length === 0 && <Empty icon={<Send size={28} />} title={tr('No channels')}>{tr('Add a webhook (Slack, Discord, ntfy…) or an SMTP server.')}</Empty>}
      {!!data?.channels.length && (
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{tr('Name')}</th><th>{tr('Type')}</th><th>{tr('Destination')}</th><th style={{ width: 40 }} /></tr></thead>
            <tbody>
              {data.channels.map((c) => (
                <tr
                  key={c.id}
                  onDoubleClick={() => edit(c)}
                  onContextMenu={menu(() => [
                    { label: tr('Edit…'), icon: <Pencil size={14} />, onClick: () => edit(c) },
                    'sep',
                    { label: tr('Delete'), icon: <Trash2 size={14} />, danger: true, onClick: () => del(c) },
                  ])}
                >
                  <td><strong>{c.name}</strong></td>
                  <td>{c.kind === 'email' ? <><Mail size={13} /> Email</> : <><Webhook size={13} /> Webhook</>}</td>
                  <td className="small mono">{c.kind === 'email' ? (c.to || []).join(', ') : tr('(URL hidden)')}</td>
                  <td><button className="icon-btn" onClick={() => del(c)}><Trash2 size={14} /></button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export function Alerts({ tab }: { tab?: string }) {
  const viewer = isViewer()
  const [active, setActive] = useState(viewer ? 'status' : tab || 'status')
  return (
    <div className="page">
      <div className="page-head">
        <div className="page-title"><Bell size={20} /><h1>{tr('Alerts')}</h1></div>
      </div>
      {!viewer && (
        <Tabs
          tabs={[{ id: 'status', label: tr('Status') }, { id: 'rules', label: tr('Rules') }, { id: 'channels', label: tr('Channels') }]}
          active={active}
          onChange={setActive}
        />
      )}
      {active === 'status' && <Status />}
      {!viewer && active === 'rules' && <Rules />}
      {!viewer && active === 'channels' && <Channels />}
    </div>
  )
}
