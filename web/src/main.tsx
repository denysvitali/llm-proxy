import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import '@mantine/core/styles.css'
import '@mantine/charts/styles.css'
import './index.css'
import { theme } from './theme'
import App from './App'
import { dashboardQueryDefaults } from './queries'
import { LiveStatsProvider } from './useLiveUpdates'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: dashboardQueryDefaults,
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="auto">
      <QueryClientProvider client={queryClient}>
        <LiveStatsProvider>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </LiveStatsProvider>
      </QueryClientProvider>
    </MantineProvider>
  </StrictMode>,
)
