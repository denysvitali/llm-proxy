import { useMemo, useState, type ReactNode } from 'react'
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

// ─── Syntax highlighting ────────────────────────────────────────────────────

type TokenType = 'comment' | 'string' | 'keyword' | 'flag' | 'variable' | 'number' | 'key' | 'section' | 'boolean' | 'plain'

interface Token {
  type: TokenType
  value: string
}

const tokenColors: Record<TokenType, string> = {
  comment: 'var(--mantine-color-dimmed)',
  string: 'var(--mantine-color-teal-6)',
  keyword: 'var(--mantine-color-blue-6)',
  flag: 'var(--mantine-color-cyan-6)',
  variable: 'var(--mantine-color-orange-6)',
  number: 'var(--mantine-color-violet-6)',
  key: 'var(--mantine-color-blue-6)',
  section: 'var(--mantine-color-blue-7)',
  boolean: 'var(--mantine-color-violet-6)',
  plain: 'inherit',
}

function tokenize(code: string, language: 'shell' | 'toml'): Token[] {
  if (language === 'toml') return tokenizeToml(code)
  return tokenizeShell(code)
}

function tokenizeShell(code: string): Token[] {
  const tokens: Token[] = []
  const regex = /(#.*)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')|(--?[\w-]+)|(\$\{?\w+\}?)|(\b\d+\b)/g
  let pos = 0
  let match

  while ((match = regex.exec(code)) !== null) {
    if (match.index > pos) {
      tokens.push({ type: 'plain', value: code.slice(pos, match.index) })
    }
    const type = match[1] ? 'comment'
      : match[2] ? 'string'
      : match[3] ? 'flag'
      : match[4] ? 'variable'
      : 'number'
    tokens.push({ type, value: match[0] })
    pos = match.index + match[0].length
  }

  if (pos < code.length) {
    tokens.push({ type: 'plain', value: code.slice(pos) })
  }

  return tokens
}

function tokenizeToml(code: string): Token[] {
  const tokens: Token[] = []
  const lines = code.split('\n')

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trimStart()
    const indent = line.slice(0, line.length - trimmed.length)

    if (indent) tokens.push({ type: 'plain', value: indent })

    if (trimmed.startsWith('#')) {
      tokens.push({ type: 'comment', value: trimmed })
    } else if (trimmed.startsWith('[')) {
      tokens.push({ type: 'section', value: trimmed })
    } else {
      const keyMatch = trimmed.match(/^([\w.-]+)(\s*=\s*)(.*)$/)
      if (keyMatch) {
        tokens.push({ type: 'key', value: keyMatch[1] })
        tokens.push({ type: 'plain', value: keyMatch[2] })
        tokens.push(...tokenizeTomlValue(keyMatch[3]))
      } else {
        tokens.push({ type: 'plain', value: trimmed })
      }
    }

    if (i < lines.length - 1) {
      tokens.push({ type: 'plain', value: '\n' })
    }
  }

  return tokens
}

function tokenizeTomlValue(value: string): Token[] {
  const tokens: Token[] = []
  const regex = /("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')|(\btrue\b|\bfalse\b)|(\b\d+\.?\d*\b)/g
  let pos = 0
  let match

  while ((match = regex.exec(value)) !== null) {
    if (match.index > pos) {
      tokens.push({ type: 'plain', value: value.slice(pos, match.index) })
    }
    const type = match[1] ? 'string' : match[2] ? 'boolean' : 'number'
    tokens.push({ type, value: match[0] })
    pos = match.index + match[0].length
  }

  if (pos < value.length) {
    tokens.push({ type: 'plain', value: value.slice(pos) })
  }

  return tokens
}

// ─── Page ───────────────────────────────────────────────────────────────────

export default function SetupPage() {
  const q = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const ov = q.data
  const [wrapLines, setWrapLines] = useState(false)

  const curlSnippet = ov
    ? `curl http://${ov.listen.replace('0.0.0.0', 'localhost')}/v1/messages \\
  -H "Content-Type: application/json" \\
  -H "x-api-key: <key>" \\
  -d '{"model": "${ov.exampleModel !== '<model>' ? ov.exampleModel : '<model>'}", "messages": [{"role": "user", "content": "Hello"}]}'`
    : ''

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
                <Card withBorder radius="md" p={0} miw={0}>
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

                <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
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
                    ? 'For Claude Code, replace <key> with your proxy API key. For Codex CLI, set LLM_PROXY_API_KEY in your terminal environment before launching. Use a proxy key, not an upstream provider key. Create one with `./llm-proxy keys create-user <name>`.'
                    : 'No proxy key is required. For Claude Code, replace <key> with a non-empty placeholder such as unused. For Codex CLI, set LLM_PROXY_API_KEY to a non-empty placeholder so the client can start.'}
                </Alert>

                {ov.exampleModel === '<model>' && (
                  <Alert color="gray" variant="light" icon={<IconInfoCircle size={16} />} title="Choose a model before launching">
                    No example model is available. Replace <Code>{'<model>'}</Code> in the snippet with a configured model or route. Check the <Anchor href="/models">model catalog</Anchor> or your proxy configuration.
                  </Alert>
                )}

                <Card withBorder radius="md" p={0} miw={0}>
                  <Tabs defaultValue="claude" keepMounted={false}>
                    <Tabs.List px="md" pt="sm" pb={4} aria-label="Coding agent">
                      <Tabs.Tab value="claude" leftSection={<IconTerminal2 size={14} />}>
                        Claude Code
                      </Tabs.Tab>
                      <Tabs.Tab value="codex" leftSection={<IconTerminal2 size={14} />}>
                        Codex CLI
                      </Tabs.Tab>
                      <Tabs.Tab value="curl" leftSection={<IconTerminal2 size={14} />}>
                        Other / curl
                      </Tabs.Tab>
                    </Tabs.List>
                    <Tabs.Panel value="claude">
                      <Snippet title="Claude Code" description="Run this command in your terminal after replacing the placeholders." snippet={ov.claudeSnippet} language="shell" wrapLines={wrapLines} onWrapLinesChange={setWrapLines} />
                    </Tabs.Panel>
                    <Tabs.Panel value="codex">
                      <Snippet title="Codex CLI" description="Merge this provider section into ~/.codex/config.toml without replacing your other settings. Then run the launch command shown in the comment." snippet={ov.codexSnippet} language="toml" wrapLines={wrapLines} onWrapLinesChange={setWrapLines} />
                    </Tabs.Panel>
                    <Tabs.Panel value="curl">
                      <Snippet title="Other / curl" description="Use this generic curl command with any HTTP client. Replace the placeholders before running." snippet={curlSnippet} language="shell" wrapLines={wrapLines} onWrapLinesChange={setWrapLines} />
                    </Tabs.Panel>
                  </Tabs>
                </Card>
              </Stack>
            </PageSection>

            <TestItSection listen={ov.listen} exampleModel={ov.exampleModel} wrapLines={wrapLines} onWrapLinesChange={setWrapLines} />
          </>
        )}
      </Stack>
    </Fade>
  )
}

