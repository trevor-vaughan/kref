#!/usr/bin/env bash
#
# Render a kref VHS demo tape into a GIF.
#
# Each demo runs against a throwaway git repo under $(mktemp -d), never the real
# working tree: kref stores entries in refs/kref-* and we do not want demo state
# (or the project's own entries) bleeding into the recording. The sandbox is also
# what lets the recording execute a freshly built ./kref even on hosts where the
# repo lives on a noexec/SELinux-restricted mount.
#
# Usage: demo.sh <root_dir> <scenario> <tape> <output_gif>
#   root_dir     repository root (used to locate ./cmd/kref and ./bin/betterleaks)
#   scenario     tour | secrets  — selects how the sandbox is pre-seeded
#   tape         absolute path to the .tape file to render
#   output_gif   absolute path to write the finished GIF to
#
# Requires on PATH: go, vhs, ttyd, ffmpeg. The pinned betterleaks binary must
# exist at <root_dir>/bin/betterleaks (provisioned by `task dev:tools`).
set -euo pipefail

if [[ $# -ne 4 ]]; then
	echo "usage: demo.sh <root_dir> <scenario> <tape> <output_gif>" >&2
	exit 2
fi

ROOT_DIR=$1
SCENARIO=$2
TAPE=$3
OUTPUT_GIF=$4

# Prefer a repo-local vhs in ./bin if one was installed there, while still
# honouring a system-wide vhs already on PATH.
export PATH="$ROOT_DIR/bin:$PATH"

# go.mod pins Go >= 1.26.4; let Go fetch that toolchain if the local one is older
# (matches the rest of the project's tasks). Respect an explicit override.
: "${GOTOOLCHAIN:=auto}"
export GOTOOLCHAIN

for tool in go vhs ttyd ffmpeg; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "demo.sh: '$tool' not found on PATH" >&2
		exit 1
	}
done

BETTERLEAKS_SRC="$ROOT_DIR/bin/betterleaks"
[[ -x "$BETTERLEAKS_SRC" ]] || {
	echo "demo.sh: betterleaks not found at $BETTERLEAKS_SRC (run 'task dev:tools')" >&2
	exit 1
}
[[ -f "$TAPE" ]] || {
	echo "demo.sh: tape not found: $TAPE" >&2
	exit 1
}

WORK=$(mktemp -d "${TMPDIR:-/tmp}/kref-demo.XXXXXX")
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

# Build kref fresh into the sandbox so the recorded shell always has an
# executable binary next to its betterleaks sibling (kref auto-discovers a
# betterleaks placed alongside its own binary). The binaries live OUTSIDE the
# demo repo ($WORK/bin vs $WORK/repo) so the tour's clean-`git status` closer
# stays honest.
mkdir -p "$WORK/bin"
(cd "$ROOT_DIR" && go build -ldflags "-X main.Version=demo" -o "$WORK/bin/kref" ./cmd/kref)
cp "$BETTERLEAKS_SRC" "$WORK/bin/betterleaks"
chmod +x "$WORK/bin/kref" "$WORK/bin/betterleaks"

# A demo repo with a stable, friendly author identity.
REPO="$WORK/repo"
mkdir -p "$REPO"
git -C "$REPO" init -q -b main
git -C "$REPO" config user.name "Ada Lovelace"
git -C "$REPO" config user.email "ada@example.com"
git -C "$REPO" config commit.gpgsign false

export PATH="$WORK/bin:$PATH"

case "$SCENARIO" in
tour)
	# The tour types `kref init` and the `new` commands live. It captures one
	# rich spec via --body "$(< docs/auth-flow.md)", so seed that file and
	# commit it — the tape closes on a clean `git status` to make the point
	# that kref leaves the working tree untouched.
	mkdir -p "$REPO/docs"
	cat >"$REPO/docs/auth-flow.md" <<'SPEC'
# Auth flow

OAuth2 **device-code grant** for the CLI. The browser does the heavy lifting;
the terminal only polls.

## Decisions

- Access tokens are short-lived (15 min) and never written to disk
- Refresh tokens rotate on every use — reuse of a stale one revokes the family
- Device codes expire after 10 minutes; polling backs off on `slow_down`

## Token endpoint

| Field         | Value                          |
|---------------|--------------------------------|
| Grant type    | `urn:ietf:params:oauth:grant-type:device_code` |
| Poll interval | 5s, `slow_down` doubles it     |
| Rotation      | refresh token, per use         |

## Polling sketch

```go
for {
    tok, err := poll(ctx, deviceCode)
    if errors.Is(err, errSlowDown) {
        interval *= 2
        continue
    }
    if err == nil {
        return keyring.Store(tok) // never touches disk unencrypted
    }
}
```

Rollout is gated behind the `auth-v2` flag until the fleet is upgraded.
SPEC
	git -C "$REPO" add docs
	git -C "$REPO" commit -qm "docs: auth flow spec"
	;;
secrets)
	# The secrets demo ingests a pre-existing docs/ tree, so it must already be
	# initialized. One file carries a GitHub-PAT-shaped token generated at
	# render time (never committed) so betterleaks quarantines it for real.
	(cd "$REPO" && kref init >/dev/null)
	mkdir -p "$REPO/docs"
	# shellcheck disable=SC2016  # backticks are literal Markdown code spans; single quotes are required so they do not expand
	printf '# Deploy runbook\n\nRun `make deploy`; roll back with `make rollback`.\n' \
		>"$REPO/docs/runbook.md"
	# shellcheck disable=SC2016  # backticks are literal Markdown code spans; single quotes are required so they do not expand
	printf '# Onboarding\n\nClone, run `task build`, then read the README.\n' \
		>"$REPO/docs/onboarding.md"
	token="ghp_$(head -c 64 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 36)"
	printf '# CI credentials\n\nDeploy-bot token for GitHub Actions:\n\n    GITHUB_TOKEN=%s\n' \
		"$token" >"$REPO/docs/ci-creds.md"
	;;
*)
	echo "demo.sh: unknown scenario '$SCENARIO' (want tour|secrets)" >&2
	exit 2
	;;
esac

# The tape's Hide preamble reads $KREF_DEMO_DIR to cd into the sandbox; -o
# overrides the tape's placeholder Output path so the GIF lands where the
# Taskfile wants it.
mkdir -p "$(dirname "$OUTPUT_GIF")"
KREF_DEMO_DIR="$REPO" vhs -o "$OUTPUT_GIF" "$TAPE"

echo "demo.sh: wrote $OUTPUT_GIF"
