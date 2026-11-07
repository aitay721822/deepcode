export const shortSha = (sha: string) => sha.slice(0, 7)

/** SQLite CURRENT_TIMESTAMP is UTC without a zone marker. */
const parse = (sqlite: string) => new Date(`${sqlite.replace(' ', 'T')}Z`)

export function formatDate(sqlite: string) {
  const d = parse(sqlite)
  return Number.isNaN(d.getTime())
    ? sqlite
    : d.toLocaleDateString('zh-TW', { year: 'numeric', month: 'long', day: 'numeric' })
}

/** Local date and time, "11/5 15:04" by default, on a 24-hour clock. */
export function formatTime(
  sqlite: string,
  opts: Intl.DateTimeFormatOptions = { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' },
) {
  const d = parse(sqlite)
  return Number.isNaN(d.getTime()) ? sqlite : d.toLocaleString('zh-TW', { hourCycle: 'h23', ...opts })
}

export function formatStars(n: number) {
  return n >= 1000 ? `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k` : String(n)
}

/** "1 位使用者", "3 位使用者": a count and its unit, many for a unit that differs past one. */
export const plural = (n: number, one: string, many = one) => `${n} ${n === 1 ? one : many}`
