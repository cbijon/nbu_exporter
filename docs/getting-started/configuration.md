# Configuration

Create a `config.yaml` file (the repository ships a ready-to-use one at the root):

```yaml
server:
    host: "0.0.0.0"            # "localhost" only for a bare-metal binary scraped locally
    port: "9440"
    uri: "/metrics"
    scrapingInterval: "1h"     # job look-back window
    collectionInterval: "5m"   # background poll of every site (default 5m)
    logName: "stdout"          # or a file path whose parent directory exists

nbuservers:                    # one entry per NetBackup primary (master) server
    - site: "primary"          # required, unique; becomes the `site` label
      scheme: "https"
      uri: "/netbackup"
      host: "${NBU1_HOSTNAME}"
      port: "1556"
      apiKey: "${NBU1_APIKEY}"
      insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
      # apiVersion: "14.0"     # optional pin; omit to auto-detect (recommended)

# Optional: OpenTelemetry distributed tracing
# opentelemetry:
#     enabled: true
#     endpoint: "localhost:4317"
#     insecure: true
#     samplingRate: 0.1

# Optional: opt-in collectors (all disabled by default)
# collectors:
#     alerts:  { enabled: false }
#     malware: { enabled: false }
#     catalog: { enabled: false }
#     slo:     { enabled: false }
#     tape:    { enabled: false }
#     perClient: { enabled: false, allowlist: [] }
```

!!! warning
    Never commit API keys to version control. Use environment variables or secure secret management.

!!! note "Container deployments"
    In Docker or Kubernetes `server.host` **must** be `"0.0.0.0"`. With `"localhost"` the exporter
    binds the container loopback only: Prometheus cannot scrape it, yet the `/livez` healthcheck
    still passes.

## API version

Leave `apiVersion` unset. At startup each site probes 14.0 → 13.0 → 12.0 → 10.0 and keeps the
first version its master accepts (HTTP 406 means "not supported"). The version in use is exposed as
`nbu_api_version{site,version}`.

| API version | NetBackup |
|---|---|
| 14.0 | 11.1 / 11.2 |
| 13.0 | 11.0 |
| 12.0 | 10.5 |
| 10.0 | 10.0 – 10.4 |

Set `apiVersion` only to pin a site deliberately (for example while troubleshooting detection).

## Multiple Sites

To monitor more than one NetBackup primary server from a single exporter, add entries to the
`nbuservers:` list — each with a **required, unique `site`**. Every metric is labelled with its
`site`, and a background loop collects all sites on `server.collectionInterval` (default `5m`), so
backend API load is decoupled from Prometheus scrape frequency: `/metrics` serves the latest
snapshot and never calls NetBackup on scrape.

```yaml
nbuservers:
    - site: "primary"
      scheme: "https"
      uri: "/netbackup"
      host: "${NBU1_HOSTNAME}"       # e.g. master.my.domain
      port: "1556"
      apiKey: "${NBU1_APIKEY}"
      insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
    - site: "secondary"
      scheme: "https"
      uri: "/netbackup"
      host: "${NBU2_HOSTNAME}"       # e.g. master2.my.domain
      port: "1556"
      apiKey: "${NBU2_APIKEY}"
      insecureSkipVerify: "${NBU2_SKIP_CERTIFICATE:-false}"
```

Each site is polled independently and detects its own API version, so masters on different
NetBackup releases can coexist. An unreachable site reports only `nbu_up{site="..."}=0` and never
affects the others. See [`config-multisite.yaml`](../config-examples/config-multisite.yaml).

### Legacy `nbuserver:` block

For compatibility, a single `nbuserver:` block (same fields, no `site`) is still accepted: it is
mapped to a one-entry list whose `site` defaults to the host, and a deprecation warning is logged at
startup. Migrate to `nbuservers:`.

## Environment Variables / .env loading

The `host`, `apiKey` and `insecureSkipVerify` fields (in every `nbuservers:` entry, and in the
legacy `nbuserver:` block) support `${VAR}` and `${VAR:-default}` interpolation.
At startup the exporter expands every reference and fails loudly if a bare `${VAR}` is not set.

`insecureSkipVerify` accepts either a native boolean (`insecureSkipVerify: true`) or a string /
`${VAR}` reference (`insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"`), resolved at startup
alongside `host`/`apiKey`. This lets you toggle TLS verification per environment without editing
`config.yaml`.

`nbu_exporter` loads a `.env` file natively at startup — you do not need a shell wrapper or
`export` statements. It looks for `.env` in the working directory first, then in the same
directory as `config.yaml`. Already-set environment variables always win: values in `.env`
never shadow real env/secret injection.

Copy `.env.example` to `.env` and fill in your values; the shipped `config.yaml` already
references them:

```bash
cp .env.example .env
# edit .env — set NBU1_HOSTNAME and NBU1_APIKEY (NBU2_* for a second site)
```

With Docker Compose, the `.env` file is not mounted into the container: `docker-compose.yml`
forwards the `NBU1_*` / `NBU2_*` variables to the exporter. Any other variable you reference from
`config.yaml` must be added to its `environment:` list. Variable names are free; `NBU1_*`/`NBU2_*`
are only the shipped convention.

### API keys with special characters

The API key is sent verbatim as the value of the HTTP `Authorization` header — it is
never URL-encoded and never placed in a request body, so any character is safe end to
end. The only place quoting matters is **parsing at load time**, and it differs by where
you put the key:

