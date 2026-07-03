#!/usr/bin/env bash
# run-ginkgo.sh — run the Ginkgo suites through `go test`, capture each
# package's JSON report, and render them with ginkgoleaf.
#
# Every argument is forwarded verbatim to `go test` (build tags, -race, -v,
# -coverprofile, the ./... pattern, …); this script appends
# `-args --ginkgo.json-report=<name>` so each Ginkgo binary writes a report
# beside its package. Each report is rendered, archived under $OUT_DIR/reports
# with a package-qualified name, and the scattered original is removed so the
# source tree stays clean.
#
# Required env:
#   GINKGOLEAF  path to the ginkgoleaf binary
# Optional env:
#   FORMAT      ginkgoleaf output format (default: tree; use "github" in CI)
#   OUT_DIR     archive root for rendered reports (default: .test-output)
#
# Exits non-zero when `go test` fails (build or spec failure) or any rendered
# report contains spec failures. No `set -e`: we must render and report even
# after `go test` exits non-zero on a failing suite.
set -uo pipefail

: "${GINKGOLEAF:?set GINKGOLEAF to the ginkgoleaf binary path}"
format="${FORMAT:-tree}"
out_dir="${OUT_DIR:-.test-output}"
report_name="ginkgo-report.json"
archive="${out_dir}/reports"

mkdir -p "$archive"
# Clear last run's renders and any reports orphaned by an interrupted run.
# Archived reports carry package-qualified names, so they never match
# -name "$report_name" and are left untouched here.
rm -f "$archive"/*.json
find . -name "$report_name" -delete

go test "$@" -args --ginkgo.json-report="$report_name"
test_rc=$?

render_rc=0
# This script deliberately runs without `set -e` so it always renders every
# report. SC2312 flags the report-name formatter substitution and the `find`
# in the loop's process substitution below; neither's exit status should abort
# rendering, so the masking is intentional.
# shellcheck disable=SC2312
while IFS= read -r report; do
	"$GINKGOLEAF" --format="$format" --exit-code --in "$report" || render_rc=1
	mv "$report" "${archive}/$(printf '%s' "${report#./}" | tr '/' '_')"
done < <(find . -name "$report_name")

[[ "$test_rc" -eq 0 ]] && [[ "$render_rc" -eq 0 ]]
