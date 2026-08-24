#!/usr/bin/env bash
# Build the dashboard SPA (web/) into internal/server/web/webdist so it is
# embedded into the Go binary at compile time. Requires Node.js + npm.
set -euo pipefail
cd "$(dirname "$0")/.."

(cd web && npm ci && npm run build)

rm -rf internal/server/web/webdist
cp -r web/dist internal/server/web/webdist
echo "embedded SPA into internal/server/web/webdist"
