import {
  Alert,
  Button,
  Card,
  Code,
  CopyButton,
  Group,
  List,
  Loader,
  Stack,
  Tabs,
  Text,
  Title,
} from '@mantine/core'
import { IconCheck, IconCopy, IconInfoCircle, IconTerminal2 } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchOverview } from '../api'
import { Fade } from '../App'

export default function SetupPage() {
  const q = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const ov = q.data

  return (
    <Fade fetching={q.isFetching}>
      {!ov ? (
        <Group justify="center" py="xl">
          <Loader size="sm" />
        </Group>
      ) : (
        <Stack gap="lg" maw={820}>
          <div>
            <Title order={4} mb={2}>Setup</Title>
            <Text size="xs" c="dimmed">Point a coding agent at this proxy</Text>
          </div>
          <List spacing="xs">
            <List.Item>
              Proxy listens on <Code>{ov.listen}</Code>, authentication{' '}
              {ov.authEnabled ? 'enabled (llx_… keys)' : 'disabled'}
            </List.Item>
            {ov.exampleModel !== '<model>' && (
              <List.Item>
                Example model: <Code>{ov.exampleModel}</Code>
              </List.Item>
            )}
          </List>

          {ov.authEnabled && (
            <Alert
              color="blue"
              variant="light"
              icon={<IconInfoCircle size={16} />}
              title="Authentication is enabled"
            >
              Replace the placeholder token in each snippet with one of your proxy
              API keys.
            </Alert>
          )}

          <Card withBorder radius="lg" p={0}>
            <Tabs defaultValue="claude" keepMounted={false}>
              <Tabs.List px="md" pt={6}>
                <Tabs.Tab value="claude" leftSection={<IconTerminal2 size={14} />}>
                  Claude Code
                </Tabs.Tab>
                <Tabs.Tab value="codex" leftSection={<IconTerminal2 size={14} />}>
                  Codex CLI
                </Tabs.Tab>
              </Tabs.List>
              <Tabs.Panel value="claude">
                <Snippet title="Claude Code" snippet={ov.claudeSnippet} />
              </Tabs.Panel>
              <Tabs.Panel value="codex">
                <Snippet title="Codex CLI" snippet={ov.codexSnippet} />
              </Tabs.Panel>
            </Tabs>
          </Card>
        </Stack>
      )}
    </Fade>
  )
}

function Snippet({ title, snippet }: { title: string; snippet: string }) {
  return (
    <div>
      <Group justify="space-between" px="md" py="xs">
        <Text size="xs" c="dimmed">
          Point your client at the proxy:
        </Text>
        <CopyButton value={snippet}>
          {({ copied, copy }) => (
            <Button
              size="compact-xs"
              variant={copied ? 'light' : 'default'}
              color={copied ? 'teal' : undefined}
              leftSection={copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
              onClick={copy}
              aria-label={`Copy ${title} snippet`}
            >
              {copied ? 'Copied' : 'Copy'}
            </Button>
          )}
        </CopyButton>
      </Group>
      <pre className="snippet-block">
        <code>{snippet}</code>
      </pre>
    </div>
  )
}
