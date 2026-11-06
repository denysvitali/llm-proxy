import { useMemo } from 'react'
import { Box, Button, Group, Switch, Text } from '@mantine/core'
import { useClipboard } from '@mantine/hooks'
import { motion } from 'framer-motion'
import { IconCheck, IconCopy, IconExclamationCircle } from '@tabler/icons-react'
import { springSnappy, useMotionPreset } from '../../motion/tokens'

type TokenType = 'comment' | 'string' | 'keyword' | 'flag' | 'variable' | 'number' | 'key' | 'section' | 'boolean' | 'plain'

interface Token {
  type: TokenType
  value: string
}

// The snippet pane is a terminal surface in BOTH schemes — a code pane that
// turns white in light mode stops reading as code. These hues are tuned for
// the near-black pane, so they do not come from the Mantine light ramp (which
// would leave strings and keys illegible here).
const tokenColors: Record<TokenType, string> = {
  comment: '#6f6f75',
  string: '#7ee2b8',
  keyword: '#8ab4ff',
  flag: '#8ab4ff',
  variable: '#ffb07c',
  number: '#f0c674',
  key: '#8ab4ff',
  section: '#8ab4ff',
  boolean: '#f0c674',
  plain: '#e6edf3',
}

const LANGUAGE_LABEL: Record<'shell' | 'toml', string> = { shell: 'shell', toml: 'toml' }

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
  const { reduced } = useMotionPreset()

  return (
    <Box miw={0} className="snippet-panel">
      <div className="snippet-intro">
        <Text size="sm" fw={650} className="snippet-title">{title} configuration</Text>
        <Text size="xs" c="dimmed" className="snippet-description">{description}</Text>
      </div>
      <div className="snippet-chrome">
        <div className="snippet-chrome-identity">
          <span className="snippet-dots" aria-hidden="true">
            <span /><span /><span />
          </span>
          <span className="snippet-lang">{LANGUAGE_LABEL[language]}</span>
        </div>
        <div className="snippet-chrome-actions">
          <Switch
            size="xs"
            label="Wrap lines"
            checked={wrapLines}
            onChange={(event) => onWrapLinesChange(event.currentTarget.checked)}
            aria-label={`Wrap ${title} snippet lines`}
            className="snippet-wrap-switch"
          />
          <Button
            size="xs"
            variant={copied ? 'light' : 'default'}
            className="snippet-copy press"
            leftSection={copied
              ? <motion.span
                  key="check"
                  className="snippet-copy-check"
                  initial={reduced ? false : { scale: 0.5, rotate: -18, opacity: 0 }}
                  animate={{ scale: 1, rotate: 0, opacity: 1 }}
                  transition={reduced ? { duration: 0 } : springSnappy}
                ><IconCheck size={14} /></motion.span>
              : <IconCopy size={14} />}
            onClick={() => clipboard.copy(snippet)}
            aria-label={`Copy ${title} snippet`}
            disabled={snippet.length === 0}
          >
            {/* live region announces the idle→copied flip without moving focus */}
            <span aria-live="polite">{copied ? 'Copied' : 'Copy snippet'}</span>
          </Button>
        </div>
      </div>
      <pre
        className="snippet-block"
        tabIndex={0}
        role="region"
        aria-label={`${title} setup snippet`}
        style={{ whiteSpace: wrapLines ? 'pre-wrap' : 'pre', overflowWrap: wrapLines ? 'anywhere' : 'normal' }}
      >
        <code>
          {tokens.map((token, i) => (
            <span key={i} style={{ color: tokenColors[token.type] }}>
              {token.value}
            </span>
          ))}
        </code>
      </pre>
      {clipboard.error && (
        <Group gap="xs" role="alert" className="snippet-error" wrap="nowrap" align="flex-start">
          <IconExclamationCircle size={16} style={{ flexShrink: 0 }} aria-hidden />
          <Text size="sm">
            Clipboard access is unavailable. Select the snippet below and copy it manually.
          </Text>
        </Group>
      )}
    </Box>
  )
}
