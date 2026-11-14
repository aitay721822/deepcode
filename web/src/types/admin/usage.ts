/**
 * One repository's spend on one model. repoId is '' for a repository deleted since, or for requests no
 * repository made. inputTokens include cachedTokens, the part the server read from its prompt cache.
 */
export type UsageRow = {
  repoId: string
  repoOwner: string
  repoName: string
  model: string
  calls: number
  inputTokens: number
  cachedTokens: number
  outputTokens: number
}
