import { Component, type ReactNode } from 'react'
import { Box, Button, Group, Stack, Text } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import './primitives.css'

// Keep the shell usable if a page fails to render. App keys this by pathname,
// so navigating away also clears the failed page's state. The fallback uses the
// same calm alert anatomy as ErrorRetryCard: icon tile, title, body.
export class PageErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean; error: Error | null }> {
  state = { failed: false, error: null as Error | null }

  static getDerivedStateFromError(error: Error) {
    return { failed: true, error }
  }

  componentDidCatch(error: Error) {
    console.error(error)
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <Box component="section" role="alert" className="page-error-card">
        <Group gap="sm" align="flex-start" wrap="nowrap">
          <Box className="page-error-icon" aria-hidden="true">
            <IconAlertTriangle size={18} stroke={1.8} />
          </Box>
          <Stack gap={6} className="page-error-copy">
            <Text className="page-error-title">This page couldn&apos;t be displayed</Text>
            <Text size="sm" c="dimmed">Try loading it again. You can still use the navigation to open another page.</Text>
          </Stack>
        </Group>
        <Group gap="xs" className="page-error-actions">
          <Button variant="default" onClick={() => this.setState({ failed: false, error: null })}>Try again</Button>
          <Button variant="default" onClick={() => window.location.reload()}>Reload page</Button>
        </Group>
        {this.state.error && (
          <details className="page-error-details">
            <summary>Error details</summary>
            <Text size="xs" c="dimmed" component="pre" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
              {this.state.error.message}
            </Text>
          </details>
        )}
      </Box>
    )
  }
}
