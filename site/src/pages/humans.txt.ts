// The people behind ducktainer, in the humans.txt format (humanstxt.org).
const humans = `/* TEAM */
Maintainer: Leandro Fusco
Contact: https://github.com/leandrofus/ducktainer/issues
Contributor: Rodrigo Medrano

/* SITE */
Last update: ${new Date().toISOString().slice(0, 10)}
Standards: HTML5, CSS3
Components: Astro, Montserrat, IBM Plex Sans, IBM Plex Mono
Software: ducktainer, AGPL-3.0-or-later
Source: https://github.com/leandrofus/ducktainer
`

export function GET() {
  return new Response(humans, {
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  })
}
