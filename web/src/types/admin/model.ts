/**
 * The chat model as it stands. provider is 'acp' (a subscription agent), 'openai' (an OpenAI-compatible server) or ''
 * when none is set. context is its context in tokens, 0 while unknown; overhead the tokens its backend adds to each
 * request, and scale how much larger its tokens are than the local tokenizer's. agent is what an agent said it is
 * (name and version '' before it starts), its model option's value, and its last failure since it last answered,
 * at a time in UTC as SQLite writes one.
 */
export type ChatModel = {
  provider: '' | 'acp' | 'openai'
  name: string
  context: number
  overhead: number
  scale: number
  agent?: { name: string; version: string; model: string; trouble: string; at: string }
}

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
