import { useState } from 'react'
import {
  Alert, Anchor, Badge, Box, Button, Card, Code, Group,
  Skeleton, Stack, Tabs, Text, Title,
} from '@mantine/core'
import {
  IconArrowUpRight, IconCheck, IconHeartbeat, IconInfoCircle,
  IconPlugConnected, IconShieldLock, IconTerminal2,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import type { Overview } from '../api'
import { dashboardQueries } from '../queries'
import { Fade } from '../App'
import { PageHeader } from '../components/PageHeader'
import { ACCOUNT_AUTH } from '../lib/accounts'
import { Snippet } from '../components/setup/Snippet'
import { Reveal } from '../components/setup/Reveal'
import './setup.css'

const accountProviders = Object.entries(ACCOUNT_AUTH).map(([name, { label, account, login }]) => ({
  name, label, account, href: login,
}))

const STEPS: { href: string; figure: string; title: string; caption: string }[] = [
  { href: '#setup-client', figure: '01', title: 'Choose a client', caption: 'Copy a configuration' },
  { href: '#setup-auth', figure: '02', title: 'Set authentication', caption: 'Use your gateway key' },
  { href: '#setup-verify', figure: '03', title: 'Verify connection', caption: 'Check your first request' },
]

export default function SetupPage() {
  const q = useQuery(dashboardQueries.overview())
  const ov = q.data
  const [wrapLines, setWrapLines] = useState(true)
  const endpoint = `${window.location.origin}/v1/messages`
  const curlSnippet = ov
    ? `curl ${endpoint} \\\n  -H "Content-Type: application/json" \\\n  -H "x-api-key: <key>" \\\n  -d '${JSON.stringify({ model: ov.exampleModel, max_tokens: 128, messages: [{ role: 'user', content: 'Hello' }] })}'`
    : ''

  return (
    <Fade pending={q.isPending}>
      <Stack gap="lg" miw={0} className="setup-page">
        <PageHeader
          title="Setup"
          eyebrow="Gateway onboarding"
          subtitle="Connect a coding client to your gateway in three steps."
        />
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
            <Skeleton h={72} radius="lg" />
            <div className="setup-workspace-skeleton">
              <Skeleton h={330} radius="lg" />
              <Skeleton h={200} radius="lg" />
            </div>
          </Stack>
        )}
        {ov && (
          <>
            <StepSequence />
            <div className="setup-layout">
              <Stack gap="md" miw={0} className="setup-main">
                <Reveal>
                  <Card className="setup-card" id="setup-client" p={0}>
                    <Box className="setup-card-head">
                      <span className="setup-step-number" aria-hidden="true">1</span>
                      <Box miw={0}>
                        <span className="setup-step-label">Step 01 · Client</span>
                        <Title order={2} size="h2" className="setup-card-title">Choose your client</Title>
                        <Text size="xs" c="dimmed" mt={3}>Each tab has a ready-to-copy setup for that client.</Text>
                      </Box>
                    </Box>
                    <Tabs defaultValue="claude" keepMounted={false}>
                      <Tabs.List aria-label="Coding agent" className="setup-client-tabs">
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
                  </Card>
                </Reveal>

                <Reveal delay={0.04}>
                  <Card component="section" className="setup-card setup-auth-card" id="setup-auth" p={0} aria-labelledby="setup-auth-heading">
                    <Box className="setup-card-head">
                      <span className="setup-step-number" aria-hidden="true">2</span>
                      <Box miw={0}>
                        <span className="setup-step-label">Step 02 · Authentication</span>
                        <Title order={2} size="h2" id="setup-auth-heading" className="setup-card-title">Use the correct key value</Title>
                      </Box>
                    </Box>
                    <Box className="setup-auth-panel">
                      <Group gap="xs" wrap="nowrap" align="flex-start" mb={4}>
                        <IconShieldLock size={16} aria-hidden className="setup-auth-icon" />
                        <Text size="sm" fw={650} className="setup-auth-lead">
                          {ov.authEnabled ? 'Use your proxy API key' : 'No proxy key required'}
                        </Text>
                      </Group>
                      <Text size="xs" c="dimmed" lh={1.6} className="setup-auth-copy">{ov.authEnabled
                        ? 'Replace <key> with an llx_ proxy key. For Codex CLI, set LLM_PROXY_API_KEY in your terminal before launching. Manage keys with ./llm-proxy keys.'
                        : 'Use a non-empty placeholder such as unused for <key>. For Codex CLI, set LLM_PROXY_API_KEY to that placeholder before launching.'}</Text>
                    </Box>
                  </Card>
                </Reveal>

                {ov.exampleModel === '<model>' && (
                  <Alert color="blue" variant="light" icon={<IconInfoCircle size={16} />} title="Choose a model before launching">
                    Replace <Code>{'<model>'}</Code> with a configured model or route from the <Anchor component={Link} to="/models">model catalog</Anchor>.
                  </Alert>
                )}

                <Reveal delay={0.08}>
                  <ConnectionCheck />
                </Reveal>
              </Stack>

              <aside className="setup-rail" aria-label="Gateway reference">
                <Reveal delay={0.04}>
                  <GatewayDetails overview={ov} endpoint={endpoint} />
                </Reveal>
                <Reveal delay={0.08}>
                  <AccountConnections overview={ov} />
                </Reveal>
              </aside>
            </div>
          </>
        )}
      </Stack>
    </Fade>
  )
}

