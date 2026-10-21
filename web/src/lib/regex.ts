// Builds a regular expression (Go/RE2 syntax, which is what the alert
// engine uses) from a piece of a log line.

// whitespace runs match any amount of whitespace (columns are often padded)
const escape = (s: string) => s.replace(/[\\^$.|?*+()[\]{}]/g, '\\$&').replace(/\s+/g, '\\s+')

/** Drops the date/clock a log line starts with: it changes every time and
 * says nothing about the event. Returns the line unchanged if nothing else is left. */
export function trimLeadingStamp(line: string): string {
  const rest = line.replace(/^[\s[(]*\d[\d\s:./\-T,Z+]*[\])]?\s*/, '')
  return rest.trim() ? rest : line
}

// the parts of a line that change from one occurrence to the next
const VARIABLE: [RegExp, string][] = [
  [/\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?/, '\\d{4}-\\d{2}-\\d{2}[T ]\\d{2}:\\d{2}:\\d{2}([.,]\\d+)?(Z|[+-]\\d{2}:?\\d{2})?'],
  [/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/, '[0-9a-fA-F-]{36}'],
  [/\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b/, '\\d{1,3}(\\.\\d{1,3}){3}(:\\d+)?'],
  [/\b0x[0-9a-fA-F]+\b/, '0x[0-9a-fA-F]+'],
  [/\b(?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])[0-9a-f]{12,}\b/, '[0-9a-f]{12,}'],
  // numbers, also chained with : . / - (clocks, dates, dotted versions in any format)
  [/\d+(?:[:./-]\d+)*/, '\\d+([:./-]\\d+)*'],
]
const TOKEN = new RegExp(VARIABLE.map(([re]) => `(${re.source})`).join('|'), 'g')

/** Pattern matching `text`. With `generalize`, numbers, dates, IPs and ids
 * become classes so the rule keeps matching when they change. */
export function suggestPattern(text: string, generalize: boolean): string {
  const t = text.replace(/\x1b\[[\d;?]*[A-Za-z]/g, '').trim()
  if (!generalize) return escape(t)
  let out = ''
  let last = 0
  for (const m of t.matchAll(TOKEN)) {
    out += escape(t.slice(last, m.index))
    const k = m.slice(1).findIndex((g) => g !== undefined)
    out += VARIABLE[k][1]
    last = m.index! + m[0].length
  }
  return out + escape(t.slice(last))
}

/** Compile a pattern in the browser to preview it (RE2's (?i) prefix aside). */
export function previewRegex(pattern: string): RegExp | null {
  let flags = ''
  let p = pattern
  const m = /^\(\?([imsU]+)\)/.exec(p)
  if (m) {
    p = p.slice(m[0].length)
    flags = m[1].replace(/U/g, '')
  }
  try {
    return new RegExp(p, flags)
  } catch {
    return null
  }
}
