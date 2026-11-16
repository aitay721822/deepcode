// The Mermaid parser deepcode checks a wiki page's diagrams with before it writes the page
// (internal/knowledge/wiki/outline/skeleton/mermaid): the one web/ draws with, so a diagram that passes draws.
// parse resolves on a diagram that reads and rejects with the parser's error on one that does not.
import mermaid from 'mermaid'

globalThis.mermaidParse = (code) => mermaid.parse(code)
