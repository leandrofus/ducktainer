import type { APIContext } from 'astro'

// Lets browsers install the site as an app; the favicon doubles as its icon.
export function GET(_: APIContext) {
  const base = import.meta.env.BASE_URL.replace(/\/$/, '')
  const manifest = {
    name: 'ducktainer',
    short_name: 'ducktainer',
    start_url: `${base}/`,
    scope: `${base}/`,
    display: 'standalone',
    theme_color: '#0B1626',
    background_color: '#0B1626',
    icons: [{ src: `${base}/favicon.svg`, sizes: 'any', type: 'image/svg+xml' }],
  }
  return new Response(JSON.stringify(manifest, null, 2), {
    headers: { 'Content-Type': 'application/manifest+json' },
  })
}
