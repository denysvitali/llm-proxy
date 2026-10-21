#!/usr/bin/env python3
"""Turn captured client/upstream exchanges into anonymized test transcripts.

Capture layout (written by a logging forwarder on each hop):
  <capture>/cc_NNN.req.json  client request as sent to the proxy
  <capture>/cc_NNN.meta      first line: the client request path
  <capture>/up_NNN.req.json  translated request as sent upstream (unused)
  <capture>/up_NNN.resp      upstream response body (SSE or JSON)

Client and upstream POSTs pair up in order. Each pair becomes
<out>/<name>/{request.json,upstream.sse,meta.json}.

Anonymization is allowlist-based: JSON structure, keys, numbers, booleans,
and a small set of protocol enum fields are kept; every other string becomes
deterministic dummy text, so prompts, paths, tool output, hook context, and
account identifiers never survive. IDs keep their prefix but get a hashed
suffix, consistently across files so tool-call references still line up.
MCP tools (environment-specific) are removed from tool lists. SSE deltas are
rebuilt from the anonymized full value, so deltas still concatenate to the
matching done/completed value.

Usage: anonymize_transcripts.py <capture-dir> <out-dir> name=source:INDEXES ...
  e.g. claude-code-cli-simple="claude-code 2.1.302":0
"""

import hashlib
import json
import re
import sys
from pathlib import Path

WORDS = ("lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod "
         "tempor incididunt ut labore et dolore magna aliqua enim ad minim veniam "
         "quis nostrud exercitation ullamco laboris nisi aliquip ex ea commodo").split()
MAX_DUMMY = 120

# String values kept verbatim: protocol enums, never free text.
KEEP_KEYS = {
    "type", "role", "status", "phase", "stop_reason", "finish_reason", "object",
    "effort", "service_tier", "truncation", "verbosity", "tool_choice", "event",
    "media_type", "ttl", "keep", "display", "mode", "context", "code", "detail",
    "prompt_cache_retention", "include", "syntax", "$schema", "format", "model",
    "summary", "reasoning_effort", "search_context_size",
}
ID_KEYS = {"id", "call_id", "tool_use_id", "item_id", "response_id", "previous_response_id"}
ID_LIKE = re.compile(r"^([A-Za-z]{2,6}_)((?=[A-Za-z-]*[0-9])[A-Za-z0-9-]{8,})$")
SCHEMA_KEYS = {"input_schema", "parameters", "schema"}


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


def dummy(text):
    if text == "":
        return ""
    seed = digest(text)
    target = min(len(text), MAX_DUMMY)
    out, i = [], 0
    while len(" ".join(out)) < target:
        out.append(WORDS[int(seed[(i * 2) % 64:(i * 2) % 64 + 2], 16) % len(WORDS)])
        i += 1
    return " ".join(out)[:target].rstrip() or WORDS[0]


def anon_id(value):
    match = ID_LIKE.match(value)
    if not match:
        return "id_" + digest(value)[:24]
    prefix, rest = match.groups()
    return prefix + digest(value)[:min(len(rest), 48)]


def anon_args(raw):
    """Anonymize a JSON-encoded tool argument string, keeping it valid JSON."""
    try:
        parsed = json.loads(raw)
    except (ValueError, TypeError):
        return dummy(raw)
    return json.dumps(walk(parsed, free=True), separators=(",", ":"), ensure_ascii=False)


def tool_name(name):
    return "mcp__example__tool_" + digest(name)[:6] if name.startswith("mcp__") else name


def walk(value, key=None, schema=False, free=False):
    """free marks tool input values: every string there is user data."""
    if isinstance(value, dict):
        out = {}
        for k, v in value.items():
            nk = anon_id(k) if ID_LIKE.match(k) and not schema else k
            if k == "tools" and isinstance(v, list) and not free:
                v = [t for t in v if not str(t.get("name", "")).startswith("mcp__")]
            out[nk] = walk(v, k, schema or k in SCHEMA_KEYS, free or (k == "input" and isinstance(v, dict)))
        return out
    if isinstance(value, list):
        if schema and key == "required":
            return list(value)
        if schema and key == "enum":
            return [f"option_{i}" if isinstance(v, str) else v for i, v in enumerate(value)]
        return [walk(v, key, schema, free) for v in value]
    if not isinstance(value, str):
        return value
    if free:
        return dummy(value)
    if schema and key in ("type", "$schema", "format", "$ref"):
        return value
    if key in KEEP_KEYS:
        return value
    if key in ID_KEYS:
        return anon_id(value)
    if key == "name":
        return tool_name(value)
    if key == "arguments":
        return anon_args(value)
    if key in ("encrypted_content", "signature", "data", "obfuscation"):
        return "x" + digest(value)[:31]
    return dummy(value)


