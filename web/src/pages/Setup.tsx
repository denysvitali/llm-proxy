import { useState } from 'react'
import {
  Alert, Anchor, Badge, Box, Button, Card, Code, Group,
  Skeleton, Stack, Tabs, Text, ThemeIcon, Title,
} from '@mantine/core'
import {
  IconArrowUpRight, IconCheck, IconHeartbeat, IconInfoCircle,
  IconPlugConnected, IconShieldLock, IconTerminal2,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { fetchOverview, type Overview } from '../api'
import { Fade } from '../App'
import { PageHeader } from '../components/PageHeader'
import { Snippet } from '../components/setup/Snippet'
import './setup.css'

const accountProviders = [
  { name: 'grok', label: 'Grok', account: 'xAI account', href: '/login' },
  { name: 'workbuddy', label: 'WorkBuddy', account: 'WorkBuddy account', href: '/login/workbuddy' },
  { name: 'codex', label: 'Codex', account: 'ChatGPT account', href: '/login/codex' },
  { name: 'zcode', label: 'ZCode', account: 'ZCode account', href: '/login/zcode' },
  { name: 'minimax-code', label: 'MiniMax Code', account: 'MiniMax account', href: '/login/minimax-code' },
]

export default function SetupPage() {
  const q = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const ov = q.data
  const [wrapLines, setWrapLines] = useState(true)
  const curlSnippet = ov
    ? `curl http://${ov.listen.replace('0.0.0.0', 'localhost')}/v1/messages \\\n  -H "Content-Type: application/json" \\\n  -H "x-api-key: <key>" \\\n  -d '${JSON.stringify({ model: ov.exampleModel, messages: [{ role: 'user', content: 'Hello' }] })}'`
    : ''

  return (
    <Fade pending={q.isPending}>
      <Stack gap="lg" miw={0}>
        <PageHeader title="Setup" subtitle="Connect your favorite tools to one model gateway." />
        {q.isError && (
          <Alert color="red" variant="light" icon={<IconInfoCircle size={16} />} title={ov ? 'Setup details could not be refreshed' : 'Setup details unavailable'}>
            <Stack gap="sm">
              <Text size="sm">{ov
                ? 'Showing the last loaded configuration. Refresh before using these snippets if the proxy configuration has changed.'
                : 'The proxy configuration could not be loaded. Check your connection and try again.'}</Text>
              <Button variant="default" size="sm" loading={q.isFetching} onClick={() => void q.refetch()} style={{ alignSelf: 'flex-start' }}>Try again</Button>
            </Stack>
          </Alert>
        )}
        {!ov && q.isPending && (
          <Stack role="status" aria-label="Loading setup details" gap="md">
            <Skeleton h={74} />
            <Skeleton h={330} />
          </Stack>
        )}
        {ov && (
          <>
            <Box component="ol" className="setup-steps" aria-label="Connection steps">
              <li><span>01</span><div><Text fw={600} size="sm">Choose a client</Text><Text size="xs" c="dimmed">Your tool, your workflow</Text></div></li>
              <li><span>02</span><div><Text fw={600} size="sm">Copy configuration</Text><Text size="xs" c="dimmed">Replace the key and model</Text></div></li>
              <li><span>03</span><div><Text fw={600} size="sm">Connect and go</Text><Text size="xs" c="dimmed">Launch from your terminal</Text></div></li>
            </Box>
            <div className="setup-layout">
              <Stack gap="lg" miw={0}>
                <Card className="setup-client-card" p={0}>
                  <Box p="lg">
                    <Group gap="sm" mb="xs">
                      <ThemeIcon variant="light" size={32}><IconTerminal2 size={18} /></ThemeIcon>
                      <Title order={2} size="h3">Configure your client</Title>
                    </Group>
                    <Text size="sm" c="dimmed">Choose your client, then copy the configuration below.</Text>
                  </Box>
                  <Tabs defaultValue="claude" keepMounted={false}>
                    <Tabs.List px="md" aria-label="Coding agent" className="setup-client-tabs">
                      <Tabs.Tab value="claude">Claude Code</Tabs.Tab>
                      <Tabs.Tab value="codex">Codex CLI</Tabs.Tab>
                      <Tabs.Tab value="curl">HTTP / curl</Tabs.Tab>
                    </Tabs.List>
                    <Tabs.Panel value="claude">
                      <Snippet title="Claude Code" description="Replace the placeholders, then run this command in your terminal." snippet={ov.claudeSnippet} language="shell" wrapLines={wrapLines} onWrapLinesChange={setWrapLines} />
                    </Tabs.Panel>
                    <Tabs.Panel value="codex">
                      <Snippet title="Codex CLI" description="Merge this provider section into ~/.codex/config.toml, preserving your other settings. Then run the launch command in the comment." snippet={ov.codexSnippet} language="toml" wrapLines={wrapLines} onWrapLinesChange={setWrapLines} />
                    </Tabs.Panel>
                    <Tabs.Panel value="curl">
                      <Snippet title="Other / curl" description="Replace the placeholders, then run from a terminal that can reach this address." snippet={curlSnippet} language="shell" wrapLines={wrapLines} onWrapLinesChange={setWrapLines} />
                    </Tabs.Panel>
                  </Tabs>
                  <Box className="setup-key-note" p="md">
                    <Group gap="xs" mb={6} wrap="nowrap"><IconShieldLock size={16} aria-hidden /><Text size="sm" fw={600}>{ov.authEnabled ? 'Use your proxy API key' : 'No proxy key required'}</Text></Group>
                    <Text size="xs" c="dimmed" lh={1.6}>{ov.authEnabled
                      ? 'Replace <key> with an llx_ proxy key. For Codex CLI, set LLM_PROXY_API_KEY in your terminal before launching. Manage keys with ./llm-proxy keys.'
                      : 'Use a non-empty placeholder such as unused for <key>. For Codex CLI, set LLM_PROXY_API_KEY to that placeholder before launching.'}</Text>
                  </Box>
                </Card>
                {ov.exampleModel === '<model>' && (
                  <Alert color="blue" variant="light" icon={<IconInfoCircle size={16} />} title="Choose a model before launching">
                    Replace <Code>{'<model>'}</Code> with a configured model or route from the <Anchor component={Link} to="/models">model catalog</Anchor>.
                  </Alert>
                )}
                <ConnectionCheck />
              </Stack>
              <Stack gap="md" miw={0}>
                <GatewayDetails overview={ov} />
                <AccountConnections overview={ov} />
              </Stack>
            </div>
          </>
        )}
      </Stack>
    </Fade>
  )
}

function GatewayDetails({ overview: ov }: { overview: Overview }) {
  return (
    <Card p="lg">
      <Group gap="xs" mb="md"><IconPlugConnected size={18} aria-hidden /><Title order={2} size="h3">Your gateway</Title></Group>
      <Stack gap="md">
        <div><Text size="xs" c="dimmed" mb={5}>Listen address</Text><Code className="setup-identifier">{ov.listen}</Code></div>
        <div><Text size="xs" c="dimmed" mb={5}>Authentication</Text><Badge variant="light" color={ov.authEnabled ? 'teal' : 'gray'} tt="none">{ov.authEnabled ? 'Proxy key required' : 'Disabled'}</Badge></div>
        <div><Text size="xs" c="dimmed" mb={5}>Example model</Text><Code className="setup-identifier">{ov.exampleModel}</Code></div>
        <Anchor component={Link} to="/models" size="sm" className="setup-inline-link">Browse models <IconArrowUpRight size={15} /></Anchor>
      </Stack>
    </Card>
  )
}

function AccountConnections({ overview }: { overview: Overview }) {
  const accounts = accountProviders.flatMap((provider) => {
    const backend = overview.backends.find((item) => item.name === provider.name && item.enabled)
    return backend ? [{ ...provider, signedIn: backend.authConfigured }] : []
  })
  if (accounts.length === 0) return null
  return (
    <Card p="lg">
      <Title order={2} size="h3" mb={5}>Account connections</Title>
      <Text size="xs" c="dimmed" mb="md">Subscriptions linked to this gateway.</Text>
      <Stack gap={0}>
        {accounts.map((provider) => (
          <div className="setup-account" key={provider.name}>
            <Group justify="space-between" gap="xs" wrap="nowrap">
              <Text size="sm" fw={600}>{provider.label}</Text>
              <Badge variant="dot" size="sm" color={provider.signedIn ? 'teal' : 'orange'} tt="none">{provider.signedIn ? 'Connected' : 'Sign-in needed'}</Badge>
            </Group>
            <Anchor href={provider.href} size="xs" className="setup-inline-link" mt={6} aria-label={`${provider.signedIn ? 'Manage' : 'Connect'} ${provider.account}`}>
              {provider.signedIn ? provider.account : `Connect ${provider.account}`} <IconArrowUpRight size={13} />
            </Anchor>
          </div>
        ))}
      </Stack>
    </Card>
  )
}

function ConnectionCheck() {
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [testing, setTesting] = useState(false)
  async function checkConnection() {
    setTesting(true)
    setResult(null)
    try {
      const response = await fetch('/healthz', { cache: 'no-store', signal: AbortSignal.timeout(10000) })
      setResult({ ok: response.ok, message: response.ok ? 'Gateway is reachable.' : `Gateway returned HTTP ${response.status}.` })
    } catch {
      setResult({ ok: false, message: 'Could not reach the gateway. Check your connection and try again.' })
    } finally {
      setTesting(false)
    }
  }
  return (
    <Card className="setup-connection-check" p="lg">
      <Group justify="space-between" gap="md">
        <Box style={{ flex: '1 1 15rem' }}>
          <Group gap="xs" mb={5}><IconHeartbeat size={18} aria-hidden /><Title order={2} size="h3">Ready to connect?</Title></Group>
          <Text size="sm" c="dimmed">Check gateway reachability from this browser. Then launch your client to verify its key and model.</Text>
        </Box>
        <Button variant="light" loading={testing} onClick={() => void checkConnection()} leftSection={<IconPlugConnected size={16} />}>Check connection</Button>
      </Group>
      <div role="status" aria-live="polite">
        {result && <Group gap="xs" mt="md" align="flex-start" wrap="nowrap"><Box c={result.ok ? 'teal' : 'red'}>{result.ok ? <IconCheck size={16} /> : <IconInfoCircle size={16} />}</Box><Text size="sm" c={result.ok ? 'teal' : 'red'}>{result.message}</Text></Group>}
      </div>
    </Card>
  )
}
