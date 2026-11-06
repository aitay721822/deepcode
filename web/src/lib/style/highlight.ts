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

const languages = { bash, cpp, csharp, css, dockerfile, go, ini, java, javascript, json, markdown, python, rust, sql, typescript, xml, yaml }
for (const [name, lang] of Object.entries(languages)) hljs.registerLanguage(name, lang)

// deepcode's language names (and common fence tags) → highlight.js names
const alias: Record<string, string> = {
  tsx: 'typescript', ts: 'typescript', js: 'javascript', jsx: 'javascript', html: 'xml', toml: 'ini',
  scss: 'css', less: 'css', sh: 'bash', shell: 'bash', powershell: 'bash', makefile: 'bash', c: 'cpp',
  'go.mod': 'go', yml: 'yaml', md: 'markdown', py: 'python', rs: 'rust', cs: 'csharp',
}

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

/** Returns escaped HTML, highlighted when the language is known. */
export function highlight(code: string, language: string) {
  const lang = Object.hasOwn(alias, language) ? alias[language] : language // not alias['constructor']
  if (!hljs.getLanguage(lang)) return escape(code)
  return hljs.highlight(code, { language: lang, ignoreIllegals: true }).value
}
