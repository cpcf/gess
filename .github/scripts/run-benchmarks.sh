#!/usr/bin/env bash
set -euo pipefail

checkout=${1:-.}
count=${BENCH_COUNT:-6}
benchtime=${BENCH_TIME:-1s}
profile=${BENCH_PROFILE:-full}

smoke_patterns=(
  '^BenchmarkSessionExplainLogRecording$'
  '^BenchmarkSessionAssertNoExplainLog$'
  '^BenchmarkClaimsTriageGessSessionCycle$'
  '^BenchmarkGessSteadyStateRuleCreatedFacts$/^streams=8$/^limit=64$/^rules=48$/^final-facts=2096$/^fired=2088$'
  '^BenchmarkGessNegationScalingSeedRun$/^streams=8$/^customers=1024$/^block-every=2$/^rules=8$/^final-facts=12288$/^fired=4096$'
)

full_patterns=(
  "${smoke_patterns[@]}"
  '^BenchmarkGessAggregateScalingSteadyStateMutations$/^modify-input$/^streams=8$/^items=1024$/^rules=8$'
  '^BenchmarkGessMannersSessionRun$/^guests=64$'
  '^BenchmarkSnapshotQueryAllScaling$/^facts=10000$'
  '^BenchmarkSessionForkCorpusScale$'
  '^BenchmarkSessionWhatIfCorpusScale$'
  '^BenchmarkBackchainDemandSupportChurn$/^active=1024$'
)

extended_patterns=(
  "${full_patterns[@]}"
  '^BenchmarkGessMannersSessionRun$/^guests=16$'
  '^BenchmarkGessMannersSessionRun$/^guests=32$'
  '^BenchmarkGessMannersSessionRun$/^guests=128$'
  '^BenchmarkSnapshotQueryAllScaling$/^facts=100000$'
  '^BenchmarkBackchainDemandSupportChurn$/^active=1$'
  '^BenchmarkBackchainDemandSupportChurn$/^active=128$'
)

case "$profile" in
  smoke) patterns=("${smoke_patterns[@]}") ;;
  full) patterns=("${full_patterns[@]}") ;;
  extended) patterns=("${extended_patterns[@]}") ;;
  *)
    echo "unknown benchmark profile: $profile" >&2
    exit 2
    ;;
esac

cd "$checkout"
for pattern in "${patterns[@]}"; do
  if ! output=$(go test ./internal/engine \
    -run '^$' \
    -bench "$pattern" \
    -benchmem \
    -benchtime "$benchtime" \
    -count "$count" 2>&1); then
    printf '%s\n' "$output"
    exit 1
  fi

  printf '%s\n' "$output"
  if ! grep -Eq '^Benchmark[^[:space:]]+[[:space:]]+[[:digit:]]+' <<< "$output"; then
    echo "benchmark pattern matched no results: $pattern" >&2
    exit 1
  fi
done
