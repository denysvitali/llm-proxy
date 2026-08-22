# llm-proxy

`llm-proxy` is a self-hosted LLM gateway. Clients speak whichever API they
already know — the Anthropic Messages API, the OpenAI Chat Completions API, or
the OpenAI Responses API — and the proxy routes each request to one of the
configured backends, translating between API shapes when needed.

Access control is two-sided: end users authenticate to the proxy with their own
API keys (each user can hold any number of keys), while the upstream provider
keys are configured once, per backend, server-side. Users never see upstream
credentials.

> [!IMPORTANT]
> Each backend is a separate service with its own terms of service. Check that
> routing your traffic through `llm-proxy` — including using subscription
> credentials from tools such as Claude Code or Codex — is permitted for your
> account and use case.

## Backends

| Backend   | Upstream                          | Native APIs                              | Notes                                                        |
| --------- | --------------------------------- | ---------------------------------------- | ------------------------------------------------------------ |
| `opencode` | [OpenCode Zen](https://opencode.ai/docs/zen/) | Anthropic Messages, Chat Completions | Both request shapes pass through byte-for-byte.              |
| `grok`     | xAI Grok subscription             | Responses API                            | Anthropic and Chat Completions requests are translated server-side, so Claude Code and Codex work unchanged. |
| `venice`    | [Venice AI](https://venice.ai/)   | Chat Completions (OpenAI-compatible)     | Anthropic and Responses requests are translated server-side. |

When an inbound request targets an API shape the routed backend does not speak
natively, the proxy translates the request and — streaming included — the
response.

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
[Model routing](#model-routing).

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

Then run Codex with `LLM_PROXY_API_KEY` set to a `llx_...` key.

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

1. **Explicit routes** — an exact match in `routes`.
2. **Backend catalogs** — the enabled backends' live model lists, consulted in
   configuration order; first catalog containing the model wins. Catalogs are
   cached for about a minute, and dated snapshot suffixes (`-YYYYMMDD`) match
   their undated entries.
3. **Default route** — `default_route.backend`, with the model rewritten by
   `default_route.model` when set.

Unroutable models answer `404`.

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
| `backends[].type`            | —                                    | required                 | `venice`, `opencode`, or `grok`; at most one backend per type.              |
| `backends[].base_url`        | —                                    | per-provider default     | Override the upstream endpoint.                                             |
| `backends[].api_key_env`     | —                                    | —                        | Name of an environment variable holding the upstream key (recommended).     |
| `backends[].api_key`         | —                                    | —                        | Literal upstream key; only used when `api_key_env` is unset or empty.       |
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
| POST   | `/v1/messages`                | Anthropic Messages API                         |
| POST   | `/v1/messages/count_tokens`   | Anthropic token counting                       |
| POST   | `/v1/chat/completions`        | OpenAI Chat Completions API                    |
| POST   | `/v1/responses`               | OpenAI Responses API                           |
| GET    | `/v1/models`                  | Merged model catalog across enabled backends   |
| GET    | `/`                           | Dashboard: status, routing, client setup       |
| GET    | `/healthz`                    | Liveness probe                                 |
| GET    | `/readyz`                     | Readiness probe (lists enabled backends)       |
| GET    | `/metrics`                    | Prometheus metrics                             |

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
