#!/usr/bin/env bash
# Builds the React UI, embeds it, and compiles a single deepcode binary.
set -euo pipefail
cd "$(dirname "$0")/.."
npm --prefix web ci
npm --prefix web run check # Biome: format, lint and import order
npm --prefix web run build
# The Mermaid parser web/ draws with, which the wiki checks a page's diagrams with before writing it.
NODE_ENV=production web/node_modules/.bin/rolldown -c scripts/mermaid/rolldown.config.mjs
gzip -9 -f internal/knowledge/wiki/outline/skeleton/mermaid/dist/parse.js
rm -rf internal/app/server/webui/dist
mkdir -p internal/app/server/webui/dist
cp -R web/dist/. internal/app/server/webui/dist/
touch internal/app/server/webui/dist/.gitkeep
# -s -w: smaller binary, and on Windows with TDM-GCC an unstripped cgo build
# is rejected by the loader ("not a valid application for this OS platform").
# VERSION (the CI sets it) is what deepcode --version prints.
go build -trimpath -ldflags="-s -w -X main.version=${VERSION:-dev}" -o "deepcode$(go env GOEXE)" ./cmd/deepcode
