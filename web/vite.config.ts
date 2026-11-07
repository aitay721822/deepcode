import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// `npm run dev` proxies the API to a running `deepcode serve`. The router plugin, before react(), writes
// src/routeTree.gen.ts from the route files (committed: tsc reads it before vite build runs).
export default defineConfig({
  plugins: [
    tanstackRouter({ routesDirectory: './src/pages/routes', generatedRouteTree: './src/routeTree.gen.ts' }),
    react(),
    tailwindcss(),
  ],
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
