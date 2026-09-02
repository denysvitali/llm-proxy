# llm-proxy

`llm-proxy` is a self-hosted LLM gateway. Clients speak whichever API they
already know — the Anthropic Messages API, the OpenAI Chat Completions API, or
the OpenAI Responses API — and the proxy routes each request to one of the
configured backends, translating between API shapes when needed.

Access control is two-sided: end users authenticate to the proxy with their own
API keys (each user can hold any number of keys), while ordinary upstream
provider keys are configured once, per backend, server-side. Subscription-backed
Grok, WorkBuddy, Codex, and ZCode instead use account sessions signed in from the web dashboard,
not upstream API keys.

> [!IMPORTANT]
> Each backend is a separate service with its own terms of service. Check that
> routing your traffic through `llm-proxy` — including using subscription
> credentials from tools such as Claude Code or Codex — is permitted for your
> account and use case.

## Backends

| Backend   | Upstream                          | Native APIs                              | Notes                                                        |
| --------- | --------------------------------- | ---------------------------------------- | ------------------------------------------------------------ |
| `abliteration` | [abliteration.ai](https://abliteration.ai/docs) | Anthropic Messages, Chat Completions, Responses | All three APIs pass through natively. The live catalog contains `abliterated-model` and the large model variants. |
| `apodex`   | [Apodex](https://platform.apodex.ai/docs) | Anthropic Messages, Chat Completions | Responses clients use the Chat translation path because Apodex's `/responses` compatibility is insufficient for Codex history. See [Apodex](#apodex) for the model tiers and their limits. |
| `opencode` | [OpenCode Zen](https://opencode.ai/docs/zen/) | Anthropic Messages, Chat Completions | Both request shapes pass through byte-for-byte.              |
| `opencode-go` | [OpenCode Go](https://opencode.ai/docs/go/) | Model-specific: Anthropic Messages, Chat Completions, or Responses | Model IDs use the `opencode-go/<id>` qualified form; the proxy selects Go's documented endpoint per model. |
| `grok`     | xAI Grok subscription             | Responses API                            | Anthropic and Chat Completions requests are translated server-side, so Claude Code and Codex work unchanged. |
| `workbuddy` | CodeBuddy International account  | Chat Completions                         | Browser sign-in against `www.codebuddy.ai`; Anthropic and Responses requests are translated server-side. |
| `codex`     | OpenAI Codex subscription         | Responses API                            | ChatGPT device-code sign-in; Anthropic and Chat Completions requests are translated server-side. |
| `zcode`     | ZCode Start Plan                 | Anthropic Messages                       | Browser sign-in stores a ZCode session; Chat Completions and Responses requests are translated server-side. |
| `nous`      | [Nous Portal](https://portal.nousresearch.com/) | Chat Completions (OpenAI-compatible) | Anthropic requests are translated server-side. Models use `vendor/model` slugs (e.g. `nousresearch/hermes-4-70b`). |
| `openrouter` | [OpenRouter](https://openrouter.ai/docs) | Chat Completions (OpenAI-compatible) | Anthropic and Responses requests are translated server-side. Models use `vendor/model` slugs. |
| `venice`    | [Venice AI](https://venice.ai/)   | Chat Completions (OpenAI-compatible)     | Anthropic and Responses requests are translated server-side. |

When an inbound request targets an API shape the routed backend does not speak
natively, the proxy translates the request and — streaming included — the
response.

### abliteration.ai

[abliteration.ai](https://abliteration.ai/docs) provides native OpenAI Chat
Completions, OpenAI Responses, and Anthropic Messages endpoints at
`https://api.abliteration.ai/v1`. Configure it with an API key from the
abliteration.ai console:

```yaml
backends:
  - type: abliteration
    api_key_env: ABLITERATION_API_KEY
```

The proxy fetches the live `GET /v1/models` catalog. The available model IDs
include `abliterated-model`, `abliterated-model-large-v2`, and
`abliterated-model-large`; use a qualified route such as
`abliteration/abliterated-model` to pin this backend.

### Apodex

[Apodex](https://platform.apodex.ai/docs) exposes all three APIs the proxy
speaks. The proxy uses native `/v1/messages` and `/v1/chat/completions`, but
routes Responses clients through Chat translation. Apodex's `/v1/responses`
implementation rejects valid Codex prompt-role history and returns opaque
reasoning state that Codex cannot reliably replay. Configure it like any other
backend:

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

- **Tools.** `apodex-1.1` accepts Chat Completions function tools, which lets
  Codex use client-side tools through the proxy's Responses translation. The
  deep-research models instead run provider-side tools and take `mcp_servers`;
  they are not substitutes for an interactive coding-agent tool loop.
- **Streaming default.** Apodex defaults `stream` to `true` on the deep-research
  models, the opposite of OpenAI. The backend pins the field to whatever the
  inbound request asked for, so a body that omits `stream` still gets one JSON
  object back rather than an unexpected SSE stream.

Non-streaming requests cap `max_tokens` at 32768 and time out around 600
seconds upstream; stream anything longer.

### WorkBuddy

This backend targets **CodeBuddy International** at `www.codebuddy.ai`, not
the China deployment at `copilot.tencent.com` / `codebuddy.cn`.

Enable the backend without an API key:

```yaml
backends:
  - type: workbuddy
```

Start the proxy, open `http://127.0.0.1:8090/login/workbuddy`, and choose
**Sign in with WorkBuddy**. Complete WorkBuddy's Google or GitHub browser
authorization. The proxy stores and refreshes its own account session, so the
WorkBuddy desktop application is not required. Override `workbuddy_auth_file`
at the top level to change where that session is stored. Use qualified model
names such as `workbuddy/hy3` or `workbuddy/glm-5.3`. The catalog is fetched
from CodeBuddy International's authenticated `/v3/config` endpoint and cached
for five minutes; the last successful response is retained during a temporary
catalog outage.

WorkBuddy exposes a streaming-only Chat Completions service internally. The
proxy aggregates it for non-streaming clients and uses the existing translation
matrix for Claude Code (`/v1/messages`) and Codex (`/v1/responses`). This is an
undocumented private endpoint and may change with a WorkBuddy update. Confirm
that this use is permitted by WorkBuddy's terms for your account.

### Codex subscription

Enable Codex without an upstream API key:

```yaml
backends:
  - type: codex
```

Start the proxy, open `http://127.0.0.1:8090/login/codex`, and choose
**Sign in with ChatGPT**. OpenAI displays a one-time device code; approve it
with the ChatGPT account and workspace whose Codex subscription you want to
use. Device-code login must be enabled in the account's ChatGPT security
settings or by its workspace administrator.

The session is stored at `~/.config/llm-proxy/codex-auth.json` with mode
`0600` and refreshed automatically. Override `codex_auth_file` at the top
level to change the path. The live authenticated catalog is cached for five
minutes, and models are available as `codex/<model>`. The backend calls
ChatGPT's Codex Responses service, so Chat Completions and Anthropic Messages
clients use the proxy's normal translation paths.

### ZCode Start Plan

The `zcode` backend uses the ZCode Start Plan account session. It does not
use a Z.ai API key or require a JWT in the configuration. Enable it with:

```yaml
backends:
  - type: zcode

routes:
  glm-5.3-flash:
    backend: zcode
    model: glm-5.3-flash
```

```bash
./llm-proxy serve
```

Open `http://127.0.0.1:8090/login/zcode` (or use the **Sign in with ZCode**
link on the dashboard), complete the browser authorization, and leave the
page open until it confirms success. The resulting ZCode session is stored at
`~/.config/llm-proxy/zcode-auth.json` with mode `0600`; override
`zcode_auth_file` at the top level to change the path. Revisit the same page
when the session expires.

After signing in, click **Verify browser session** on that page before making a
model request. ZCode's plan gateway requires the short-lived Aliyun browser
verification parameter; the proxy stores one proof for up to about 40 seconds
and consumes it for one model request, matching ZCode's one-use verification
flow. Verify again before the next request unless the optional CAPTCHA solver
is configured. When `stats.redis_url` points to Redis or Valkey, the proof is
stored in that shared service so exactly one replica can consume it. Without
shared Redis/Valkey, a separate owner-only file beside `zcode-auth.json` is
used for a local single-replica deployment.
A fresh proxy verification takes precedence over a stale client-supplied
`X-Aliyun-Captcha-Verify-Param`. The proxy emits the current ZCode platform
headers and keeps client identity fields under proxy control; inbound clients
cannot override them.

The backend forwards requests to
`https://zcode.z.ai/api/v1/zcode-plan/anthropic/v1/messages`. Chat
Completions and Responses requests are translated to Anthropic Messages
before forwarding because ZCode's legacy `/chat/completions` plan route is no
longer accepted. The current Start Plan catalog entry is `glm-5.3-flash`. To
use another model that ZCode enables for the account, add an explicit route
for it.

This uses an undocumented provider gateway discovered from the ZCode client
and may change with a ZCode release. Confirm that routing your own entitlement
through a proxy is allowed by the service terms.

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

### OpenCode Go

[OpenCode Go](https://opencode.ai/docs/go/) is a low-cost subscription with
model-specific endpoints. Configure it with the API key from OpenCode Zen:

```yaml
backends:
  - type: opencode-go
    api_key_env: OPENCODE_API_KEY
```

The proxy fetches Go's live model catalog from
`https://opencode.ai/zen/go/v1/models`. Go models use the qualified
`opencode-go/<model-id>` form. The proxy sends each model to its documented
native endpoint and translates client requests when the client API differs.

## Install

Requires Go (see `go.mod` for the minimum version).

```bash
go build -o llm-proxy .
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
   For a `codex` backend, open `http://127.0.0.1:8090/login/codex` and complete
   the ChatGPT device-code flow instead.
   For a `zcode` backend, open `http://127.0.0.1:8090/login/zcode` and complete
   the browser authorization instead.

3. Create a user and mint an API key. The plaintext key starts with `llx_`,
   grants access to everything the proxy can reach, and is shown exactly once:

   ```bash
   ./llm-proxy keys create-user alice
   ./llm-proxy keys create alice --name laptop
   # llx_9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822c
   ```

   `llm-proxy keys list alice` shows existing keys (never their secrets);
   `llm-proxy keys set-state alice <key-id> --disable` revokes one.

   The proxy watches the key-store file and reloads it within a few seconds,
   so keys minted or revoked while `serve` is running take effect without a
   restart.

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
   backend serves it. The pinned backend's live catalog is authoritative: a
   qualified ID the catalog no longer lists is not forwarded (the upstream's
   own rejection would relay as a misleading 4xx — e.g. a removed model
   surfacing as a 401 auth failure clients keep retrying); the backend's
   fallback chain serves it instead, and with none configured the request
   answers `404` like any unroutable model. A catalog that cannot be fetched,
   or answers an empty list, fails open and the request is forwarded as
   before.
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

### Fallbacks

A route (or backend) can name fallback backends that take over when the
primary fails **before anything has reached the client** — transport errors,
a spent retry budget, or a terminal 5xx. Once streaming content has flowed,
the break is surfaced in-band instead; replaying on another backend would
duplicate output. Client-side rejections (4xx) are relayed, not failed over.

```yaml
backends:
  - type: opencode
    api_key_env: OPENCODE_API_KEY
    retry_attempts: 4          # give up on this backend sooner...
    retry_max_backoff: 10s
    fallbacks:                  # ...and let grok serve the request
      - backend: grok
        model: grok-4.5
```

Fallback entries carry an optional model rewrite (`model`); without one the
primary's upstream model name is kept. The route entry's `fallbacks` run
first, then the primary backend's own, capped at four backends per request
including the primary. Fallbacks also apply to qualified IDs like
`opencode/model`, which bypass routes entirely — both when the pinned backend
fails mid-request and when its catalog no longer lists the model (the primary
is skipped without a round trip).

Each hand-off increments `llm_proxy_fallbacks_total{from_backend,to_backend}`;
retry metrics carry `backend` and `model` labels, so exhaustion per backend
is directly observable (see the alerting rules shipped in the deployment
repository).

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
| `stats.persist_file`         | `LLM_PROXY_STATS_PERSIST_FILE`       | `~/.local/state/llm-proxy/stats.json` | JSON snapshot backing dashboard history. Empty disables persistence and time-range charts. |
| `stats.persist_interval`     | `LLM_PROXY_STATS_PERSIST_INTERVAL`   | `1m` when persistence is enabled | How often stats are flushed to `stats.persist_file`. |
| `stats.retention_days`       | `LLM_PROXY_STATS_RETENTION_DAYS`     | `7` when persistence is enabled | Bucket retention period; `0` keeps history indefinitely. |
| `stats.redis_url`            | `LLM_PROXY_STATS_REDIS_URL`          | *(empty)*              | Redis/Valkey URL for shared stats and cross-instance dashboard notifications. When set, this replaces local JSON stats persistence. |
| `stats.redis_key_prefix`     | `LLM_PROXY_STATS_REDIS_KEY_PREFIX`   | `llm-proxy:stats:`     | Prefix used to isolate this proxy's stats keys and Pub/Sub channel in a shared Redis/Valkey instance. |
| `grok_auth_file`             | `LLM_PROXY_GROK_AUTH_FILE`         | `~/.config/grok-proxy/auth.json` | xAI account session file used by the Grok subscription backend. This is not an API key. |
| `workbuddy_auth_file`        | `LLM_PROXY_WORKBUDDY_AUTH_FILE`    | `~/.config/llm-proxy/workbuddy-auth.json` | Account session created by the WorkBuddy browser sign-in flow. |
| `codex_auth_file`            | `LLM_PROXY_CODEX_AUTH_FILE`        | `~/.config/llm-proxy/codex-auth.json` | ChatGPT session created by the Codex device-code sign-in flow. |
| `zcode_auth_file`            | `LLM_PROXY_ZCODE_AUTH_FILE`        | `~/.config/llm-proxy/zcode-auth.json` | ZCode session created by the browser sign-in flow. |
| —                            | `LLM_PROXY_ZCODE_CAPTCHA_SOLVER_URL` | empty | Optional internal endpoint that returns a fresh one-use ZCode CAPTCHA proof as `{"verify_param":"..."}` for every upstream request. |
| `backends[].type`            | —                                    | required                 | Registered backend type (`abliteration`, `apodex`, `venice`, `opencode`, `opencode-go`, `grok`, `workbuddy`, `codex`, `zcode`, `nous`, `openrouter`); at most one backend per type. |
| `backends[].base_url`        | —                                    | per-provider default     | Override the upstream endpoint.                                             |
| `backends[].api_key_env`     | —                                    | —                        | Name of an environment variable holding an ordinary upstream key. Account-backed backends (`grok`, `workbuddy`, `codex`, `zcode`) use their web sign-in sessions instead. |
| `backends[].api_key`         | —                                    | —                        | Literal ordinary upstream key. Account-backed backends use their web sign-in sessions instead. |
| `backends[].enabled`         | —                                    | `true`                   | Set `false` to take the backend out of routing without deleting it.         |
| `backends[].default_model`   | —                                    | —                        | Model used when a client model cannot be matched against this backend's catalog. |
| `backends[].fallbacks`       | —                                    | —                        | Alternate backends (with optional model rewrites) tried when this backend fails before anything reaches the client. |
| `backends[].retry_attempts`  | —                                    | `3`                      | Extra connection-phase attempts after a transient upstream failure.        |
| `backends[].retry_max_backoff` | —                                  | `30s`                    | Cap on a single retry pause (exponential backoff and provider `Retry-After`). |
| `routes.<model>.backend`     | —                                    | —                        | Backend serving this inbound model name.                                    |
| `routes.<model>.model`       | —                                    | inbound model name       | Upstream model name sent to the backend.                                    |
| `routes.<model>.fallbacks`   | —                                    | —                        | Fallbacks for this route, tried before the backend's own.                   |
| `default_route.backend`      | —                                    | —                        | Backend for models that match nothing else.                                 |
| `default_route.model`        | —                                    | inbound model name       | Upstream model rewrite applied on the default route.                        |
| `default_route.fallbacks`    | —                                    | —                        | Fallbacks applied when the default route backend fails.                     |
| `log_level`                  | `LLM_PROXY_LOG_LEVEL`                | `info`                   | Log verbosity.                                                              |
| `log_format`                 | `LLM_PROXY_LOG_FORMAT`               | `text`                   | `text` or JSON logging.                                                     |

Tracing is configured through the standard `OTEL_*` environment variables
(`OTEL_TRACES_EXPORTER=otlp`, `OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_EXPORTER_OTLP_PROTOCOL`). When none is set, tracing stays a no-op.
With a collector configured, every request gets a span and every backend
attempt a child span carrying `llm_proxy.backend`, `llm_proxy.model` and the
attempt's outcome; the access log gains the matching `trace_id`.

For an HA deployment, configure every proxy instance with the same
`stats.redis_url` and `stats.redis_key_prefix`. Redis/Valkey becomes the
shared source for dashboard counters, latency histograms, time series, and
cross-instance live-update notifications. Prometheus metrics remain
per-instance and should continue to be scraped from every replica. If Redis is
not configured or becomes unavailable, the proxy continues serving requests
and dashboard reads fall back to process-local metrics.

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
| GET    | `/api/updates/ws`             | WebSocket stream of stats-change events for the dashboard |
| GET    | `/api/updates/sse`             | Server-Sent-Events twin of the WebSocket, for transports that cannot upgrade |
| GET/POST | `/login`                    | Web-only xAI account sign-in for Grok |
| GET/POST | `/login/workbuddy`          | Web-only WorkBuddy account sign-in |
| GET/POST | `/login/codex`              | ChatGPT device-code sign-in for Codex |
| GET/POST | `/login/zcode`              | Web-only ZCode account sign-in and browser verification |
| POST     | `/login/zcode/captcha`      | Stores the current browser verification parameter |
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
