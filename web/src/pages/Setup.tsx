import type { ReactNode } from 'react'
import {
  Alert,
  Badge,
  Button,
  Card,
  Code,
  CopyButton,
  Group,
  Loader,
  Stack,
  Tabs,
  Text,
} from '@mantine/core'
import { IconCheck, IconCopy, IconInfoCircle, IconTerminal2 } from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { fetchOverview } from '../api'
import { Fade } from '../App'
import { PageHeader } from '../components/PageHeader'

export default function SetupPage() {
  const q = useQuery({ queryKey: ['overview'], queryFn: fetchOverview })
  const ov = q.data

  return (
    <Fade pending={q.isPending}>
      {!ov ? (
        <Group justify="center" py="xl">
          <Loader size="sm" />
        </Group>
      ) : (
        <Stack gap="lg" maw={820}>
          <PageHeader title="Setup" subtitle="Point a coding agent at this proxy" />

          <Card withBorder radius="lg" p={0}>
            <StatusRow label="Listen">
              <Code>{ov.listen}</Code>
            </StatusRow>
            <StatusRow label="Auth" last={ov.exampleModel === '<model>'}>
              <Badge
                color={ov.authEnabled ? 'teal' : 'gray'}
                variant="light"
                size="sm"
                tt="none"
              >
                {ov.authEnabled ? 'enabled (llx_… keys)' : 'disabled'}
              </Badge>
            </StatusRow>
            {ov.exampleModel !== '<model>' && (
              <StatusRow label="Example model" last>
                <Code>{ov.exampleModel}</Code>
              </StatusRow>
            )}
          </Card>

          <Stack gap="sm">
            {ov.backends.some((b) => b.name === 'grok') && (
              <Alert color="violet" variant="light" title="Grok uses your xAI account">
                Grok does not use an upstream API key.{' '}
                <a href="/login">Sign in with xAI</a> to use your coding subscription.
              </Alert>
            )}

            {ov.backends.some((b) => b.name === 'workbuddy') && (
              <Alert color="blue" variant="light" title="WorkBuddy uses your account">
                WorkBuddy does not use an upstream API key.{' '}
                <a href="/login/workbuddy">Sign in with WorkBuddy</a> to connect your subscription.
              </Alert>
            )}

            {ov.backends.some((b) => b.name === 'codex') && (
              <Alert color="gray" variant="light" title="Codex uses your ChatGPT account">
                Codex does not use an upstream API key.{' '}
                <a href="/login/codex">Sign in with ChatGPT</a> using a one-time device code.
              </Alert>
            )}

            {ov.backends.some((b) => b.name === 'zcode') && (
              <Alert color="violet" variant="light" title="ZCode uses your account">
                ZCode does not use an upstream API key.{' '}
                <a href="/login/zcode">Sign in with ZCode</a> to connect your Start Plan.
              </Alert>
            )}
          </Stack>

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
              <Tabs.List px="md" pt="sm" pb={4}>
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

function StatusRow({
  label,
  last,
  children,
}: {
  label: string
  last?: boolean
  children: ReactNode
}) {
  return (
    <Group
      justify="space-between"
      wrap="nowrap"
      gap="md"
      px="md"
      py="sm"
      style={
        last
          ? undefined
          : { borderBottom: '0.5px solid var(--mantine-color-default-border)' }
      }
    >
      <Text size="sm">{label}</Text>
      {children}
    </Group>
  )
}

function Snippet({ title, snippet }: { title: string; snippet: string }) {
  return (
    <div>
      <Group justify="space-between" px="md" py="xs">
        <Text fz={11} tt="uppercase" fw={600} c="dimmed" style={{ letterSpacing: '0.06em' }}>
          Install
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
