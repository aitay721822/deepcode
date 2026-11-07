// Self-hosted fonts, so the page asks no third party: Newsreader (headings, prose), Schibsted Grotesk (interface), IBM Plex Mono (code, labels).
import '@fontsource-variable/newsreader/opsz.css'
import '@fontsource-variable/newsreader/opsz-italic.css'
import '@fontsource-variable/schibsted-grotesk/wght.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/400-italic.css'
import '@fontsource/ibm-plex-mono/500.css'
import '@fontsource/ibm-plex-mono/600.css'
import './index.css' // before any component, so every component stylesheet cascades after the base
import { createRoot } from 'react-dom/client'
import { App } from './App'

const root = document.getElementById('root')
if (!root) throw new Error('index.html has no #root')
createRoot(root).render(<App />)