def split_like(text, pieces):
    """Split text into len(pieces) chunks proportional to the original sizes."""
    total = sum(len(p) for p in pieces) or 1
    out, pos, acc = [], 0, 0
    for i, piece in enumerate(pieces):
        acc += len(piece)
        end = len(text) if i == len(pieces) - 1 else round(len(text) * acc / total)
        out.append(text[pos:end])
        pos = end
    return out


def parse_sse(raw):
    events = []
    for block in raw.replace("\r\n", "\n").split("\n\n"):
        if not block.strip():
            continue
        name, data = None, []
        for line in block.split("\n"):
            if line.startswith("event:"):
                name = line[6:].strip()
            elif line.startswith("data:"):
                data.append(line[5:].lstrip())
        events.append((name, "\n".join(data)))
    return events


def anon_sse(raw):
    events = parse_sse(raw)
    decoded = []
    for name, data in events:
        try:
            decoded.append((name, json.loads(data)))
        except ValueError:
            decoded.append((name, data))
    # Group deltas by the value they build so they can be rebuilt from the
    # anonymized whole.
    groups = {}
    for i, (_, ev) in enumerate(decoded):
        if isinstance(ev, dict) and isinstance(ev.get("delta"), str):
            kind = "args" if "arguments" in ev.get("type", "") else "text"
            key = (kind, ev.get("item_id"), ev.get("output_index"), ev.get("content_index"), ev.get("summary_index"))
            groups.setdefault(key, []).append(i)
    rebuilt = {}
    for (kind, *_), idxs in groups.items():
        pieces = [decoded[i][1]["delta"] for i in idxs]
        full = "".join(pieces)
        whole = anon_args(full) if kind == "args" else dummy(full)
        for i, part in zip(idxs, split_like(whole, pieces)):
            rebuilt[i] = part
    out = []
    for i, (name, ev) in enumerate(decoded):
        if isinstance(ev, dict):
            ev = walk(ev)
            if i in rebuilt:
                ev["delta"] = rebuilt[i]
            data = json.dumps(ev, separators=(",", ":"), ensure_ascii=False)
        else:
            data = ev
        out.append((f"event: {name}\n" if name else "") + f"data: {data}\n\n")
    return "".join(out)


def main():
    capture, out_dir = Path(sys.argv[1]), Path(sys.argv[2])
    clients = sorted(capture.glob("cc_*.req.json"))
    upstreams = sorted(capture.glob("up_*.resp"))
    if len(clients) != len(upstreams):
        sys.exit(f"unpaired capture: {len(clients)} client vs {len(upstreams)} upstream")
    for spec in sys.argv[3:]:
        name, rest = spec.split("=", 1)
        source, indexes = rest.rsplit(":", 1)
        for n, index in enumerate(int(i) for i in indexes.split(",")):
            client, upstream = clients[index], upstreams[index]
            path = client.with_name(client.name.replace(".req.json", ".meta")).read_text().split("\n")[0]
            path = path.split("?")[0]
            request = walk(json.loads(client.read_bytes()))
            request["model"] = "alias"
            raw = upstream.read_text()
            target = out_dir / f"{name}-{n + 1}"
            target.mkdir(parents=True, exist_ok=True)
            (target / "request.json").write_text(json.dumps(request, indent=1, ensure_ascii=False) + "\n")
            (target / "upstream.sse").write_text(anon_sse(raw))
            (target / "meta.json").write_text(json.dumps({
                "source": source,
                "client_path": path,
                "upstream_wire": "openai-responses",
                "turn": n + 1,
            }, indent=1) + "\n")
            print(target, (target / "request.json").stat().st_size, (target / "upstream.sse").stat().st_size)


if __name__ == "__main__":
    main()
