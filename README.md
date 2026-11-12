# NBU Exporter — Prometheus Exporter for Veritas NetBackup

A Prometheus exporter that collects backup job statistics, storage metrics, tape health, and per-client lifecycle data from the Veritas NetBackup REST API.

[![CI](https://github.com/fjacquet/nbu_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/fjacquet/nbu_exporter/actions/workflows/ci.yml)
[![Security](https://github.com/fjacquet/nbu_exporter/actions/workflows/security.yml/badge.svg)](https://github.com/fjacquet/nbu_exporter/actions/workflows/security.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/fjacquet/nbu_exporter)](https://goreportcard.com/report/github.com/fjacquet/nbu_exporter)
![Go Version](https://img.shields.io/github/go-mod/go-version/fjacquet/nbu_exporter)
[![Latest Release](https://img.shields.io/github/v/release/fjacquet/nbu_exporter)](https://github.com/fjacquet/nbu_exporter/releases/latest)
[![License: GPL](https://img.shields.io/github/license/fjacquet/nbu_exporter)](LICENSE)
[![Documentation](https://img.shields.io/badge/docs-github%20pages-blue)](https://fjacquet.github.io/nbu_exporter/)

## Features

- **Job metrics** — count, volume, duration, dedup ratio; running and queued jobs (live, outside the window too); by action, policy type, and status
- **Storage metrics** — free/used bytes per storage unit, capacity, concurrent job limits
- **Tape & disk-pool metrics** — drive status per robot, media inventory per pool, disk pool volume states (opt-in; NetBackup 10.5+, tested against the 11.1/11.2 API spec)
- **Per-client lifecycle** — last successful job timestamp and job counts per client, for BACKUP / DUPLICATION / IMPORT phases (opt-in, explicit client allowlist; seeded from the API so it survives restarts)
- **Multi-site** — scrape several NetBackup primaries from one exporter, every metric labelled by `site`
- **Automatic API version detection** — probes 14.0 → 13.0 → 12.0 → 10.0 at startup; or pin explicitly
- **7 Grafana dashboards** — Overview, Jobs, Storage, Data Protection, Tape & Disks, Lifecycle, Multi-site
- **Prometheus alerting rules** — per-client compliance, tape health, inter-site divergence
- **Config validation on change** via SIGHUP / fsnotify file watcher (an invalid edit is rejected and logged; applying changes — sites, collectors, intervals — requires a restart)
- **Optional OpenTelemetry** distributed tracing (OTLP gRPC)
- **Health check** endpoint at `/health`

## Full observability stack (recommended)

Deploy the exporter, Prometheus, and Grafana with one command:

```bash
git clone https://github.com/fjacquet/nbu_exporter.git
cd nbu_exporter

# Set your NBU master hostname + API key in .env — the bundled config.yaml
# reads them via ${NBU1_HOSTNAME} / ${NBU1_APIKEY} (no need to edit config.yaml
# for a basic single-site setup; see Configuration below to customise).
cp .env.example .env
$EDITOR .env

docker compose up -d
```

Then open:
- **Grafana** — `http://localhost:3000` (admin / admin) → Dashboards → NetBackup
- **Prometheus** — `http://localhost:9090`
- **Metrics** — `http://localhost:9440/metrics`

See the [deployment checklist](#deployment-checklist-netbackup-11x) below and [docs/deploy-stack.md](docs/deploy-stack.md) for the full step-by-step guide (API key, TLS, multi-site, alerting, production hardening). Upgrading NetBackup? See [docs/netbackup-11-migration.md](docs/netbackup-11-migration.md).

## Deployment checklist (NetBackup 11.x)

### Setup

1. **API key** — NetBackup web UI → *Security → API keys*, on a user whose role can read jobs, storage, tape (and alerts / malware / catalog / SLO if those collectors are enabled). One key per master.
2. **`.env`** — `cp .env.example .env`, fill `NBU1_*` (and `NBU2_*` for a second master). For the published image set `NBU_IMAGE_OWNER` to the GitHub account whose release built it and pin `NBU_TAG` to a release.
3. **TLS** — keep `NBU1_SKIP_CERTIFICATE=false` and trust the NetBackup CA: put its PEM in `./nbu-ca/` and uncomment the `nbu-ca` volume and `SSL_CERT_DIR` in the compose file ([deploy-stack §10](docs/deploy-stack.md#10-tls-trust-the-netbackup-ca-keep-certificate-verification-on) has the `openssl` snippet).
4. **`config.yaml`** — enable the collectors you need (`tape`, `perClient` + `allowlist`, `alerts`, `malware`, `catalog`, `slo`). Uncomment the `secondary` entry for a second master.
5. **Start** — `docker compose up -d` (builds locally) or `docker compose -f docker-compose.ghcr.yml up -d` (published image).

> Config changes (sites, collectors, intervals) are **validated** on save/SIGHUP but only **applied on restart**: `docker compose restart nbu_exporter`. Adding a master logs a `RESTART` warning until you do.

### Post-deploy checks

```bash
# 1. Exporter alive and both masters reachable (nbu_up 1 per site)
curl -s localhost:9440/health
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'
#    expected on 11.1: nbu_api_version{site="primary",version="14.0"} 1

# 2. Version negotiation (one line per site) and errors/warnings with their message
docker logs nbu_exporter 2>&1 | grep -A1 -E 'detected API version|"level": "(error|warning)"'

# 3. Prometheus scrapes the exporter and loaded the 4 rule files (17 rules)
curl -s 'localhost:9090/api/v1/query?query=up{job="netbackup"}'
curl -s localhost:9090/api/v1/rules | python3 -c \
  "import sys,json; g=json.load(sys.stdin)['data']['groups']; print(len(g),'groups',sum(len(x['rules']) for x in g),'rules')"

# 4. Optional collectors produce series (only if enabled)
curl -s localhost:9440/metrics | grep -E '^nbu_(tape|client)_' | cut -d'{' -f1 | sort | uniq -c
```

Then in Grafana (**Dashboards → NetBackup**): the `$site` variable lists every master, *Overview* shows each site UP, and *Tape & Disks* / *Lifecycle* fill in when their collectors are on.

If a check fails, run with `--trace` (`docker compose run --rm nbu_exporter -c /etc/nbu_exporter/config.yaml --trace`, Ctrl-C after the first collection cycle) to log the raw API responses — it never logs headers or the API key — and see [Troubleshooting](docs/deploy-stack.md#troubleshooting). The `NbuTapePoolScratchLow` alert assumes a volume pool named `Scratch`; adjust `deploy/prometheus/rules-tape.yml` if yours differs.

## Configuration

```yaml
# config.yaml — minimal single-site example
server:
    host: "0.0.0.0"          # required in a container; "localhost" is unreachable for Prometheus
    port: "9440"
    uri: "/metrics"
    scrapingInterval: "1h"
    collectionInterval: "5m"
    logName: "stdout"

nbuservers:
    - site: "primary"        # becomes the `site` label on every metric
      scheme: "https"
      uri: "/netbackup"
      host: "${NBU1_HOSTNAME}"
      port: "1556"
      apiKey: "${NBU1_APIKEY}"   # generated from NBU web UI → API keys
      insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
      # apiVersion: leave unset — auto-detected (NetBackup 11.1/11.2 → 14.0)

collectors:
    tape:      { enabled: true }   # drives, media, volume pools, disk pools
    perClient:
        enabled: false             # opt-in; per-client compliance + lifecycle
        allowlist: []              # exact client names; empty = nothing emitted
```

For a second master, add another entry under `nbuservers:` (the bundled `config.yaml` has a commented `secondary` entry reading `NBU2_*`) — see [`docs/config-examples/config-multisite.yaml`](docs/config-examples/config-multisite.yaml). The legacy single `nbuserver:` block still works but logs a deprecation warning.

### Environment Variables

| Variable | Description |
|---|---|
| `NBU1_HOSTNAME` / `NBU1_APIKEY` | First master: FQDN and API key |
| `NBU1_SKIP_CERTIFICATE` | `true` disables TLS verification (avoid — trust the CA instead, see below) |
| `NBU2_HOSTNAME` / `NBU2_APIKEY` / `NBU2_SKIP_CERTIFICATE` | Second master (only read when its `nbuservers:` entry is uncommented) |
| `NBU_IMAGE_OWNER` / `NBU_TAG` | Image used by `docker-compose.ghcr.yml`: `ghcr.io/<owner>/nbu_exporter:<tag>` |
| `GF_ADMIN_USER` / `GF_ADMIN_PASSWORD` / `GF_SERVER_ROOT_URL` | Grafana login and public URL |

`config.yaml` supports `${VAR}` interpolation for secrets — never commit API keys.

## Dashboards

| Dashboard | What it shows |
|---|---|
| **Overview** | Site health tiles, backup success rate, storage usage, alert summary |
| **Jobs** | Success rate, volume, duration p50/p95, dedup ratio, queued jobs |
| **Storage** | Capacity gauges per storage unit |
| **Data Protection** | Alerts, malware scans, catalog SLOs |
| **Tape & Disks** | Drive status by robot, media inventory by pool, disk pool volumes |
| **Lifecycle** | Per-client compliance timeline, BACKUP/DUPLICATION/IMPORT success rates, overdue clients |
| **Multi-site** | Cross-site volume, replication rate, divergence ratio, per-site comparison |

All dashboards have a `$site` variable to filter by site or view all at once.

## Binary / CLI usage

```bash
# Build
make cli                    # outputs bin/nbu_exporter

# Run
./bin/nbu_exporter --config config.yaml
./bin/nbu_exporter --config config.yaml --debug   # verbose logging
./bin/nbu_exporter --config config.yaml --trace   # log every API response body

# macOS (Homebrew)
brew install fjacquet/tap/nbu_exporter
```

## Development

```bash
make tools         # install golangci-lint, govulncheck, cyclonedx-gomod (one-time)
make sure          # fmt + vet + test + build + lint
make ci            # full CI gate (fmt-check, vet, lint, test-race, govulncheck, coverage)
make test-coverage # tests with HTML coverage report
make docker        # build Docker image

# Regenerate Grafana dashboard JSONs after editing grafana/gen/*.py
PYTHONPATH=. python3 grafana/build_dashboards.py
```

## Documentation

Full docs at **[fjacquet.github.io/nbu_exporter](https://fjacquet.github.io/nbu_exporter/)**.

| Topic | Link |
|---|---|
| **Full stack deployment** | [docs/deploy-stack.md](docs/deploy-stack.md) |
| Installation | [Getting Started](https://fjacquet.github.io/nbu_exporter/getting-started/installation/) |
| Configuration | [Configuration Guide](https://fjacquet.github.io/nbu_exporter/getting-started/configuration/) |
| Metrics reference | [Metrics](https://fjacquet.github.io/nbu_exporter/metrics/) |
| Docker | [Docker](https://fjacquet.github.io/nbu_exporter/docker/) |
| OpenTelemetry | [Tracing Setup](https://fjacquet.github.io/nbu_exporter/opentelemetry-example/) |
| Changelog | [Changelog](https://fjacquet.github.io/nbu_exporter/changelog/) |

## Contributing

1. Fork the repository
2. Create a feature branch
3. Run `make ci` before submitting
4. Submit a pull request

## License

See [LICENSE](LICENSE) file for details.
