# airtrail-ics

`airtrail-ics` turns flights from any current [AirTrail](https://airtrail.johan.ohly.dk/) instance into a dynamic, read-only iCalendar feed. It synchronizes in the background, preserves the last-known-good feed on disk, and requires no GUI.

## Calendar behavior

The service requests `GET /api/flight/list` with an AirTrail API key. A flight becomes an event when it has valid origin and destination IATA codes, valid airport IANA timezones, and one complete time pair.

The service prefers `departureScheduled` plus `landingScheduled` when both values exist. Otherwise, it uses the canonical `departure` plus `arrival` pair from AirTrail; it never mixes values from different pairs. Flights with missing or malformed required data are skipped and logged.

Event summaries are exactly `XXX -> YYY`. AirTrail stores timestamps as instants but displays them in each airport local timezone. The feed follows that display model: departure and arrival are converted to their respective airport-local wall clocks and emitted as floating iCalendar times, preventing the calendar account timezone from shifting them.

AirTrail flight IDs produce stable event UIDs. Changed events keep their UID and increment `SEQUENCE`; deleted flights disappear from the authoritative feed.

## Quick start

Requirements:

- Docker Engine with Compose v2;
- a current AirTrail version with the flight-list API.

Create an API key in AirTrail under **Settings -> Security**, then prepare the local deployment:

```console
cp .env.example .env
```

Set `AIRTRAIL_ICS_IMAGE`, `AIRTRAIL_URL`, and `AIRTRAIL_API_KEY` in `.env`, then start the service:

```console
docker compose pull airtrail-ics
docker compose up -d airtrail-ics
```

The default bind address is loopback-only, so a calendar application on the Docker host can subscribe without DNS:

```text
http://127.0.0.1:18080/calendar.ics
```

To use the feed from another device on a trusted local network, set `AIRTRAIL_ICS_BIND` to the host's LAN address. The application endpoint has no built-in authentication; do not expose it directly to the internet.

## Calendar applications

Use your calendar application's **subscribe from URL** feature and provide the local or reverse-proxied feed URL. Subscribed calendars are read-only, and refresh intervals are controlled by the calendar application.

## Connect to Caddy

Caddy is optional. Use it when the feed needs HTTPS, internet access, or a secret URL. The supplied Compose service and `Caddyfile` expose only:

```text
https://<CALENDAR_DOMAIN>/<CALENDAR_TOKEN>.ics
```

Set `CALENDAR_DOMAIN` and a token generated with `openssl rand -hex 32`, point the domain at the Docker host, and start the Caddy profile:

```console
docker compose --profile caddy up -d
```

The bundled Caddy service shares the private `backend` network with `airtrail-ics` and proxies to `airtrail-ics:8080`. To use an existing Caddy container instead, attach it to the same Docker network and add the route from the supplied `Caddyfile` to your existing configuration.

The secret URL is a capability credential: anyone who has it can read the flight calendar. Caddy accepts only the exact tokenized `.ics` path and returns `404` for everything else. Access logging is not enabled in the supplied Caddyfile, preventing the token from being written to normal access logs.

To revoke access, replace `CALENDAR_TOKEN`, restart Caddy, and update the calendar subscription. A single token cannot revoke only one recipient; use separate Caddy routes if individual revocation is required.

## Configuration

All configuration is supplied through Compose environment variables.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `AIRTRAIL_ICS_IMAGE` | yes | - | Published image to run, or your own compatible build. |
| `AIRTRAIL_ICS_BIND` | no | `127.0.0.1` | Host address for direct local access. |
| `AIRTRAIL_ICS_PORT` | no | `18080` | Host port for direct local access. |
| `AIRTRAIL_URL` | yes | - | AirTrail origin or base path, without `/api/flight/list`. |
| `AIRTRAIL_API_KEY` | yes | - | Bearer API key from AirTrail. |
| `AIRTRAIL_SCOPE` | no | `mine` | `mine`, `user`, or `all`; AirTrail restricts `user` and `all` to suitable roles. |
| `AIRTRAIL_USER_ID` | for `user` scope | empty | AirTrail user ID requested by `scope=user`. |
| `CALENDAR_NAME` | no | `AirTrail Flights` | Name embedded in the ICS feed. |
| `SYNC_CRON` | no | `0 */6 * * *` | Standard five-field cron schedule. Use `0 3 * * *` for nightly at 03:00. |
| `SYNC_TIMEZONE` | no | `UTC` | IANA timezone used to evaluate `SYNC_CRON`. |
| `SYNC_RETRY_COUNT` | no | `3` | Retries after the initial attempt, from 0 through 20. |
| `HTTP_TIMEOUT` | no | `30s` | Timeout for an AirTrail API request. |
| `CALENDAR_DOMAIN` | for Caddy | - | DNS name served by the optional Caddy profile. |
| `CALENDAR_TOKEN` | for Caddy | - | URL-safe 256-bit secret used in the Caddy path. |

Retry delays are built in: 1, 2, 4, and 8 minutes, then capped at 15 minutes. There is no retry-interval setting. When all attempts fail, the existing feed remains available. On a true first run with no cached state, the calendar endpoint returns `503` until a later cycle succeeds.

The default AirTrail scope is `mine`. Admin and owner keys may use `user` or `all`; refer to the [AirTrail list-flight API documentation](https://airtrail.johan.ohly.dk/docs/api/flight/list-flights).

## Operations

Useful commands:

```console
docker compose logs -f airtrail-ics
docker compose pull
docker compose up -d
docker compose ps
```

Calendar state is stored in the `airtrail-data` named volume. Back up that volume to retain event revision metadata and the last-known-good calendar across host migrations. The AirTrail API key is never written to the state file.

The application is non-root and has a read-only root filesystem. Direct access binds to loopback by default. When Caddy is enabled, it reaches the application over a private Docker network; a separate un-published egress network lets the application call AirTrail.

## Development

Build locally:

```console
docker build -t airtrail-ics:local .
```

Validate without installing Go locally:

```console
docker run --rm -v "$PWD:/src" -w /src golang:1.25-alpine sh -c 'gofmt -w . && go vet ./... && go build ./...'
AIRTRAIL_ICS_IMAGE=ghcr.io/example/airtrail-ics:latest \
AIRTRAIL_URL=https://airtrail.example.com \
AIRTRAIL_API_KEY=example \
CALENDAR_DOMAIN=calendar.example.com \
CALENDAR_TOKEN=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
docker compose config --quiet
```
