#!/usr/bin/env bash
# Builds the React UI, embeds it, and compiles a single deepcode binary.
set -euo pipefail
cd "$(dirname "$0")/.."
npm --prefix web ci
npm --prefix web run build
rm -rf internal/app/server/webui/dist
mkdir -p internal/app/server/webui/dist
cp -R web/dist/. internal/app/server/webui/dist/
touch internal/app/server/webui/dist/.gitkeep
# -s -w: smaller binary, and on Windows with TDM-GCC an unstripped cgo build
# is rejected by the loader ("not a valid application for this OS platform").
go build -ldflags="-s -w" -o "deepcode$(go env GOEXE)" ./cmd/deepcode
