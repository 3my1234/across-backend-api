#!/bin/sh
# Run in the existing k6 image, isolated network and tiny disposable /exchange.
set -eu
REAL_GZIP=$(command -v gzip)
export REAL_GZIP
mkdir -p /tmp/fake-k6
cat >/tmp/fake-k6/k6 <<'FAKE'
#!/bin/sh
[ "$1" = run ] && [ "$2" = --out ] && [ "$4" = /suite/sessions.js ] || exit 99
printf '%s\n' '{"metric":"offered_requests","data":{"value":1}}' '{"metric":"http_reqs","data":{"value":1}}' >"${3#json=}"
exit "${FAKE_K6_EXIT:-0}"
FAKE
cat >/tmp/fake-k6/gzip <<'FAKE'
#!/bin/sh
if [ "${FAKE_GZIP_EXIT:-0}" -ne 0 ]; then exit "$FAKE_GZIP_EXIT"; fi
exec "$REAL_GZIP" "$@"
FAKE
chmod +x /tmp/fake-k6/k6 /tmp/fake-k6/gzip
PATH=/tmp/fake-k6:$PATH
export PATH
/bin/sh /recorder.sh
"$REAL_GZIP" -dc /exchange/timeseries.json.gz >/exchange/decoded
printf '%s\n' '{"metric":"offered_requests","data":{"value":1}}' '{"metric":"http_reqs","data":{"value":1}}' >/exchange/expected
cmp /exchange/expected /exchange/decoded
test ! -e /exchange/timeseries.pipe
set +e
FAKE_K6_EXIT=7 /bin/sh /recorder.sh
status=$?
set -e
test "$status" -eq 7
test ! -e /exchange/timeseries.pipe
set +e
FAKE_GZIP_EXIT=9 /bin/sh /recorder.sh
status=$?
set -e
test "$status" -eq 9
printf '%s\n' 'Recorder passed: byte-exact metric retention, k6 failure propagation and compression failure rejection.'