// A progress indicator, not a tab bar: mono figures in numbered nodes, joined
// by a hairline rule, each linking to the step it describes. Collapses to a
// compact three-up strip below 48em so it stays out of the way on a phone.
function StepSequence() {
  return (
    <nav className="setup-sequence" aria-label="Connection steps">
      {STEPS.map((step) => (
        <a href={step.href} key={step.href}>
          <span className="setup-seq-node" aria-hidden="true">{step.figure}</span>
          <div className="setup-seq-copy">
            <strong>{step.title}</strong>
            <small>{step.caption}</small>
          </div>
        </a>
      ))}
    </nav>
  )
}

function GatewayDetails({ overview: ov, endpoint }: { overview: Overview; endpoint: string }) {
  return (
    <Card p={0} className="setup-card">
      <Box className="setup-rail-head">
        <Group gap={7} wrap="nowrap" align="center">
          <IconTerminal2 size={15} aria-hidden />
          <span className="setup-rail-label">Gateway details</span>
        </Group>
      </Box>
      <Box className="setup-rail-body">
        <div className="setup-endpoint-block">
          <Text size="xs" className="setup-field-label">Messages endpoint</Text>
          <Code className="setup-identifier setup-endpoint">{endpoint}</Code>
        </div>
        <Stack gap={0} className="setup-fact-list">
          <div className="setup-context-row">
            <Text size="xs" c="dimmed">Authentication</Text>
            <Badge
              variant="light"
              color={ov.authEnabled ? 'zinc' : 'gray'}
              tt="none"
              className="setup-fact-badge"
              data-state={ov.authEnabled ? 'on' : 'off'}
            >
              {ov.authEnabled ? 'Proxy key required' : 'Disabled'}
            </Badge>
          </div>
          <div className="setup-context-row setup-model-row">
            <Text size="xs" c="dimmed">Example model</Text>
            <Code className="setup-identifier setup-model-value">{ov.exampleModel}</Code>
          </div>
        </Stack>
        <Anchor component={Link} to="/models" size="sm" className="setup-inline-link">
          Browse model catalog <IconArrowUpRight size={14} aria-hidden />
        </Anchor>
      </Box>
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
    <Card p={0} className="setup-card">
      <Box className="setup-rail-head">
        <Group gap={7} wrap="nowrap" align="center">
          <IconShieldLock size={15} aria-hidden />
          <span className="setup-rail-label">Account connections</span>
        </Group>
      </Box>
      <Box className="setup-rail-body">
        <Text size="xs" c="dimmed" mb={8}>Account-backed providers enabled on this gateway.</Text>
        <Stack gap={0}>
          {accounts.map((provider) => (
            <div className="setup-account" key={provider.name}>
              <Group justify="space-between" gap="xs" wrap="wrap">
                <Text size="sm" fw={600}>{provider.label}</Text>
                <span className="setup-conn-state" data-connected={provider.signedIn}>
                  <span className="setup-conn-dot" aria-hidden />
                  {provider.signedIn ? 'Connected' : 'Sign-in needed'}
                </span>
              </Group>
              <Anchor href={provider.href} size="xs" className="setup-inline-link" aria-label={`${provider.signedIn ? 'Manage' : 'Connect'} ${provider.account}`}>
                {provider.signedIn ? provider.account : `Connect ${provider.account}`} <IconArrowUpRight size={13} aria-hidden />
              </Anchor>
            </div>
          ))}
        </Stack>
      </Box>
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
    <Card component="section" className="setup-card" id="setup-verify" p={0} aria-labelledby="setup-verify-heading">
      <Box className="setup-card-head">
        <span className="setup-step-number" aria-hidden="true">3</span>
        <Box miw={0}>
          <span className="setup-step-label">Step 03 · Verify</span>
          <Title order={2} size="h2" id="setup-verify-heading" className="setup-card-title">Check your connection</Title>
        </Box>
      </Box>
      <Box className="setup-check-body">
        <div className="setup-check-row">
          <Box miw={0} className="setup-check-copy">
            <Group gap="xs" mb={4} wrap="nowrap">
              <IconHeartbeat size={17} aria-hidden className="setup-check-glyph" />
              <Text size="sm" fw={650}>Browser reachability</Text>
            </Group>
            <Text size="sm" c="dimmed" lh={1.55}>This checks that the gateway responds from this browser. Launch your client to verify its key and model.</Text>
          </Box>
          <Button
            variant="default"
            className="setup-check-button press"
            loading={testing}
            onClick={() => void checkConnection()}
            leftSection={<IconPlugConnected size={15} aria-hidden />}
          >
            Check connection
          </Button>
        </div>
        <div role="status" aria-live="polite" className="setup-check-result-slot">
          {result && (
            <div className="setup-check-result" data-ok={result.ok}>
              <span className="setup-check-result-icon" aria-hidden>
                {result.ok ? <IconCheck size={15} /> : <IconInfoCircle size={15} />}
              </span>
              <Text size="sm" component="div">{result.message}</Text>
            </div>
          )}
        </div>
      </Box>
    </Card>
  )
}
