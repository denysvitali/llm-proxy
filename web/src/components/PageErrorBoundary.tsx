import { Component, type ReactNode } from 'react'
import { Alert, Button, Stack, Text } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'

// Keep the shell usable if a page fails to render. App keys this by pathname,
// so navigating away also clears the failed page's state.
export class PageErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <Alert title="This page couldn't be displayed" color="red" icon={<IconAlertTriangle size={20} />} role="alert">
        <Stack gap="md" align="flex-start">
          <Text size="sm">Try loading it again. You can still use the navigation to open another page.</Text>
          <Button variant="default" onClick={() => window.location.reload()}>Reload page</Button>
        </Stack>
      </Alert>
    )
  }
}
