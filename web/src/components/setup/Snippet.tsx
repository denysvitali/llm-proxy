import { useMemo } from 'react'
import { Box, Button, Divider, Group, Stack, Switch, Text } from '@mantine/core'
import { useClipboard } from '@mantine/hooks'
import { IconCheck, IconCopy, IconExclamationCircle } from '@tabler/icons-react'

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

export function Snippet({
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
      <Stack gap="sm" p="lg">
        <Text size="sm" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{description}</Text>
        <Group justify="space-between" gap="sm" wrap="wrap" className="setup-snippet-toolbar">
          <Switch
            size="sm"
            label="Wrap lines"
            checked={wrapLines}
            onChange={(event) => onWrapLinesChange(event.currentTarget.checked)}
            aria-label={`Wrap ${title} snippet lines`}
          />
          <Button
            size="sm"
            variant={copied ? 'light' : 'filled'}
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
            <Text size="sm" style={{ color: 'var(--data-warning)' }}>
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
