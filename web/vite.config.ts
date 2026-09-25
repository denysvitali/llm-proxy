import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev server proxies API calls to a locally running llm-proxy so
// `npm run dev` works against real data; production builds are embedded
// into the Go binary and served same-origin.
const apiTarget = process.env.LLM_PROXY_API_TARGET || 'http://127.0.0.1:8090'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/stats': apiTarget,
      '/api': { target: apiTarget, ws: true },
      '/v1/models': apiTarget,
      '/metrics': apiTarget,
      '/login': apiTarget,
    },
  },
})
