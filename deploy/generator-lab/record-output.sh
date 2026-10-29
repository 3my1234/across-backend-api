#!/bin/sh
# Lossless recording stays INSIDE the generator CPU/memory budget.
set -eu
command -v gzip >/dev/null
command -v mkfifo >/dev/null
pipe=/exchange/timeseries.pipe
output=/exchange/timeseries.json.gz
test -d /exchange
rm -f "$pipe"
mkfifo "$pipe"
gzip -1 <"$pipe" >"$output" &
recorder=$!
set +e
k6 run --out "json=$pipe" /suite/sessions.js &
runner=$!
trap 'kill -TERM "$runner" "$recorder" 2>/dev/null; exit 143' TERM INT
wait "$runner"
run_status=$?
wait "$recorder"
record_status=$?
set -e
rm -f "$pipe"
if [ "$record_status" -ne 0 ]; then exit "$record_status"; fi
exit "$run_status"
