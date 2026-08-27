# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`llm-proxy` is a self-hosted LLM gateway (Go 1.25, stdlib `net/http`, cobra/viper). Clients speak any of three APIs — Anthropic Messages (`/v1/messages`), OpenAI Chat Completions, OpenAI Responses — and every endpoint can reach every backend: the proxy carries a full 3×3 translation matrix (request, response, and SSE streaming, in both directions). README.md documents user-facing behavior (config fields, routing rules, backends) in depth; read it before changing anything user-visible.

## Commands

```bash
go build -o llm-proxy .              # package main is at repo root, NOT ./cmd
go test ./...                        # all tests
go test -race ./internal/server -run 'TestHandleMessages' -v   # single test
go vet ./...
test -z "$(gofmt -l .)"              # CI enforces gofmt
golangci-lint run                    # CI builds it from source (Go-version lag)
```

Dashboard SPA (`web/`, React 19 + Mantine + Vite):

```bash
cd web && npm ci && npm run lint     # oxlint
../scripts/build-web.sh              # build SPA into internal/server/web/webdist
```

`webdist/` is committed and `go:embed`-ed into the binary (`internal/server/web.go`). **After any `web/` change, run `scripts/build-web.sh` and commit the rebuilt `webdist/`** — otherwise the binary serves stale UI. For quick iteration `npm run dev` (Vite proxy) needs no rebuild.

Running locally:

```bash
./llm-proxy serve --config <yaml>    # config defaults to ~/.config/llm-proxy/config.yaml
./llm-proxy keys create-user alice && ./llm-proxy keys create alice --name laptop
```

## Architecture

Request flow: handler (`internal/server/{anthropic,openai_chat,openai_responses}.go`) → auth middleware (`llx_` key against `internal/auth` JSON store) → model routing → wire resolution → `exchange()` with retry/fallback → response relayed verbatim (native) or translated.

- **`internal/backend/`** — the `Backend` interface (`backend.go`): `Name/Models/Supports/Send`. Providers register via `init()` into a registry; **`all/all.go` blank-imports every backend package** — adding a backend means writing the package plus one line there, plus `backends[].type` docs in README. Optional `ModelWireOverrider` lets a backend decide wire support per model (used by `opencode-go`). `grok` is the outlier: xAI account OAuth session (web `/login` flow), no upstream API key.
- **`internal/translate/`** — pure, testable conversion functions between the three wire formats. No HTTP, no state; everything else calls these.
- **`internal/server/wire.go`** — the translation core: `translations` map keyed by `[inbound Kind, backend wire Kind]`, with diagonal = passthrough (model rewrite only). Wire preference order: native first, then OpenAI shapes before Anthropic.
- **`internal/server/server.go`** — routing precedence: qualified `<backend>/<model>` IDs (split at first `/`, so nested upstream slugs work) > explicit `routes` > live backend catalogs (cached ~1 min) > `default_route`. Unroutable → 404.
- **Fallbacks** (`retry.go`, `fallback_test.go`) — fail over to another backend only when nothing has reached the client yet (transport error, spent retry budget, terminal 5xx); mid-stream breaks surface in-band instead. 4xx from the client side are relayed, not failed over. Max 4 backends per request.
- **Stats** (`stats.go`, `redis_stats.go`) — per backend+model metrics feeding `/stats`, `/api/overview`, Prometheus `/metrics`, and the dashboard's live updates (`update.go`: WebSocket + SSE hub). Optional Redis/Valkey for HA shared stats; falls back to process-local.
- **`internal/config`** — viper loader; env vars use `LLM_PROXY_` prefix with dots→underscores.
- **`internal/tracing`** — OTel spans per request and per backend attempt; no-op without `OTEL_*` env.

Tests are table-driven with `httptest` fake upstreams — the translation matrix test (`server/translation_matrix_test.go`) exercises every client-API × wire-format pair and is the gate for any translation change.

## CI (`.github/workflows/`)

- `test.yml`: gofmt + `go vet` + `go test -race`.
- `lint.yml`: golangci-lint (installed from source — release binaries lag the module's Go version).
- `e2e.yml`: builds the proxy, points real Claude Code and Codex at it, runs against live Venice/OpenRouter/OpenCode when secrets exist; skips gracefully otherwise. Note: proxy keys must be created **before** starting `serve` — the keystore is loaded once at startup and not watched.
- `container.yml`: distroless image → `ghcr.io/denysvitali/llm-proxy`.

Commits follow conventional style (`feat(stats):`, `fix(translate):`, …) — match it.
