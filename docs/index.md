# NBU Exporter

A production-ready Prometheus exporter that collects backup job statistics and storage metrics from Veritas NetBackup REST API, exposing them for monitoring and visualization in Grafana.

[![CI](https://github.com/fjacquet/nbu_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/fjacquet/nbu_exporter/actions/workflows/ci.yml)
[![Security](https://github.com/fjacquet/nbu_exporter/actions/workflows/security.yml/badge.svg)](https://github.com/fjacquet/nbu_exporter/actions/workflows/security.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/fjacquet/nbu_exporter)](https://goreportcard.com/report/github.com/fjacquet/nbu_exporter)

## Features

- **Job Metrics Collection** - Aggregates backup job statistics by type, policy, and status
- **Storage Monitoring** - Tracks storage unit capacity (free/used bytes) for disk-based storage
- **Tape & Disk Pools** (opt-in) - Drive status, media per pool, partially-full volume pools, disk-pool volume states
- **Per-client Lifecycle** (opt-in) - Last success and job counts per allowlisted client for BACKUP / DUPLICATION / IMPORT
- **Data Protection** (opt-in, NBU 11.1+) - Alerts, malware scans, catalog posture, SLOs
- **Dashboards & Alerts** - 7 provisioned Grafana dashboards and Prometheus alerting rules (docker-compose stack)
- **Multi-Site** - Scrape multiple NetBackup primary servers from one exporter, with every metric labelled by `site` (background snapshot collection)
- **Prometheus Integration** - Native Prometheus metrics exposition via HTTP endpoint
- **OpenTelemetry Tracing** - Optional distributed tracing for performance analysis and troubleshooting
- **Multi-Version API Support** - Automatic detection of NetBackup API versions (14.0, 13.0, 12.0, 10.0)
- **Health Checks** - Built-in `/health` endpoint for monitoring exporter status
- **Graceful Shutdown** - Proper signal handling with configurable shutdown timeout
- **Security** - Configurable TLS verification, API key masking in logs

## Quick Start

```bash
# Full stack (exporter + Prometheus + Grafana)
git clone https://github.com/fjacquet/nbu_exporter.git
cd nbu_exporter
cp .env.example .env && $EDITOR .env   # NBU1_HOSTNAME, NBU1_APIKEY (NBU2_* for a second master)
docker compose up -d

# Or the binary alone
make cli
./bin/nbu_exporter --config config.yaml
```

See [Deploying the full stack](deploy-stack.md) (setup, TLS, multi-site, post-deploy checks) or the
[Installation guide](getting-started/installation.md).

## Version Support Matrix

| NetBackup Version | API Version | Support Status |
|-------------------|-------------|----------------|
| 11.1 - 11.2       | 14.0        | Fully Supported |
| 11.0              | 13.0        | Fully Supported |
| 10.5              | 12.0        | Fully Supported |
| 10.0 - 10.4       | 10.0        | Fully Supported |

The exporter automatically detects, per site, the highest supported API version (14.0 → 13.0 → 12.0 → 10.0); leave `apiVersion` unset. Masters on different releases can be monitored together.

## Metrics at a Glance

| Metric | Description |
|--------|-------------|
| `nbu_up` | `1` per site when its last collection succeeded |
| `nbu_api_version` | Active NetBackup API version per site (`14.0` on NBU 11.1/11.2) |
| `nbu_jobs_count` | Number of jobs by action, policy type and status |
| `nbu_jobs_bytes` | Bytes transferred by jobs |
| `nbu_status_count` | Job count by action and exit status |
| `nbu_jobs_state_count` / `nbu_jobs_queued_count` | Running / queued jobs (live, not limited to the window) |
| `nbu_disk_bytes` | Storage unit capacity (free/used) |
| `nbu_tape_drives_count` | Tape drives by type, robot type and status (opt-in) |
| `nbu_client_last_job_success_seconds` | Last successful job per client and lifecycle phase (opt-in) |

See the [Metrics Reference](metrics.md) for full details.
