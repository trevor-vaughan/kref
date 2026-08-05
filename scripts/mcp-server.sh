#!/usr/bin/env bash
# mcp-server.sh — launch kref's MCP server for this repository.
#
# Wired into the repo-scoped .mcp.json so an agent host starts the server with
# no absolute paths in committed config. Two jobs beyond `kref mcp`:
#
#   1. Find a binary. ./bin/kref (the `task build` output) wins over an
#      installed kref on PATH: in this repo you almost always mean the build of
#      the tree you are editing, not whatever release is installed. When neither
#      exists, say how to get one instead of failing with a bare ENOENT.
#   2. Anchor the working directory to the repo root. kref resolves its store
#      from the current directory git-style, so this keeps the server pointed at
#      this checkout however the host happened to launch it.
#
# NOTE: ./bin/kref is only refreshed by `task build` — `task test`/`check`/`e2e`
# build to a temp path — so a stale ./bin/kref serves a stale MCP surface. Run
# `task build` after changing anything under cmd/ or internal/.
#
# stdout is the MCP JSON-RPC channel; every diagnostic here goes to stderr.
# Arguments are forwarded to `kref mcp` (e.g. --allow, --dir).
set -euo pipefail

# Resolve this script's directory, following symlinks, so the repo root is
# found from the script's own location rather than the caller's cwd.
SOURCE="${BASH_SOURCE[0]}"
while [[ -L "$SOURCE" ]]; do
    DIR="$(cd -P "$(dirname "$SOURCE")" && pwd)"
    SOURCE="$(readlink "$SOURCE")"
    [[ "$SOURCE" != /* ]] && SOURCE="$DIR/$SOURCE"
done
SCRIPT_DIR="$(cd -P "$(dirname "$SOURCE")" && pwd)"
REPO_ROOT="$(cd -P "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

if [[ -x "$REPO_ROOT/bin/kref" ]]; then
    exec "$REPO_ROOT/bin/kref" mcp "$@"
fi

if command -v kref >/dev/null 2>&1; then
    exec kref mcp "$@"
fi

cat >&2 <<MSG
kref: no binary found, so the MCP server cannot start.

Looked for an executable at $REPO_ROOT/bin/kref (the \`task build\` output),
then for kref on PATH. Neither is there.

Build one from this checkout:

  task build        # writes ./bin/kref

or install it onto your PATH:

  go install ./cmd/kref

Then reconnect the server in your agent host.
MSG
exit 1
