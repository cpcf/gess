#!/usr/bin/env bash
set -uo pipefail

base=${1:?base benchmark file is required}
head=${2:?head benchmark file is required}
output=${3:-comparison.txt}
time_threshold=${TIME_REGRESSION_THRESHOLD_PERCENT:-15}
memory_threshold=${MEMORY_REGRESSION_THRESHOLD_PERCENT:-10}
failed=0

: > "$output"

compare_unit() {
  local unit=$1
  local threshold=$2
  local label=$3
  local result="${output}.${label}"

  if ! benchstat -filter ".unit:${unit}" "$base" "$head" | tee "$result" | awk -v threshold="$threshold" -v unit="$unit" '
    /%/ {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^\+[0-9.]+%$/) {
          value = $i
          gsub(/[+%]/, "", value)
          if (value + 0 > threshold) {
            bad = 1
            print unit " regression over " threshold "%: " $0
          }
        }
      }
    }
    END { exit bad }
  '; then
    failed=1
  fi
  cat "$result"
  cat "$result" >> "$output"
}

compare_unit sec/op "$time_threshold" sec-op
compare_unit B/op "$memory_threshold" bytes-op
compare_unit allocs/op "$memory_threshold" allocs-op

exit "$failed"
