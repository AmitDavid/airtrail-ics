#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SUFFIX=$$
IMAGE="airtrail-ics:smoke"
NETWORK="airtrail-ics-smoke-${SUFFIX}"
VOLUME="airtrail-ics-smoke-${SUFFIX}"
MOCK="airtrail-ics-mock-${SUFFIX}"
APP="airtrail-ics-app-${SUFFIX}"
CADDY="airtrail-ics-caddy-${SUFFIX}"

cleanup() {
	docker rm -f "$CADDY" "$APP" "$MOCK" >/dev/null 2>&1 || true
	docker volume rm "$VOLUME" >/dev/null 2>&1 || true
	docker network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker build -t "$IMAGE" "$ROOT"
docker network create "$NETWORK" >/dev/null
docker volume create "$VOLUME" >/dev/null

docker run -d --name "$MOCK" \
	--network "$NETWORK" --network-alias mock-airtrail \
	-v "$ROOT/testdata/mock-airtrail.go:/mock-airtrail.go:ro" \
	golang:1.25-alpine go run /mock-airtrail.go >/dev/null

attempt=0
until docker exec "$MOCK" wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 30 ]; then
		echo "mock AirTrail did not become ready" >&2
		exit 1
	fi
	sleep 1
done

docker run -d --name "$APP" \
	--network "$NETWORK" --network-alias airtrail-ics \
	-v "$VOLUME:/data" \
	-e AIRTRAIL_URL=http://mock-airtrail:8080 \
	-e AIRTRAIL_API_KEY=smoke-key \
	-e 'SYNC_CRON=* * * * *' \
	-e SYNC_TIMEZONE=UTC \
	-e SYNC_RETRY_COUNT=1 \
	"$IMAGE" >/dev/null

docker run -d --name "$CADDY" \
	--network "$NETWORK" \
	-v "$ROOT/testdata/Caddyfile.smoke:/etc/caddy/Caddyfile:ro" \
	caddy:2.10.2-alpine >/dev/null

attempt=0
until docker exec "$APP" /airtrail-ics healthcheck >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 90 ]; then
		echo "application did not become ready after its retry" >&2
		docker logs "$APP" >&2
		exit 1
	fi
	sleep 1
done

attempt=0
while :; do
	FEED=$(docker exec "$CADDY" wget -qO- http://127.0.0.1:8081/test-token.ics 2>/dev/null || true)
	if printf '%s' "$FEED" | grep -q 'SEQUENCE:1' && \
		printf '%s' "$FEED" | grep -q 'SUMMARY:LHR -> CPH'; then
		break
	fi
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 90 ]; then
		echo "scheduled update did not reach the calendar" >&2
		docker logs "$APP" >&2
		exit 1
	fi
	sleep 1
done

printf '%s' "$FEED" | grep -q 'SUMMARY:CPH -> JFK'
printf '%s' "$FEED" | grep -q 'DTSTART:20260806T080000'
printf '%s' "$FEED" | grep -q 'DTEND:20260806T170000'
printf '%s' "$FEED" | grep -q 'SUMMARY:CPH -> TLL'
printf '%s' "$FEED" | grep -q 'DTSTART:20270110T130000'
printf '%s' "$FEED" | grep -q 'DTEND:20270110T160000'
if printf '%s' "$FEED" | grep -q 'SUMMARY:JFK -> CPH'; then
	echo "deleted event is still present" >&2
	exit 1
fi
if printf '%s' "$FEED" | grep -q 'SUMMARY:AAA -> BBB'; then
	echo "incomplete event was not skipped" >&2
	exit 1
fi

if docker exec "$CADDY" wget -qO- http://127.0.0.1:8081/wrong-token.ics >/dev/null 2>&1; then
	echo "Caddy accepted an incorrect token" >&2
	exit 1
fi

[ "$(docker inspect -f '{{.Config.User}}' "$APP")" = "65532:65532" ]
[ -z "$(docker port "$APP")" ]

LOGS=$(docker logs "$APP" 2>&1)
printf '%s' "$LOGS" | grep -q 'calendar synchronization retry scheduled'
printf '%s' "$LOGS" | grep -q 'added=2 updated=1 deleted=1'
printf '%s' "$LOGS" | grep -q 'flight skipped'

echo "smoke test passed"
