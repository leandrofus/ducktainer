import type { APIContext } from 'astro'
import { strings } from '../i18n'
import { docsFor, slugOf } from '../docs'
import { REPO_URL, docsPath } from '../paths'

// A plain-text map of the English docs for AI assistants (llmstxt.org).
export async function GET({ site }: APIContext) {
  const docs = await docsFor('en')
  const lines = docs.map((d) => `- [${d.data.title}](${new URL(docsPath('en', slugOf(d)), site)}): ${d.data.description}`)
  const text = [
    '# ducktainer',
    '',
    `> ${strings.en.description}`,
    '',
    '## Documentation',
    '',
    ...lines,
    '',
    '## Project',
    '',
    `- [Source code](${REPO_URL})`,
    `- [Releases](${REPO_URL}/releases)`,
    `- [Contributing](${REPO_URL}/blob/main/CONTRIBUTING.md)`,
    '',
  ].join('\n')
  return new Response(text, { headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
}
