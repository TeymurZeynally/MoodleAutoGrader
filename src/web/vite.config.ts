import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In dev the API runs in compose on :8180; the production build is embedded into the Go binary.
export default defineConfig({ plugins: [react()], server: { proxy: { '/api': 'http://127.0.0.1:8180' } }, build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 2500 } })
