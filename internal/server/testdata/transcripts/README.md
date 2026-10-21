# Recorded client transcripts

Real requests from Claude Code CLI 2.1.302, the Claude Agent SDK 0.2.170, and
Codex CLI 0.163.0, each paired with the ChatGPT Codex (`codex/gpt-6-luna`)
Responses stream that answered it. `transcript_test.go` replays every one
streaming and buffered, and translates each request onto all three wires.

Each directory holds `request.json` (client request, model rewritten to
`alias`), `upstream.sse` (recorded upstream stream), and `meta.json` (client,
endpoint, turn). All free text, paths, tool output, and identifiers were
replaced by `scripts/fixtures/anonymize_transcripts.py`; JSON structure,
protocol enums, built-in tool names, and schemas are kept. MCP tools were
removed.

To record more: run `scripts/fixtures/capture_forwarder.py` in front of the
proxy (prefix `cc_`) and between the proxy and its upstream (prefix `up_`),
drive a client through it, then run the anonymizer on the capture directory
and review the output before committing.
