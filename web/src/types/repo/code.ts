export type Citation = { path: string; startLine: number; endLine: number }
export type FileEntry = { path: string; language: string; size: number; lines: number }
export type FileContent = { path: string; language: string; content: string; commitSha: string }
export type SymbolHit = {
  name: string
  qualifiedName: string
  kind: string
  path: string
  startLine: number
  endLine: number
  signature: string
}
