import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev server proxies API calls to a locally running llm-proxy so
// `npm run dev` works against real data; production builds are embedded
// into the Go binary and served same-origin.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/stats': 'http://127.0.0.1:8090',
      '/api': 'http://127.0.0.1:8090',
      '/v1/models': 'http://127.0.0.1:8090',
      '/metrics': 'http://127.0.0.1:8090',
    },
  },
})
