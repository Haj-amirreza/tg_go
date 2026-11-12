#!/usr/bin/env sh
# Build the same versioned artifacts used by Dockerfile.next.prebuilt.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
go_bin=${GO_BIN:-go}
[ "$("$go_bin" version | awk '{print $3}')" = go1.27.4 ] || {
  echo 'Go 1.27.4 is required; set GO_BIN to its executable.' >&2
  exit 1
}
case "$(node --version)" in v24.22.1) ;; *) echo 'Node 24.22.1 is required.' >&2; exit 1;; esac
cd "$root/frontend/admin"
npm ci --strict-ssl=true
npm run build
# A non-root scratch container needs readable static assets regardless of host umask.
find dist -type d -exec chmod 755 {} +
find dist -type f -exec chmod 644 {} +
cd "$root/backend"
mkdir -p bin
CGO_ENABLED=0 "$go_bin" build -trimpath -ldflags='-s -w' -o bin/unibot ./cmd/unibot
printf '%s\n' 'Built backend/bin/unibot and frontend/admin/dist; no credentials were loaded.'
