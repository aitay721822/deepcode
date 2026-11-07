/** A path segment naming a record: a positive integer written plainly (not 1e3, 0x10 or 007) and exact as a number. */
export const isId = (s?: string) => !!s && /^[1-9]\d*$/.test(s) && Number.isSafeInteger(+s)

/** A path segment naming a repository: its CUID2, a letter and 23 more letters or digits. */
export const isRepoId = (s?: string) => !!s && /^[a-z][0-9a-z]{23}$/.test(s)

/** Percent-encodes each segment of a repository path, keeping the slashes. */
export const encodePath = (p: string) => p.split('/').map(encodeURIComponent).join('/')

/** decodeURIComponent that leaves malformed input (a stray %) as it is instead of throwing. */
export function decode(s: string) {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

/**
 * The id a wiki heading gets from its text (WikiArticle). Models write in-page links GitHub's way (#whats-new for
 * "What's New", #data-flow--storage for "Data Flow & Storage"), and such a link reaches its heading through the same
 * rule: punctuation dropped, then each run of spaces, hyphens and underscores made one hyphen. Prefixed: a heading
 * "Root" must not take the app's own id="root".
 */
export const headingId = (text: string) =>
  `h-${
    text
      .toLowerCase()
      .replace(/[^\p{L}\p{M}\p{N}\s_-]/gu, '')
      .replace(/[\s_-]+/g, '-')
      .replace(/^-|-$/g, '') || 'section'
  }`
