#!/usr/bin/env bash
# Runs the scenario for the Stampede action: writes HTML, JSON, JUnit and
# Markdown reports, appends the Markdown to the job summary and sets the
# verdict, p95, error-rate, exit-code and report-dir outputs. It never fails
# itself, so the reports are uploaded before the verdict is checked.
set -uo pipefail

out_file="${GITHUB_OUTPUT:-/dev/stdout}"
dir="${INPUT_OUTPUT_DIR:-stampede-report}"
mkdir -p "$dir"

args=(run "${INPUT_SCENARIO:?the scenario input is required}"
  -o "${dir}/report.html" --json "${dir}/report.json" --junit "${dir}/junit.xml" --md "${dir}/summary.md")
[ -n "${INPUT_TARGET_URL:-}" ] && args+=(--base-url "$INPUT_TARGET_URL")
[ -n "${INPUT_SHAPE:-}" ] && args+=(--shape "$INPUT_SHAPE")
[ -n "${INPUT_RATE:-}" ] && args+=(--rate "$INPUT_RATE")
[ -n "${INPUT_VUS:-}" ] && args+=(--vus "$INPUT_VUS")
[ -n "${INPUT_DURATION:-}" ] && args+=(--duration "$INPUT_DURATION")
if [ -n "${INPUT_EXTRA_ARGS:-}" ]; then
  read -r -a extra <<<"$INPUT_EXTRA_ARGS"
  args+=("${extra[@]}")
fi

echo "stampede ${args[*]}"
stampede "${args[@]}"
code=$?

if [ -f "${dir}/summary.md" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  cat "${dir}/summary.md" >>"$GITHUB_STEP_SUMMARY"
fi

verdict="" p95="" error_rate=""
if [ -f "${dir}/report.json" ]; then
  if command -v jq >/dev/null 2>&1; then
    verdict=$(jq -r '.verdict' "${dir}/report.json")
    p95=$(jq -r '.overall.latency.p95' "${dir}/report.json")
    error_rate=$(jq -r '.overall.errorRate' "${dir}/report.json")
  else
    read -r verdict p95 error_rate < <(python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print(r["verdict"], r["overall"]["latency"]["p95"], r["overall"]["errorRate"])' "${dir}/report.json")
  fi
fi

{
  echo "exit-code=${code}"
  echo "verdict=${verdict}"
  echo "p95=${p95}"
  echo "error-rate=${error_rate}"
  echo "report-dir=${dir}"
} >>"$out_file"
exit 0
