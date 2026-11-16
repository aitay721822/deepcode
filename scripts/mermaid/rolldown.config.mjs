// Bundles web/'s Mermaid parser (entry.mjs) into one script goja runs; scripts/build.sh gzips it into the Go
// package that embeds it. Run from the repository's root:
//   NODE_ENV=production web/node_modules/.bin/rolldown -c scripts/mermaid/rolldown.config.mjs
import { fileURLToPath } from 'node:url'

const here = (p) => fileURLToPath(new URL(p, import.meta.url))

// Mermaid builds the same case-insensitive RegExp on every parse (detectDirective); goja's regexp2 spends about
// 40 ms on each build folding Unicode case, so it is built once (replace() resets lastIndex: sharing it is safe).
// A Mermaid that no longer builds it so fails the build, rather than parsing slowly unnoticed.
const at = 'const commentWithoutDirectives = new RegExp('
let hoisted = false
const hoist = {
  name: 'hoist-directive-regexp',
  transform(code) {
    if (!code.includes(at)) return null
    hoisted = true
    return `var __cwd;\n${code.replace(at, `const commentWithoutDirectives = __cwd || (__cwd = new RegExp(`).replace(/("ig"\s*\))/, '$1)')}`
  },
  buildEnd() {
    if (!hoisted) this.error(`Mermaid no longer holds "${at}": check detectDirective and update the hoist`)
  },
}

export default {
  input: here('./entry.mjs'),
  platform: 'browser',
  plugins: [hoist],
  resolve: {
    alias: {
      mermaid: here('../../web/node_modules/mermaid/dist/mermaid.core.mjs'),
      dompurify: here('./dompurify.mjs'),
    },
  },
  transform: { define: { 'process.env.NODE_ENV': '"production"' } },
  output: {
    file: here('../../internal/knowledge/wiki/outline/skeleton/mermaid/dist/parse.js'),
    format: 'iife',
    minify: true,
    codeSplitting: false,
    comments: false,
  },
}