// ─── Test it section ────────────────────────────────────────────────────────

function TestItSection({
  listen,
  exampleModel,
  wrapLines,
  onWrapLinesChange,
}: {
  listen: string
  exampleModel: string
  wrapLines: boolean
  onWrapLinesChange: (value: boolean) => void
}) {
  const [testResult, setTestResult] = useState<{ status: number; body: string } | null>(null)
  const [testing, setTesting] = useState(false)

  const model = exampleModel !== '<model>' ? exampleModel : 'claude-sonnet-4-20250514'
  const host = listen.replace('0.0.0.0', 'localhost')
  const curlCommand = `curl http://${host}/v1/messages \\
  -H "Content-Type: application/json" \\
  -H "x-api-key: <key>" \\
  -d '{"model": "${model}", "messages": [{"role": "user", "content": "Hello"}]}'`

  async function runTest() {
    setTesting(true)
    setTestResult(null)
    try {
      const response = await fetch(`http://${host}/v1/messages`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'x-api-key': '<key>',
        },
        body: JSON.stringify({
          model,
          messages: [{ role: 'user', content: 'Hello' }],
        }),
      })
      const text = await response.text()
      setTestResult({ status: response.status, body: text.slice(0, 200) })
    } catch (error) {
      setTestResult({ status: 0, body: error instanceof Error ? error.message : String(error) })
    } finally {
      setTesting(false)
    }
  }

  return (
    <PageSection title="3. Test it" description="Verify your proxy is reachable and responding.">
      <Stack gap="sm" miw={0}>
        <Card withBorder radius="md" p={0} miw={0}>
          <Snippet title="Test curl" description="Run this command to test your proxy endpoint, or use the button below." snippet={curlCommand} language="shell" wrapLines={wrapLines} onWrapLinesChange={onWrapLinesChange} />
        </Card>
        <Group gap="sm" align="flex-start">
          <Button size="sm" loading={testing} onClick={runTest} leftSection={<IconTerminal2 size={15} />}>
            Run test
          </Button>
          {testResult && (
            <Text size="sm" c={testResult.status >= 200 && testResult.status < 300 ? 'teal' : 'red'} fw={600}>
              Status: {testResult.status}
            </Text>
          )}
        </Group>
        {testResult && testResult.body && (
          <Box miw={0}>
            <Text size="xs" c="dimmed" mb={4}>Response (first 200 chars):</Text>
            <pre
              className="snippet-block"
              style={{ maxWidth: '100%', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}
            >
              <code>{testResult.body}</code>
            </pre>
          </Box>
        )}
      </Stack>
    </PageSection>
  )
}

// ─── Account connections ────────────────────────────────────────────────────

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
    <Card withBorder radius="md" p="md">
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

// ─── Snippet ────────────────────────────────────────────────────────────────

function Snippet({
  title,
  description,
  snippet,
  language,
  wrapLines,
  onWrapLinesChange,
}: {
  title: string
  description: string
  snippet: string
  language: 'shell' | 'toml'
  wrapLines: boolean
  onWrapLinesChange: (value: boolean) => void
}) {
  const clipboard = useClipboard({ timeout: 2000 })
  const copied = clipboard.copied
  const tokens = useMemo(() => tokenize(snippet, language), [snippet, language])

  return (
    <Box miw={0}>
      <Stack gap="sm" p="md">
        <Text size="sm" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{description}</Text>
        <Group justify="space-between" gap="sm" wrap="wrap">
          <Switch
            size="sm"
            label="Wrap lines"
            checked={wrapLines}
            onChange={(event) => onWrapLinesChange(event.currentTarget.checked)}
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
        <code>
          {tokens.map((token, i) => (
            <span key={i} style={{ color: tokenColors[token.type] }}>
              {token.value}
            </span>
          ))}
        </code>
      </pre>
    </Box>
  )
}
