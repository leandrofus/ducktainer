import { copyFile } from 'node:fs/promises'
import { defineConfig } from 'astro/config'
import sitemap from '@astrojs/sitemap'

// Crawlers look for /sitemap.xml by default; the integration writes sitemap-index.xml.
// Copy the index to that name once the sitemap integration has written it.
function sitemapAlias() {
  return {
    name: 'sitemap-alias',
    hooks: {
      'astro:build:done': async ({ dir }) => {
        await copyFile(new URL('sitemap-index.xml', dir), new URL('sitemap.xml', dir))
      },
    },
  }
}

// Served by GitHub Pages at https://leandrofus.github.io/ducktainer/.
export default defineConfig({
  site: 'https://leandrofus.github.io',
  base: '/ducktainer',
  // the logo, mark and favicon are shared with the web UI
  publicDir: '../assets/brand',
  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'es', 'ru'],
    routing: { prefixDefaultLocale: false },
  },
  // each URL lists its translations (hreflang alternates) for search engines
  integrations: [
    sitemap({
      i18n: {
        defaultLocale: 'en',
        locales: { en: 'en-US', es: 'es-ES', ru: 'ru-RU' },
      },
    }),
    sitemapAlias(),
  ],
})
