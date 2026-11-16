/**
 * One repository's spend on one model. repoId is '' for a repository deleted since, or for requests no
 * repository made. inputTokens include cachedTokens, the part the server read from its prompt cache.
 * unreportedCalls are the calls the server counted no tokens for (a subscription agent's answer, a refused
 * request); estInputTokens and estOutputTokens the tokens of those answered, as a local tokenizer estimated them.
 * milliseconds are every call's time, summed.
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
  unreportedCalls: number
  estInputTokens: number
  estOutputTokens: number
  milliseconds: number
}
