#!/bin/zsh
# svgpad-bench.sh — timed headless svgpad suite for cross-machine comparison.
#
# Runs a fixed set of prompts with pinned seeds through the one-shot mode and
# writes per-run wall times to svgpad-bench.<host>.csv in the repo root.
# Runs are serial (libds4 holds a single engine lock).
#
# Usage (from the repo root, after `task build:svgpad`):
#   ./scripts/svgpad-bench.sh                 # default model/backend
#   BACKEND=cuda ./scripts/svgpad-bench.sh    # e.g. on a DGX Spark
#   MODEL=v41-q2 ./scripts/svgpad-bench.sh    # any installed catalog alias
#   BENCH_VISION=1 ./scripts/svgpad-bench.sh  # adds a vision-review run
#                                             # (needs an installed vision model)
# Compare: paste the CSVs side by side; seeds are pinned so both machines do
# the same nominal work (backend float differences can still diverge output).
#
# Run on a quiet machine: residual GPU/unified memory from a prior engine or
# a resident ollama model (`ollama stop <model>`) inflates engine-open time
# or fails it outright with "ds4_engine_open failed with ds4 status 1".
set -u
REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${BIN:-$REPO/bin/ds4go-svgpad}"
BACKEND="${BACKEND:-auto}"
MODEL="${MODEL:-}"
HOST="$(hostname -s)"
OUT="$REPO/svgpad-bench.$HOST.csv"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

model_args=()
[ -n "$MODEL" ] && model_args=(--model "$MODEL")

run() {
  local name="$1" prompt="$2"; shift 2
  local start=$(date +%s)
  "$BIN" --prompt "$prompt" --outfile "$WORK/$name.svg" --backend "$BACKEND" \
    "${model_args[@]}" "$@" > "$WORK/$name.out" 2> "$WORK/$name.err"
  local code=$? end=$(date +%s)
  echo "$HOST,$BACKEND,${MODEL:-default},$name,$code,$((end-start))" | tee -a "$OUT"
}

echo "host,backend,model,run,exit,secs" | tee "$OUT"
run llama-s11    "draw a llama standing in a grassy field under a yellow sun" --seed 11 --visual-review off
run sailboat-s22 "draw a sailboat on the ocean at sunset"                     --seed 22 --visual-review off
run pelican-s33  "draw a pelican riding a bicycle"                            --seed 33 --visual-review off
if [ -n "${BENCH_VISION:-}" ]; then
  run pelican-vision "draw a pelican riding a bicycle" --seed 33 \
    --model "${VISION_MODEL:-vision-q2}" --visual-review on
fi
echo "results: $OUT"
