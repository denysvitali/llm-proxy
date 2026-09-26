import { Component, type ReactNode } from 'react'
import { Alert, Button, Stack, Text } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'

// Keep the shell usable if a page fails to render. App keys this by pathname,
// so navigating away also clears the failed page's state.
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
      <Alert title="This page couldn't be displayed" color="red" icon={<IconAlertTriangle size={20} />} role="alert">
        <Stack gap="md" align="flex-start">
          <Text size="sm">Try loading it again. You can still use the navigation to open another page.</Text>
          <Button variant="default" onClick={() => this.setState({ failed: false, error: null })}>Try again</Button>
          <Button variant="default" onClick={() => window.location.reload()}>Reload page</Button>
          {this.state.error && (
            <details style={{ width: '100%' }}>
              <summary style={{ cursor: 'pointer', fontSize: 'var(--mantine-font-size-sm)' }}>Error details</summary>
              <Text size="xs" c="dimmed" component="pre" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                {this.state.error.message}
              </Text>
            </details>
          )}
        </Stack>
      </Alert>
    )
  }
}
