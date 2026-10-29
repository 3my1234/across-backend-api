#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true ]]
mkdir -p gateway-evidence
git rev-parse HEAD >gateway-evidence/source.txt
sha256sum deploy/capacity-lab/haproxy.cfg deploy/notification-lab/gateway-probe.* >gateway-evidence/source.sha256
GOOS=linux CGO_ENABLED=0 go build -o gateway-evidence/health-target deploy/notification-lab/gateway-probe.go
docker pull alpine:3.21
docker pull haproxy:3.0-alpine
docker pull grafana/k6:2.3.0
docker network create --internal atlxpres-gateway-probe >/dev/null
trap 'docker rm -f probe-api1 probe-api2 probe-gateway >/dev/null 2>&1 || true; docker network rm atlxpres-gateway-probe >/dev/null 2>&1 || true' EXIT
for slot in 1 2; do
 docker run -d --name "probe-api$slot" --network atlxpres-gateway-probe --network-alias "api$slot" --cpus .75 --memory 128m -v "$PWD/gateway-evidence/health-target:/health:ro" alpine:3.21 /health >/dev/null
done
docker run -d --name probe-gateway --network atlxpres-gateway-probe --cpus .25 --memory 128m -v "$PWD/deploy/capacity-lab/haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro" haproxy:3.0-alpine >/dev/null
# Wait for both backend health checks; this is outside every measured arm.
sleep 5
arm=0
for spec in direct:.25 proxy:.25 proxy:1 proxy:1 proxy:.25 direct:.25; do
 arm=$((arm+1));kind=${spec%:*};cpu=${spec#*:}
 docker update --cpus "$cpu" probe-gateway >/dev/null
 target=http://probe-gateway:8080
 if test "$kind" = direct; then target=http://api1:8080; fi
 prefix="arm-$arm-$kind-$cpu"
 docker inspect probe-api1 probe-api2 probe-gateway >"gateway-evidence/$prefix-limits.json"
 (
  while true; do date -u +%FT%TZ; docker stats --no-stream --format '{{json .}}'; done
 ) >"gateway-evidence/$prefix-resources.jsonl" &
 monitor=$!
 set +e
 docker run --rm --name probe-generator --network atlxpres-gateway-probe --cpus 2 --memory 4g --user 0:0 -e K6_NO_USAGE_REPORT=true -e TARGET="$target" -v "$PWD/deploy/notification-lab/gateway-probe.js:/probe.js:ro" -v "$PWD/gateway-evidence:/evidence" grafana/k6:2.3.0 run --summary-export "/evidence/$prefix-summary.json" /probe.js >"gateway-evidence/$prefix.log" 2>&1
 exitcode=$?
 set -e
 kill "$monitor" 2>/dev/null || true; wait "$monitor" 2>/dev/null || true
 echo "$exitcode" >"gateway-evidence/$prefix-exit.txt"
done
echo 'Synthetic constant health only; tests gateway independently, not application capacity or database latency.' >gateway-evidence/scope.txt
