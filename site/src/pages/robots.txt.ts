import type { APIContext } from 'astro'

// Everything is public; the sitemap is what search engines should read.
export function GET({ site }: APIContext) {
  const base = import.meta.env.BASE_URL.replace(/\/$/, '')
  const sitemap = new URL(`${base}/sitemap-index.xml`, site)
  return new Response(`User-agent: *\nAllow: /\n\nSitemap: ${sitemap}\n`, {
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  })
}