| Source | Rule |
|---|---|
| `.env`, single-quoted `'…'` | Fully literal — no `$` expansion, no `\` escapes, no `#` comment. Best default. Cannot contain a literal `'`. |
| `.env`, double-quoted `"…"` | Expands `$VAR`/`${VAR}` and processes `\` escapes. `$`, `\`, `"` are special — write `\$`, `\\`, `\"`. |
| `.env`, unquoted | `$VAR` expands; a ` #` (space-hash) starts a comment; a value **starting** with `'`/`"` is treated as quoted. |
| `config.yaml` inline | Only the `${NAME}` and `${NAME:-default}` tokens are interpolated (`os.LookupEnv`), so a literal key containing either form is treated as an env ref. Prefer referencing an env var. |

For quotes inside the key specifically: use double quotes to include a `'`, single
quotes to include a `"`. NetBackup API keys are token strings (dot-separated,
base64url-style segments), so in practice they contain none of these characters and an
unquoted value works fine. When referencing an env var from `config.yaml`
(`apiKey: "${NBU1_APIKEY}"`) the value is inserted verbatim and never re-scanned, so the
env var itself may contain `$`, `${…}`, or any character.

## Fallback values: `${VAR:-default}`

A bare `${VAR}` **fails at startup** when the variable is unset — misconfiguration should
be loud rather than authenticate with an empty secret. Where a safe default exists, write
`${VAR:-default}` instead: the reference then never errors, falling back when the variable
is unset *or* empty, exactly as in the shell and in `docker-compose.yml`. That is why the
shipped `config.yaml` can be env-driven and still start out of the box:

```yaml
insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
```

`false` is this exporter's original shipped default, so a host that never exported
`NBU1_SKIP_CERTIFICATE` behaves exactly as before.

Use it for settings, not for secrets — a `${NBU1_APIKEY:-}` would silently turn a missing
password into an empty one.

## TLS

Keep certificate verification on and trust the NetBackup CA: Go reads `SSL_CERT_DIR` /
`SSL_CERT_FILE`. With Docker Compose, put the CA PEM in `./nbu-ca/`, uncomment the `nbu-ca` volume
(`./nbu-ca:/etc/nbu_exporter/ca:ro`) and `SSL_CERT_DIR=/etc/ssl/certs:/etc/nbu_exporter/ca`. The full
procedure is in [Deploy stack §10](../deploy-stack.md). `insecureSkipVerify: true` is a last resort,
for a trusted network only.

## Reloading the configuration

`SIGHUP` (`systemctl reload`) and file changes re-read **and validate** `config.yaml`, with the same
secret resolution as startup. An invalid edit is rejected and logged, and the running configuration
is kept. Changes are **not applied live**: sites, collectors, intervals and the per-client allowlist
take effect only after a **restart**. A changed `nbuservers` list logs a RESTART warning.

## Server Section

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `host` | string | Yes | Bind address (`0.0.0.0` in a container) |
| `port` | string | Yes | Server port (1-65535) |
| `uri` | string | Yes | Metrics endpoint path |
| `scrapingInterval` | duration | Yes | Job lookback window per collection (e.g., "1h", "30m") |
| `collectionInterval` | duration | No | Background poll interval for every site (default "5m"). Effective job window = max(scrapingInterval, collectionInterval). |
| `cacheTTL` | duration | No | Storage metrics cache TTL (default "5m") |
| `logName` | string | No | `"stdout"` (also `""` or `"-"`) for stdout only, or a log file path whose parent directory exists |

## NBU Servers Section

Each `nbuservers:` entry (the legacy `nbuserver:` block takes the same fields, without `site`):

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `site` | string | Yes | Unique site name; becomes the `site` label |
| `scheme` | string | Yes | Protocol (`http`/`https`) |
| `uri` | string | No | API base path (default `/netbackup`) |
| `host` | string | Yes | NetBackup primary server hostname (supports `${VAR}`) |
| `port` | string | Yes | API port (typically 1556) |
| `apiKey` | string | Yes | NetBackup API key (supports `${VAR}`) |
| `apiVersion` | string | No | Optional pin (14.0, 13.0, 12.0 or 10.0). Omit to auto-detect (recommended). |
| `insecureSkipVerify` | bool or `${VAR}` | No | Skip TLS certificate verification; defaults to `false`. Last resort — prefer trusting the NetBackup CA. |

## OpenTelemetry Section (Optional)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Enable OpenTelemetry tracing (default: false) |
| `endpoint` | string | Yes* | OTLP gRPC endpoint (e.g., "localhost:4317") |
| `insecure` | bool | No | Use insecure connection (default: false) |
| `samplingRate` | float | No | Sampling rate 0.0-1.0. Set it explicitly: an omitted value is `0.0`, which samples nothing. |

*Required when `enabled` is `true`

## Collectors Section (Optional)

All collectors are disabled by default. Each degrades gracefully: an endpoint the master lacks, or
one the API key may not read, is logged and skipped without failing the scrape.

| Collector | Requires | Description |
|---|---|---|
| `alerts`, `malware`, `catalog`, `slo` | API 14.0 (NetBackup 11.1+) | Data-protection metrics |
| `tape` | API 12.0+ (NetBackup 10.5+) | Tape drives, tape media, volume pools, disk pools |
| `perClient` | — | Per-client backup compliance and lifecycle. `allowlist` lists exact client names; an **empty allowlist emits no per-client series** (there is no "all clients" mode). |

See [Configuration Examples](../config-examples/README.md) for complete sample configs.
