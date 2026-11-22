#!/bin/sh
set -eu
cd "$(dirname "$0")"
go test ./...
GOOS=linux GOARCH=arm GOARM=5 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o ../KOBOeReader/.adds/readwise-sync/readwise-sync-arm .
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o ../KOBOeReader/.adds/readwise-sync/readwise-sync-arm64 .
