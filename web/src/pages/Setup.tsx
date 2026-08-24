import {
  Alert,
  Code,
  CopyButton,
  Group,
  List,
  Loader,
  Stack,
  Text,
  Title,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { IconCheck, IconCopy } from '@tabler/icons-react'
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
          <Title order={4}>Setup</Title>
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
            <Alert color="blue" title="Authentication is enabled">
              Replace the placeholder token in each snippet with one of your proxy
              API keys.
            </Alert>
          )}

          <SnippetCard title="Claude Code" snippet={ov.claudeSnippet} />
          <SnippetCard title="Codex CLI" snippet={ov.codexSnippet} />
        </Stack>
      )}
    </Fade>
  )
}

function SnippetCard({ title, snippet }: { title: string; snippet: string }) {
  return (
    <div>
      <Group justify="space-between" mb={4}>
        <Title order={5} mb={0}>
          {title}
        </Title>
        <CopyButton value={snippet}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? 'Copied' : 'Copy'}>
              <UnstyledButton onClick={copy} aria-label={`Copy ${title} snippet`}>
                {copied ? (
                  <IconCheck size={15} color="#0ca30c" />
                ) : (
                  <IconCopy size={15} opacity={0.6} />
                )}
              </UnstyledButton>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
      <Text size="xs" c="dimmed" mb={2}>
        Point your client at the proxy:
      </Text>
      <pre
        style={{
          margin: 0,
          padding: '14px 16px',
          borderRadius: 8,
          overflowX: 'auto',
          fontSize: '0.82rem',
          lineHeight: 1.5,
          background: 'var(--mantine-color-dark-7)',
          color: '#e8eaed',
        }}
      >
        <code>{snippet}</code>
      </pre>
    </div>
  )
}
