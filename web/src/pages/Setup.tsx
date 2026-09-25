import { useState, type ReactNode } from 'react'
import {
  Alert,
  Anchor,
  Badge,
  Box,
  Button,
  Card,
  Code,
  Divider,
  Group,
  Loader,
  SimpleGrid,
  Stack,
  Switch,
  Tabs,
  Text,
} from '@mantine/core'
import { useClipboard } from '@mantine/hooks'
import {
  IconCheck,
  IconCopy,
  IconExclamationCircle,
  IconInfoCircle,
  IconTerminal2,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchOverview } from '../api'
import { Fade } from '../App'
import { PageHeader } from '../components/PageHeader'
import { PageSection } from '../components/PageSection'

export default function SetupPage() {
  const q = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const ov = q.data

  return (
    <Fade pending={q.isPending}>
      <Stack gap="lg" maw={960} miw={0}>
        <PageHeader title="Setup" subtitle="One connection for all your models. Get your coding agent up and running." />

        {q.isError && (
          <Alert
            color="gray"
            variant="light"
            icon={<IconInfoCircle size={16} />}
            title={ov ? 'Setup details could not be refreshed' : 'Setup details unavailable'}
          >
            <Stack gap="sm">
              <Text size="sm">
                {ov
                  ? 'Showing the last loaded configuration. Refresh before using these snippets if the proxy configuration has changed.'
                  : 'The proxy configuration could not be loaded. Check your connection and try again.'}
              </Text>
              <Button variant="default" size="sm" loading={q.isFetching} onClick={() => void q.refetch()} style={{ alignSelf: 'flex-start' }}>
                Try again
              </Button>
            </Stack>
          </Alert>
        )}

        {!ov && q.isPending && (
          <Group justify="center" py="xl" role="status">
            <Loader size="sm" aria-hidden="true" />
            <Text size="sm" c="dimmed">
              {q.fetchStatus === 'paused' ? 'Waiting for a connection to load setup…' : 'Loading setup details…'}
            </Text>
          </Group>
        )}

        {ov && (
          <>
            <PageSection title="1. Check your connection" description="Review the proxy configuration and any account sign-ins before launching your agent.">
              <Stack gap="sm" miw={0}>
                <Card withBorder radius="lg" p={0} miw={0}>
                  <StatusRow label="Listen address">
                    <Code style={{ overflowWrap: 'anywhere' }}>{ov.listen}</Code>
                  </StatusRow>
                  <Divider />
                  <StatusRow label="Proxy authentication">
                    <Badge color={ov.authEnabled ? 'teal' : 'gray'} variant="light" size="sm" tt="none">
                      {ov.authEnabled ? 'enabled (llx_… keys)' : 'disabled'}
                    </Badge>
                  </StatusRow>
                  {ov.exampleModel !== '<model>' && (
                    <>
                      <Divider />
                      <StatusRow label="Example model">
                        <Code style={{ overflowWrap: 'anywhere' }}>{ov.exampleModel}</Code>
                      </StatusRow>
                    </>
                  )}
                </Card>

                <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
                  <AccountConnectionCard
                    show={ov.backends.some((b) => b.name === 'grok')}
                    signedIn={ov.backends.find((b) => b.name === 'grok')?.authConfigured ?? false}
                    color="violet"
                    title="Grok uses your xAI account"
                    signInHref="/login"
                    signInLabel="Sign in with xAI"
                    body="Grok does not use an upstream API key."
                    tail=" to use your coding subscription."
                  />

                  <AccountConnectionCard
                    show={ov.backends.some((b) => b.name === 'workbuddy')}
                    signedIn={ov.backends.find((b) => b.name === 'workbuddy')?.authConfigured ?? false}
                    color="blue"
                    title="WorkBuddy uses your account"
                    signInHref="/login/workbuddy"
                    signInLabel="Sign in with WorkBuddy"
                    body="WorkBuddy does not use an upstream API key."
                    tail=" to connect your subscription."
                  />

                  <AccountConnectionCard
                    show={ov.backends.some((b) => b.name === 'codex')}
                    signedIn={ov.backends.find((b) => b.name === 'codex')?.authConfigured ?? false}
                    color="gray"
                    title="Codex uses your ChatGPT account"
                    signInHref="/login/codex"
                    signInLabel="Sign in with ChatGPT"
                    body="Codex does not use an upstream API key."
                    tail=" using a one-time device code."
                  />

                  <AccountConnectionCard
                    show={ov.backends.some((b) => b.name === 'zcode')}
                    signedIn={ov.backends.find((b) => b.name === 'zcode')?.authConfigured ?? false}
                    color="violet"
                    title="ZCode uses your account"
                    signInHref="/login/zcode"
                    signInLabel="Sign in with ZCode"
                    body="ZCode does not use an upstream API key."
                    tail=" to connect your Start Plan."
                  />
                </SimpleGrid>
              </Stack>
            </PageSection>

            <PageSection title="2. Configure your agent" description="Choose your CLI, copy its snippet, and review the placeholders before using it.">
              <Stack gap="sm" miw={0}>
                <Alert color="blue" variant="light" icon={<IconInfoCircle size={16} />} title={ov.authEnabled ? 'Use a proxy API key' : 'Proxy authentication is disabled'}>
                  {ov.authEnabled
                    ? 'For Claude Code, replace <key> with your proxy API key. For Codex CLI, set LLM_PROXY_API_KEY in your terminal environment before launching. Use a proxy key, not an upstream provider key.'
                    : 'No proxy key is required. For Claude Code, replace <key> with a non-empty placeholder such as unused. For Codex CLI, set LLM_PROXY_API_KEY to a non-empty placeholder so the client can start.'}
                </Alert>

                {ov.exampleModel === '<model>' && (
                  <Alert color="gray" variant="light" icon={<IconInfoCircle size={16} />} title="Choose a model before launching">
                    No example model is available. Replace <Code>{'<model>'}</Code> in the snippet with a configured model or route. Check the <Anchor href="/models">model catalog</Anchor> or your proxy configuration.
                  </Alert>
                )}

                <Card withBorder radius="lg" p={0} miw={0}>
                  <Tabs defaultValue="claude" keepMounted={false}>
                    <Tabs.List px="md" pt="sm" pb={4} aria-label="Coding agent">
                      <Tabs.Tab value="claude" leftSection={<IconTerminal2 size={14} />}>
                        Claude Code
                      </Tabs.Tab>
                      <Tabs.Tab value="codex" leftSection={<IconTerminal2 size={14} />}>
                        Codex CLI
                      </Tabs.Tab>
                    </Tabs.List>
                    <Tabs.Panel value="claude">
                      <Snippet title="Claude Code" description="Run this command in your terminal after replacing the placeholders." snippet={ov.claudeSnippet} />
                    </Tabs.Panel>
                    <Tabs.Panel value="codex">
                      <Snippet title="Codex CLI" description="Merge this provider section into ~/.codex/config.toml without replacing your other settings. Then run the launch command shown in the comment." snippet={ov.codexSnippet} />
                    </Tabs.Panel>
                  </Tabs>
                </Card>
              </Stack>
            </PageSection>
          </>
        )}
      </Stack>
    </Fade>
  )
}

