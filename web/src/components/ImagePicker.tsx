import { useEffect, useState } from 'react'
import { t as tr } from '../i18n'
import { api, enc } from '../lib/api'
import type { Image } from '../lib/types'
import { Modal, openDialog } from '../ui/overlays'

/** Ask for an image reference, suggesting local images, the tags of the
 * typed repository and (Docker Hub) matching repositories. */
export function askImage(opts: { ep: string; title: string; value: string; confirm: string; help: string }): Promise<string | null> {
  return new Promise((resolve) => {
    openDialog((close) => <ImageDialog {...opts} onDone={(v) => (close(), resolve(v))} />)
  })
}

function repoOf(ref: string) {
  const i = ref.lastIndexOf(':')
  return i > ref.lastIndexOf('/') ? ref.slice(0, i) : ref
}

function ImageDialog({ ep, title, value, confirm, help, onDone }: { ep: string; title: string; value: string; confirm: string; help: string; onDone: (v: string | null) => void }) {
  const [v, setV] = useState(value)
  const [local, setLocal] = useState<string[]>([])
  const [remote, setRemote] = useState<string[]>([])

  useEffect(() => {
    api
      .get(`/e/${enc(ep)}/images`)
      .then((ims: Image[]) => setLocal((ims || []).flatMap((im) => im.RepoTags || []).filter((t) => t !== '<none>:<none>')))
      .catch(() => {})
  }, [ep])

  useEffect(() => {
    const ref = v.trim()
    if (!ref || ref.includes('@')) return setRemote([])
    const tagged = ref.lastIndexOf(':') > ref.lastIndexOf('/')
    const h = setTimeout(() => {
      // a typed repository (or a tag being typed): its tags; otherwise search Docker Hub
      const req = tagged || ref.includes('/') || local.some((l) => repoOf(l) === ref)
        ? api.get(`/registry/tags?image=${enc(repoOf(ref))}`).then((r: { tags: string[] }) => r.tags.map((t) => `${repoOf(ref)}:${t}`))
        : api.get(`/registry/search?q=${enc(ref)}`).then((r: { repos: string[] }) => r.repos)
      req.then(setRemote).catch(() => setRemote([]))
    }, 300)
    return () => clearTimeout(h)
  }, [v, local])

  const options = [...new Set([...remote, ...local.filter((l) => l.startsWith(v.trim().split(':')[0]))])]
  const name = v.trim()
  return (
    <Modal
      title={title}
      onClose={() => onDone(null)}
      footer={
        <>
          <button className="btn" onClick={() => onDone(null)}>
            {tr('Cancel')}
          </button>
          <button className="btn btn-primary" disabled={!name} onClick={() => onDone(name)}>
            {confirm}
          </button>
        </>
      }
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (name) onDone(name)
        }}
      >
        <label className="field">
          <span>{tr('Image')}</span>
          <input autoFocus list="image-suggestions" value={v} onChange={(e) => setV(e.target.value)} onFocus={(e) => e.target.select()} />
          <datalist id="image-suggestions">
            {options.map((o) => (
              <option key={o} value={o} />
            ))}
          </datalist>
        </label>
        <p className="help">{help}</p>
      </form>
    </Modal>
  )
}
