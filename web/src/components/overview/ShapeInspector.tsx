import { useMemo } from 'react'
import { Accordion, Alert, Badge, Group, Stack, Text } from '@mantine/core'
import type { DiffReport, ShapeBody, ShapeChange, ShapeDetail, ShapeNode } from '../../api'

// Structural preview of a recorded shape. Rendering is text-only: one line per
// key with its JSON type union, so a shape reads like a schema diff rather than
// a data dump — and there is no value to render in the first place.

interface ShapeLine {
  path: string
  depth: number
  label: string
  type: string
  // fromDiff marks the line as added/removed/retyped between the two sides.
  fromDiff?: ShapeChange['kind']
  cut?: boolean
}

// changeIndex maps a JSON Pointer path to that hop's change kind. It also
// promotes the summary counts to line-level marks, so a changed subtree's
// roots are visible even when the detail list hit its cap.
function changeIndex(changes: DiffReport | undefined): Map<string, ShapeChange['kind']> {
  const index = new Map<string, ShapeChange['kind']>()
  for (const change of changes?.changes ?? []) {
    index.set(change.path, change.kind)
    // Mark the parent of an added/removed subtree as changed too: the path
    // itself often names a location that does not exist on the other side.
    const parent = change.path.slice(0, change.path.lastIndexOf('/'))
    if (parent && !index.has(parent)) index.set(parent, change.kind)
  }
  return index
}

function typeLabel(node: ShapeNode | undefined): string {
  if (!node) return 'unknown'
  const types = node.types ?? []
  const joined = types.length ? types.join(' | ') : 'unknown'
  const suffix: string[] = []
  if (node.items) suffix.push('[]')
  if (node.keys && Object.keys(node.keys).length) suffix.push('{…}')
  return suffix.length ? `${joined} ${suffix.join(' ')}` : joined
}

// describeNode flattens a shape tree into indented lines. Keys are sorted so
// two sides of the same conversion line up visually.
function describeNode(node: ShapeNode | undefined, path: string, depth: number, out: ShapeLine[]): void {
  if (!node) return
  if (node.items) describeNode(node.items, `${path}/[]`, depth, out)
  const keys = node.keys ? Object.keys(node.keys).sort() : []
  for (const key of keys) {
    const child = node.keys![key]
    const childPath = `${path}/${key}`
    out.push({
      path: childPath,
      depth,
      label: key,
      type: typeLabel(child),
      cut: child.cut || undefined,
    })
    describeNode(child, childPath, depth + 1, out)
  }
}

function linesFor(side: ShapeNode | undefined, changes: DiffReport | undefined) {
  const lines: ShapeLine[] = []
  describeNode(side, '', 0, lines)
  const kinds = changeIndex(changes)
  for (const line of lines) line.fromDiff = kinds.get(line.path)
  return lines
}

// A body may be an SSE stream: report each event's own tree. Events are the
// derived names recorded for the stream (type/object discriminators).
function bodyLines(body: ShapeBody | undefined, changes: DiffReport | undefined) {
  if (!body) return []
  if (body.format !== 'sse') return linesFor(body.shape, changes)
  const lines: ShapeLine[] = []
  const kinds = changeIndex(changes)
  for (const name of body.order ?? Object.keys(body.events ?? {})) {
    const eventNode = body.events?.[name]
    lines.push({ path: `/events/${name}`, depth: 0, label: name, type: typeLabel(eventNode) })
    describeNode(eventNode, `/events/${name}`, 1, lines)
  }
  for (const line of lines) line.fromDiff = kinds.get(line.path)
  return lines
}

const changeColor: Record<ShapeChange['kind'], string> = {
  added: 'green',
  removed: 'red',
  retyped: 'yellow',
  format: 'grape',
}

function DiffBadges({ changes }: { changes: DiffReport }) {
  if (changes.identical) {
    return <Badge size="xs" variant="light" color="gray">identical structure</Badge>
  }
  return (
    <Group gap={6} wrap="wrap">
      {changes.added > 0 && <Badge size="xs" variant="light" color="green">+{changes.added} added</Badge>}
      {changes.removed > 0 && <Badge size="xs" variant="light" color="red">−{changes.removed} removed</Badge>}
      {changes.retyped > 0 && <Badge size="xs" variant="light" color="yellow">{changes.retyped} retyped</Badge>}
      {changes.cut && <Badge size="xs" variant="light" color="orange">change list truncated</Badge>}
    </Group>
  )
}

