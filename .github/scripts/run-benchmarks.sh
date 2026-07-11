#!/usr/bin/env bash
set -euo pipefail

checkout=${1:-.}
count=${BENCH_COUNT:-6}
benchtime=${BENCH_TIME:-1s}

patterns=(
  '^BenchmarkSessionExplainLogRecording$'
  '^BenchmarkSessionAssertNoExplainLog$'
  '^BenchmarkClaimsTriageGessSessionCycle$'
  '^BenchmarkGessSteadyStateRuleCreatedFacts$/^streams=8$/^limit=64$/^rules=48$/^final-facts=2096$/^fired=2088$'
  '^BenchmarkGessNegationScalingSeedRun$/^streams=8$/^customers=1024$/^block-every=2$/^rules=8$/^final-facts=12288$/^fired=4096$'
  '^BenchmarkGessAggregateScalingSteadyStateMutations$/^modify-input$/^streams=8$/^items=1024$/^rules=8$'
  '^BenchmarkGessMannersSessionRun$/^guests=64$'
  '^BenchmarkSnapshotQueryAllScaling$/^facts=10000$'
  '^BenchmarkSessionForkCorpusScale$'
  '^BenchmarkSessionWhatIfCorpusScale$'
  '^BenchmarkBackchainDemandSupportChurn$/^active=1024$'
)

if [[ ${BENCH_FULL:-0} == 1 ]]; then
  patterns+=(
    '^BenchmarkGessMannersSessionRun$/^guests=16$'
    '^BenchmarkGessMannersSessionRun$/^guests=32$'
    '^BenchmarkGessMannersSessionRun$/^guests=128$'
    '^BenchmarkSnapshotQueryAllScaling$/^facts=100000$'
    '^BenchmarkBackchainDemandSupportChurn$/^active=1$'
    '^BenchmarkBackchainDemandSupportChurn$/^active=128$'
  )
fi

cd "$checkout"
for pattern in "${patterns[@]}"; do
  go test ./internal/engine \
    -run '^$' \
    -bench "$pattern" \
    -benchmem \
    -benchtime "$benchtime" \
    -count "$count"
done
