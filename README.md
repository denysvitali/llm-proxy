# llm-proxy

`llm-proxy` is a self-hosted LLM gateway. Clients speak whichever API they
already know — the Anthropic Messages API, the OpenAI Chat Completions API, or
the OpenAI Responses API — and the proxy routes each request to one of the
configured backends, translating between API shapes when needed.

Access control is two-sided: end users authenticate to the proxy with their own
API keys (each user can hold any number of keys), while ordinary upstream
provider keys are configured once, per backend, server-side. Grok is different:
it uses an xAI account session and coding subscription signed in from the web
dashboard; Grok has no upstream API-key setting.

> [!IMPORTANT]
> Each backend is a separate service with its own terms of service. Check that
> routing your traffic through `llm-proxy` — including using subscription
> credentials from tools such as Claude Code or Codex — is permitted for your
> account and use case.

## Backends

| Backend   | Upstream                          | Native APIs                              | Notes                                                        |
| --------- | --------------------------------- | ---------------------------------------- | ------------------------------------------------------------ |
| `apodex`   | [Apodex](https://platform.apodex.ai/docs) | Anthropic Messages, Chat Completions, Responses API | Every shape is served natively, so nothing is translated. See [Apodex](#apodex) for the model tiers and their limits. |
| `opencode` | [OpenCode Zen](https://opencode.ai/docs/zen/) | Anthropic Messages, Chat Completions | Both request shapes pass through byte-for-byte.              |
| `grok`     | xAI Grok subscription             | Responses API                            | Anthropic and Chat Completions requests are translated server-side, so Claude Code and Codex work unchanged. |
| `nous`      | [Nous Portal](https://portal.nousresearch.com/) | Chat Completions (OpenAI-compatible) | Anthropic requests are translated server-side. Models use `vendor/model` slugs (e.g. `nousresearch/hermes-4-70b`). |
| `openrouter` | [OpenRouter](https://openrouter.ai/docs) | Chat Completions (OpenAI-compatible) | Anthropic and Responses requests are translated server-side. Models use `vendor/model` slugs. |
| `venice`    | [Venice AI](https://venice.ai/)   | Chat Completions (OpenAI-compatible)     | Anthropic and Responses requests are translated server-side. |

When an inbound request targets an API shape the routed backend does not speak
natively, the proxy translates the request and — streaming included — the
response.

### Apodex

[Apodex](https://platform.apodex.ai/docs) serves all three APIs the proxy
speaks — `/v1/messages`, `/v1/chat/completions`, and `/v1/responses` — so every
inbound shape passes through untranslated. Configure it like any other backend:

```yaml
backends:
  - type: apodex
    api_key_env: APODEX_API_KEY
```

The catalog comes from Apodex's `GET /v1/models`, so `apodex/<id>` works for
any model the account can reach. Two families are on offer:

| Family | Models | Notes |
| ------ | ------ | ----- |
| Core   | `apodex-1.1`, `apodex-1.1-mini` | Single-pass inference, 262k context, text-only. Reasoning is on by default and spends the `max_tokens` budget. |
| Deep research | `apodex-1-1-deep-research`, `apodex-1-1-deep-solve`, `apodex-1-1-deep-discover` | Agentic: tools run server-side on Apodex and the results come back inside the response. |

Two Apodex behaviours are worth knowing before pointing a client at it:

- **Tools.** The core models take no tool definitions, and the deep-research
  models run their own server-side tools rather than calling back to the
  client (they take `mcp_servers` instead of `tools`). Agents that depend on
  client-side tool execution — Claude Code and Codex both do — will not work
  against Apodex; it suits one-shot and chat-style traffic.
- **Streaming default.** Apodex defaults `stream` to `true` on the deep-research
  models, the opposite of OpenAI. The backend pins the field to whatever the
  inbound request asked for, so a body that omits `stream` still gets one JSON
  object back rather than an unexpected SSE stream.

Non-streaming requests cap `max_tokens` at 32768 and time out around 600
seconds upstream; stream anything longer.

### OpenRouter

[OpenRouter](https://openrouter.ai/docs) serves an OpenAI-compatible Chat
Completions API at `https://openrouter.ai/api/v1` and publishes its catalog at
`/models`. Configure it with the key from your OpenRouter account:

```yaml
backends:
  - type: openrouter
    api_key_env: OPENROUTER_API_KEY
```

The catalog includes every model visible to the key, so use a qualified route
such as `openrouter/vendor/model` when a bare ID would be ambiguous across
backends.

## Install

Requires Go (see `go.mod` for the minimum version).

```bash
go build -o llm-proxy ./cmd
```

A container image is published to `ghcr.io/denysvitali/llm-proxy` on every
push to `main` and for `v*` tags.

## Quickstart

1. Create `~/.config/llm-proxy/config.yaml`:

   ```yaml
   server:
     listen: 127.0.0.1:8090

   auth:
     file: ~/.config/llm-proxy/keys.json

   backends:
     - type: venice
       api_key_env: VENICE_API_KEY

   routes:
     claude-sonnet-4-6:
       backend: venice
       model: qwen3-235b

   default_route:
     backend: venice
   ```

2. Export the upstream key and start the server:

   ```bash
   export VENICE_API_KEY="sk-..."
   ./llm-proxy serve
   ```

   If the configuration includes the `grok` backend, open
   `http://127.0.0.1:8090/login` and choose **Sign in with xAI**. Approve the
   device authorization with the xAI account that owns the coding subscription;
   the session is stored in `~/.config/grok-proxy/auth.json` and refreshed by
   the proxy. Grok account sign-in is available from this web page only.

3. Create a user and mint an API key. The plaintext key starts with `llx_`,
   grants access to everything the proxy can reach, and is shown exactly once:

   ```bash
   ./llm-proxy keys create-user alice
   ./llm-proxy keys create alice --name laptop
   # llx_9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822c
   ```

   `llm-proxy keys list alice` shows existing keys (never their secrets);
   `llm-proxy keys set-state alice <key-id> --disable` revokes one.

### Claude Code

Point Claude Code at the proxy with the user key from the previous step:

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:8090 \
ANTHROPIC_AUTH_TOKEN=llx_9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822c \
claude
```

Requests hit `/v1/messages`; models are routed as described under
[Model routing](#model-routing). To pin one backend regardless of routes,
prefix the model with the backend name, e.g.
`claude --model grok/grok-4-0709` or `claude --model venice/qwen3-235b` —
the proxy translates Anthropic Messages to that backend's wire format
automatically (see [API translation](#api-translation)).

### Codex

Add a provider to `~/.codex/config.toml` that talks the Responses API against
the proxy:

```toml
model_provider = "llm-proxy"

[model_providers.llm-proxy]
name = "llm-proxy"
base_url = "http://127.0.0.1:8090/v1"
wire_api = "responses"
env_key = "LLM_PROXY_API_KEY"
```

Then run Codex with `LLM_PROXY_API_KEY` set to a `llx_...` key and pick any
served model — e.g. `codex -m "venice/qwen3-235b-a32b-fp8"`. Chat-only and
Anthropic-only backends work too: the proxy translates the Responses API onto
whatever the backend speaks (see [API translation](#api-translation)).

## API translation

Every endpoint accepts every backend: the proxy carries a full translation
matrix between the three supported client APIs and the three upstream wire
formats, so a single proxy hostname serves any client against any backend.

| Client speaks ↓ / Backend speaks → | Anthropic Messages | Chat Completions | Responses |
| --- | --- | --- | --- |
| **Anthropic Messages** (`/v1/messages`) | passthrough | translated | translated |
| **Chat Completions** (`/v1/chat/completions`) | translated | passthrough | translated |
| **Responses** (`/v1/responses`) | translated | translated | passthrough |

Translation covers requests, non-streaming responses, and streaming (SSE)
events in both directions — including tool definitions, tool calls/results,
thinking/reasoning content, images, token usage (with cache-hit details), and
stop/finish reasons. When a backend supports several wire formats, the most
natural one for the inbound API is used (native first, then OpenAI shapes
before Anthropic).

### Generic OpenAI SDKs

Any OpenAI-compatible client works against `/v1`:

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8090/v1",
    api_key="llx_9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822c",
)

resp = client.chat.completions.create(
    model="qwen3-235b",
    messages=[{"role": "user", "content": "Hello"}],
)
print(resp.choices[0].message.content)
```

The Responses API is available the same way via `client.responses.create(...)`.
Anthropic SDK clients can set `base_url="http://127.0.0.1:8090"` and use the
key as `api_key`.

## Model routing

Every inbound request carries a model name, which resolves in this order:

0. **Qualified IDs** — `<backend>/<model>` addresses one backend directly and
   beats every other rule. The prefix must name a registered, enabled backend;
   the remainder (split at the *first* slash) is sent upstream verbatim, so
   nested upstream names work: `nous/nousresearch/hermes-4-70b` routes to the
   `nous` backend with model `nousresearch/hermes-4-70b`. This lets a client
   be configured once with the proxy hostname and select per-request which
   backend serves it.
1. **Explicit routes** — an exact match in `routes`.
2. **Backend catalogs** — the enabled backends' live model lists, consulted in
   configuration order; first catalog containing the model wins. Catalogs are
   cached for about a minute, and dated snapshot suffixes (`-YYYYMMDD`) match
   their undated entries.
3. **Default route** — `default_route.backend`, with the model rewritten by
   `default_route.model` when set.

Unroutable models answer `404`; a qualified ID naming a disabled backend
answers `404` too rather than falling through to another backend.

`GET /v1/models` lists every enabled backend's catalog in the qualified
`<backend>/<id>` form, so clients can copy an ID straight into their
configuration.
`?backend=<name>` restricts the answer to one backend's catalog.

## Configuration

Configuration loads in this order: environment variables (`LLM_PROXY_`
prefix, dots become underscores), the config file (`LLM_PROXY_CONFIG`, or
`~/.config/llm-proxy/config.yaml` when present), then defaults. Command-line
flags are applied afterwards.

| Field                        | Environment variable                 | Default                  | Description                                                                 |
| ---------------------------- | ------------------------------------ | ------------------------ | --------------------------------------------------------------------------- |
| `base_url`                   | `LLM_PROXY_BASE_URL`                 | —                        | Public base URL of this proxy; used by the dashboard for client snippets.   |
| `server.listen`              | `LLM_PROXY_SERVER_LISTEN`            | `127.0.0.1:8090`         | HTTP listen address.                                                        |
| `server.max_body_bytes`      | `LLM_PROXY_SERVER_MAX_BODY_BYTES`    | `16777216`               | Maximum request body size (16 MiB).                                         |
| `auth.file`                  | `LLM_PROXY_AUTH_FILE`                | *(empty)*                | Path to the JSON key store. Empty disables client authentication — keep the listener on loopback if you do. |
| `grok_auth_file`             | `LLM_PROXY_GROK_AUTH_FILE`         | `~/.config/grok-proxy/auth.json` | xAI account session file used by the Grok subscription backend. This is not an API key. |
| `backends[].type`            | —                                    | required                 | Registered backend type (`apodex`, `venice`, `opencode`, `grok`, `nous`, `openrouter`); at most one backend per type. |
| `backends[].base_url`        | —                                    | per-provider default     | Override the upstream endpoint.                                             |
| `backends[].api_key_env`     | —                                    | —                        | Name of an environment variable holding an ordinary upstream key (not supported for `grok`). |
| `backends[].api_key`         | —                                    | —                        | Literal ordinary upstream key (not supported for `grok`).                   |
| `backends[].enabled`         | —                                    | `true`                   | Set `false` to take the backend out of routing without deleting it.         |
| `backends[].default_model`   | —                                    | —                        | Model used when a client model cannot be matched against this backend's catalog. |
| `routes.<model>.backend`     | —                                    | —                        | Backend serving this inbound model name.                                    |
| `routes.<model>.model`       | —                                    | inbound model name       | Upstream model name sent to the backend.                                    |
| `default_route.backend`      | —                                    | —                        | Backend for models that match nothing else.                                 |
| `default_route.model`        | —                                    | inbound model name       | Upstream model rewrite applied on the default route.                        |
| `log_level`                  | `LLM_PROXY_LOG_LEVEL`                | `info`                   | Log verbosity.                                                              |
| `log_format`                 | `LLM_PROXY_LOG_FORMAT`               | `text`                   | `text` or JSON logging.                                                     |

Per-backend fields have no flat environment-variable form; configure them in
the YAML file.

## Authentication

Users and keys live in the JSON file named by `auth.file`:

```json
[
  {
    "name": "alice",
    "keys": [
      {
        "id": "0d7f...",
        "name": "laptop",
        "hash": "salt-hex:digest-hex",
        "created_at": "2026-08-22T12:00:00Z",
        "last_used": "2026-08-22T13:37:00Z"
      }
    ]
  }
]
```

Keys are generated as `llx_` followed by 48 hex characters. Only a salted
SHA-256 hash is stored — `hex(16-byte-salt):hex(sha256(salt || key))` — and
verification is constant-time, so the plaintext is unrecoverable and is shown
only once at creation. The store is written atomically with `0600`
permissions. Keys can be disabled without deleting them (`disabled: true`),
and `last_used` records recent activity.

Clients present the key either as `Authorization: Bearer llx_...` or as
`x-api-key: llx_...`.

## Endpoints

| Method | Path                          | Purpose                                        |
| ------ | ----------------------------- | ---------------------------------------------- |
| POST   | `/v1/messages`                | Anthropic Messages API (any backend, via [translation](#api-translation)) |
| POST   | `/v1/messages/count_tokens`   | Anthropic token counting                       |
| POST   | `/v1/chat/completions`        | OpenAI Chat Completions API (any backend)      |
| POST   | `/v1/responses`               | OpenAI Responses API (any backend)             |
| GET    | `/v1/models`                  | Merged model catalog using `<backend>/<id>` IDs |
| GET    | `/`                           | Dashboard: status, routing, per-model stats, client setup |
| GET    | `/api/overview`               | JSON data used by the dashboard SPA |
| GET/POST | `/login`                    | Web-only xAI account sign-in for Grok |
| GET    | `/stats`                      | Per-model/backend JSON stats (uptime, latency percentiles, throughput, cache and tool-call rates) |
| GET    | `/healthz`                    | Liveness probe                                 |
| GET    | `/readyz`                     | Readiness probe (lists enabled backends)       |
| GET    | `/metrics`                    | Prometheus metrics                             |

### Model stats

Every upstream request is recorded per backend + model. `/stats` (and the
dashboard's *Model stats* table) summarizes it OpenRouter-style: uptime
(successful / total requests), TTFT and end-to-end latency p50/p90/p99,
output-token throughput p50/p90/p99, cache-hit rate, and tool-call error rate
(errored `tool_result`s seen in later requests over tool calls observed in
responses). Raw series for Prometheus/Grafana live under `/metrics`:
`llm_proxy_model_requests_total`, `llm_proxy_model_ttft_seconds`,
`llm_proxy_model_e2e_seconds`, `llm_proxy_model_tokens_total`,
`llm_proxy_model_output_tokens_per_second`, `llm_proxy_model_tool_calls_total`,
`llm_proxy_model_tool_errors_total`.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
test -z "$(gofmt -l .)"
golangci-lint run
```

CI runs all of the above (see `.github/workflows/`); the lint job builds
golangci-lint from source so it tracks the module's Go version.
