export const shortSha = (sha: string) => sha.slice(0, 7)

/** SQLite CURRENT_TIMESTAMP is UTC without a zone marker. */
const parse = (sqlite: string) => new Date(`${sqlite.replace(' ', 'T')}Z`)

export function formatDate(sqlite: string) {
  const d = parse(sqlite)
  return Number.isNaN(d.getTime())
    ? sqlite
    : d.toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric' })
}

/** Local date and time, "5 Nov, 15:04" by default. */
export function formatTime(
  sqlite: string,
  opts: Intl.DateTimeFormatOptions = { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' },
) {
  const d = parse(sqlite)
  return Number.isNaN(d.getTime()) ? sqlite : d.toLocaleString('en-GB', opts)
}

export function formatStars(n: number) {
  return n >= 1000 ? `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k` : String(n)
}

/** "1 user", "3 users"; many for a plural other than one + s. */
export const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`
