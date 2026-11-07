import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import cpp from 'highlight.js/lib/languages/cpp'
import csharp from 'highlight.js/lib/languages/csharp'
import css from 'highlight.js/lib/languages/css'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import go from 'highlight.js/lib/languages/go'
import ini from 'highlight.js/lib/languages/ini'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import markdown from 'highlight.js/lib/languages/markdown'
import python from 'highlight.js/lib/languages/python'
import rust from 'highlight.js/lib/languages/rust'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

const languages = {
  bash,
  cpp,
  csharp,
  css,
  dockerfile,
  go,
  ini,
  java,
  javascript,
  json,
  markdown,
  python,
  rust,
  sql,
  typescript,
  xml,
  yaml,
}
for (const [name, lang] of Object.entries(languages)) hljs.registerLanguage(name, lang)

// deepcode's language names (and common fence tags) → highlight.js names
const alias: Record<string, string> = {
  tsx: 'typescript',
  ts: 'typescript',
  js: 'javascript',
  jsx: 'javascript',
  html: 'xml',
  toml: 'ini',
  scss: 'css',
  less: 'css',
  sh: 'bash',
  shell: 'bash',
  powershell: 'bash',
  makefile: 'bash',
  c: 'cpp',
  'go.mod': 'go',
  yml: 'yaml',
  md: 'markdown',
  py: 'python',
  rs: 'rust',
  cs: 'csharp',
}

const escapeHtml = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

// Characters that reorder or hide text ("Trojan Source"): the bidirectional controls; invisible ones such as the
// zero-width space and the soft hyphen; the Hangul fillers, letters that can even name a variable; and the tag
// characters and variation selectors that can smuggle text. Not the joiners nor the emoji presentation selectors
// (U+FE0E, U+FE0F), which emoji and scripts need. Each stays in the text, so copied text is unchanged, inside a span
// that shows its code point and isolates it, so a direction control reaches nothing past it (source.css).
const hidden =
  // biome-ignore lint/suspicious/noMisleadingCharacterClass: the combining marks are escaped and listed on purpose; the u flag matches each alone
  /[\u00ad\u034f\u061c\u115f\u1160\u180e\u200b\u200e\u200f\u202a-\u202e\u2060-\u206f\u3164\ufe00-\ufe0d\ufeff\uffa0\ufff9-\ufffb\u{e0000}-\u{e007f}\u{e0100}-\u{e01ef}]/gu
const around = new RegExp(`(${hidden.source})`, 'u')

/** A hidden character's code point, as its marker shows it ("202E"). */
export const codePoint = (c: string) => (c.codePointAt(0) ?? 0).toString(16).toUpperCase().padStart(4, '0')

/** Splits text around its hidden characters: text at even indexes, a hidden character at each odd one. */
export const splitHidden = (text: string) => text.split(around)

const reveal = (html: string) =>
  html.replace(hidden, (c) => `<span class="hidden-char" data-cp="${codePoint(c)}">${c}</span>`)

/** Returns escaped HTML, highlighted when the language is known, with hidden characters shown. */
export function highlight(code: string, language: string) {
  const text = code.replace(/^\ufeff/, '') // a file's byte order mark: its encoding, not its text
  const lang = Object.hasOwn(alias, language) ? alias[language] : language // not alias['constructor']
  if (!hljs.getLanguage(lang)) return reveal(escapeHtml(text))
  return reveal(hljs.highlight(text, { language: lang, ignoreIllegals: true }).value)
}
