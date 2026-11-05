# shellcheck shell=bash
# Checks shared by release-changelog.sh and release-tag.sh, so both refuse the same
# versions. Sourced, not executed.

# Keep identical to tag_pattern in cliff.toml.
RELEASE_TAG_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'

die() {
  echo "error: $1" >&2
  shift
  for hint in "$@"; do echo "  hint: $hint" >&2; done
  exit 1
}

# check_release_version <task> <X.Y.Z or vX.Y.Z>: sets `version` (no leading v) and `last`
# (the newest release tag reachable from HEAD, or empty). Dies unless the version is
# well-formed, untagged, and after the last release.
check_release_version() {
  local task="$1" newest
  version="${2:-}"
  [[ -n "$version" ]] ||
    die "$task needs the version" "task $task -- 0.2.0"
  version="${version#v}"
  [[ "v$version" =~ $RELEASE_TAG_RE ]] ||
    die "version \"$version\" is not X.Y.Z" "task $task -- 0.2.0"
  ! git rev-parse -q --verify "refs/tags/v$version" >/dev/null ||
    die "tag v$version already exists" "git show v$version"

  last="$(git tag --merged HEAD --list 'v*' --sort=-v:refname | grep -E -m1 "$RELEASE_TAG_RE" || true)"
  if [[ -n "$last" ]]; then
    newest="$(printf '%s\n%s\n' "${last#v}" "$version" | sort -V | tail -n1)"
    [[ "$newest" == "$version" && "${last#v}" != "$version" ]] ||
      die "v$version is not after the last release, $last"
  fi
}

# release_notes <git-cliff>: prints the notes for v$version, without header. Dies if no
# Conventional Commit since $last would appear in them.
release_notes() {
  local notes
  notes="$("$1" --config cliff.toml --unreleased --tag "v$version" --strip all | sed "/./,\$!d")"
  grep -q '^- ' <<<"$notes" ||
    die "no Conventional Commit since ${last:-the start of the history}; there is nothing to release" "task release:notes"
  printf '%s\n' "$notes"
}
