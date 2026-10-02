import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // Control plane runs on :8080 during development (see `make dev-server`).
    proxy: { '/api': 'http://localhost:8080', '/healthz': 'http://localhost:8080' },
  },
})