function ShapeLines({ lines }: { lines: ShapeLine[] }) {
  if (!lines.length) return <Text size="xs" c="dimmed">Nothing recorded.</Text>
  return (
    <Stack gap={1} className="shape-tree">
      {lines.map((line) => (
        <Group key={line.path} gap={6} wrap="nowrap" className="shape-row">
          <Text
            component="code"
            size="xs"
            className="shape-key"
            style={{ paddingInlineStart: line.depth * 12 }}
            c={line.fromDiff ? changeColor[line.fromDiff] : undefined}
            title={line.fromDiff ? `${line.fromDiff} at ${line.path}` : line.path}
          >
            {line.label}
          </Text>
          <Text size="xs" c="dimmed" className="shape-type">{line.type}{line.cut ? ' …' : ''}</Text>
        </Group>
      ))}
    </Stack>
  )
}

function Hop({
  title,
  fromLabel,
  toLabel,
  shows,
  body,
  changes,
}: {
  title: string
  fromLabel: string
  toLabel: string
  // shows names which body the tree displays: the hop produces one side and
  // compares it against the other, and naming it avoids ambiguity.
  shows: string
  body: ShapeBody | undefined
  changes?: DiffReport
}) {
  const lines = useMemo(() => bodyLines(body, changes), [body, changes])
  return (
    <Accordion.Item value={title}>
      <Accordion.Control>
        <Stack gap={4}>
          <Group gap={8} wrap="wrap">
            <Text size="sm" fw={600}>{title}</Text>
            <Text size="xs" c="dimmed" className="overview-ident">{fromLabel} → {toLabel}</Text>
          </Group>
          {changes ? <DiffBadges changes={changes} /> : null}
        </Stack>
      </Accordion.Control>
      <Accordion.Panel>
        <Stack gap={6}>
          <Text size="xs" c="dimmed">
            {changes ? `Showing the ${shows}; marks compare it against what came before.` : `Showing the ${shows}.`}
          </Text>
          <ShapeLines lines={lines} />
        </Stack>
      </Accordion.Panel>
    </Accordion.Item>
  )
}

// ShapeInspector renders all four sides of an attempt so the conversion is
// auditable end to end: client request, forwarded request, upstream response,
// relayed response. The two produced sides carry the per-hop diffs.
export function ShapeInspector({ detail }: { detail: ShapeDetail }) {
  const trace = detail.shapes
  if (!trace || (!trace.request_client && !trace.request_upstream && !trace.response_upstream && !trace.response_client)) {
    return <Alert color="gray" variant="light">No structure was recorded for this attempt.</Alert>
  }
  const wireLabels: Record<string, string> = {
    anthropic: 'Anthropic',
    'openai-chat': 'Chat',
    'openai-responses': 'Responses',
  }
  const clientWire = wireLabels[trace.client_kind ?? ''] ?? (trace.client_kind || detail.kind || 'client')
  const wireKind = wireLabels[trace.wire_kind ?? ''] ?? (trace.wire_kind || detail.kind || 'wire')
  return (
    <Accordion multiple defaultValue={['Request']} variant="separated" chevronPosition="left">
      <Hop
        title="Request"
        fromLabel={`client · ${clientWire}`}
        toLabel={`upstream · ${wireKind}`}
        shows="forwarded request body"
        body={{ format: 'json', shape: trace.request_upstream }}
        changes={trace.request_changes}
      />
      <Hop
        title="Client request"
        fromLabel={`client · ${clientWire}`}
        toLabel={`upstream · ${wireKind}`}
        shows="original client request body"
        body={{ format: 'json', shape: trace.request_client }}
      />
      <Hop
        title="Upstream response"
        fromLabel={`upstream · ${wireKind}`}
        toLabel={`client · ${clientWire}`}
        shows="body the upstream returned"
        body={trace.response_upstream}
      />
      <Hop
        title="Response"
        fromLabel={`upstream · ${wireKind}`}
        toLabel={`client · ${clientWire}`}
        shows="relayed response body"
        body={trace.response_client}
        changes={trace.response_changes}
      />
    </Accordion>
  )
}
