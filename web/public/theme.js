// Runs from <head>, before the first paint, so the page never flashes the wrong theme. Dark-first: no
// stored choice becomes 'dark' (the theme switch reads localStorage.theme). A file, not an inline
// script, because the Content-Security-Policy runs only scripts this server sends as files.
try {
  if (!localStorage.getItem('theme')) localStorage.setItem('theme', 'dark')
  document.documentElement.classList.toggle('dark', localStorage.getItem('theme') === 'dark')
} catch {
  document.documentElement.classList.add('dark')
}