// Account connections share a compact card with an explicit sign-in state.
function AccountConnectionCard({
  show,
  signedIn,
  color,
  title,
  body,
  signInHref,
  signInLabel,
  tail,
}: {
  show: boolean
  signedIn: boolean
  color: 'violet' | 'blue' | 'gray'
  title: string
  body: string
  signInHref: string
  signInLabel: string
  tail: string
}) {
  if (!show) return null
  return (
    <Card withBorder radius="lg" p="md">
      <Group justify="space-between" align="flex-start" gap="sm" mb="xs">
        <Text size="sm" fw={600}>{title}</Text>
        <Badge variant="dot" color={signedIn ? 'teal' : color} tt="none" size="sm">{signedIn ? 'Connected' : 'Sign-in needed'}</Badge>
      </Group>
      <Text size="xs" c="dimmed">{body}</Text>
      <Text size="sm" mt="sm"><Anchor href={signInHref}>{signInLabel}</Anchor>{tail}</Text>
    </Card>
  )
}

function StatusRow({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <Group justify="space-between" wrap="wrap" gap="xs" px="md" py="sm" miw={0}>
      <Text size="sm" c="dimmed">{label}</Text>
      <Box miw={0} maw="100%" style={{ overflowWrap: 'anywhere' }}>{children}</Box>
    </Group>
  )
}

function Snippet({ title, description, snippet }: { title: string; description: string; snippet: string }) {
  const clipboard = useClipboard({ timeout: 2000 })
  const [wrapLines, setWrapLines] = useState(false)
  const copied = clipboard.copied

  return (
    <Box miw={0}>
      <Stack gap="sm" p="md">
        <Text size="sm" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{description}</Text>
        <Group justify="space-between" gap="sm" wrap="wrap">
          <Switch
            size="sm"
            label="Wrap lines"
            checked={wrapLines}
            onChange={(event) => setWrapLines(event.currentTarget.checked)}
            aria-label={`Wrap ${title} snippet lines`}
          />
          <Button
            size="sm"
            variant={copied ? 'light' : 'default'}
            color={copied ? 'teal' : undefined}
            leftSection={copied ? <IconCheck size={15} /> : <IconCopy size={15} />}
            onClick={() => clipboard.copy(snippet)}
            aria-label={`Copy ${title} snippet`}
            disabled={snippet.length === 0}
          >
            {/* live region announces the idle→copied flip without moving focus */}
            <span aria-live="polite">{copied ? 'Copied' : 'Copy snippet'}</span>
          </Button>
        </Group>
        {clipboard.error && (
          <Group gap="xs" role="alert">
            <IconExclamationCircle size={16} style={{ flexShrink: 0 }} />
            <Text size="sm" c="yellow">
              Clipboard access is unavailable. Select the snippet below and copy it manually.
            </Text>
          </Group>
        )}
      </Stack>
      <Divider />
      <pre
        className="snippet-block"
        tabIndex={0}
        role="region"
        aria-label={`${title} setup snippet`}
        style={{ maxWidth: '100%', whiteSpace: wrapLines ? 'pre-wrap' : 'pre', overflowWrap: wrapLines ? 'anywhere' : 'normal' }}
      >
        <code>{snippet}</code>
      </pre>
    </Box>
  )
}
