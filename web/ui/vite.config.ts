import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const target = process.env.NUVIO_DEV_SERVER || 'http://127.0.0.1:8096'
const apiPaths = ['/api', '/Users', '/Items', '/Videos', '/videos', '/System', '/Shows', '/Sessions', '/Search', '/Branding', '/DisplayPreferences', '/UserViews']

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    assetsDir: 'assets',
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(apiPaths.map((p) => [p, { target, changeOrigin: false }])),
  },
})
