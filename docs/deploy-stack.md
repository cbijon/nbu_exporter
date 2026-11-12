# Deploying the full monitoring stack

This guide deploys nbu_exporter, Prometheus, and Grafana as a single docker-compose stack with all dashboards and alerting rules pre-wired.

**Result:** 7 Grafana dashboards (Overview, Jobs, Storage, Data Protection, Tape, Lifecycle, Multi-site) + Prometheus alerting rules, all running in 3 containers.

---

## Prerequisites

| Requirement | Details |
|---|---|
| NetBackup version | 10.0 or later — tested against the 11.1/11.2 API (14.0). Tape/disk-pool metrics need 10.5+, alerts/malware/catalog/SLO need 11.1+ |
| NetBackup API key | One per master, generated in the NetBackup web UI → Security → API keys |
| Network access | Exporter host → NBU master on port 1556 |
| Docker + Compose | v2.x (`docker compose version`) |

---

## 1. Get the repo

```bash
git clone https://github.com/fjacquet/nbu_exporter.git
cd nbu_exporter
```

---

## 2. Generate a NetBackup API key

In the NetBackup Admin Console or web UI: **Security → API Keys → Add**.

The key's role needs read access to jobs and storage (plus tape/devices, catalog, alerts, malware and SLOs for the matching opt-in collectors). A collector the key cannot read is logged and skipped; it does not break the other metrics.

---

## 3. Configure

### Single site

The bundled `config.yaml` already reads the master from `.env`, so a basic setup
only needs:

```bash
cp .env.example .env
$EDITOR .env    # NBU1_HOSTNAME (FQDN), NBU1_APIKEY, keep NBU1_SKIP_CERTIFICATE=false (see §10)
```

Then enable the opt-in collectors you want in `config.yaml` (all are off by default):

```yaml
nbuservers:
  - site: "primary"            # becomes the `site` label; rename freely (short, stable)
    scheme: "https"
    uri: "/netbackup"
    host: "${NBU1_HOSTNAME}"
    port: "1556"
    apiKey: "${NBU1_APIKEY}"
    insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
    # apiVersion: leave unset — auto-detected (NetBackup 11.1/11.2 → 14.0)

collectors:
  tape:      { enabled: true }   # Tape & Disks dashboard (NBU 10.5+)
  alerts:    { enabled: true }   # Data Protection dashboard (NBU 11.1+)
  malware:   { enabled: true }
  catalog:   { enabled: true }
  slo:       { enabled: true }
  perClient:
    enabled: true                # Lifecycle dashboard + per-client alerts
    allowlist: ["srv-db-01", "srv-app-01"]   # exact client names; empty = nothing
```

Keep `server.host: "0.0.0.0"` in a container. To use the image published by a release
instead of building locally, set `NBU_IMAGE_OWNER` (the GitHub account whose release
workflow built it) and `NBU_TAG` in `.env` and use `docker-compose.ghcr.yml`.

### Two sites (multi-site)

The shipped `config.yaml` already contains a commented second `nbuservers:` entry wired
to `NBU2_*` variables. To add the second master after a single-site deployment:

1. Uncomment the `- site: "secondary"` block in `config.yaml` (rename the site if you
   like — each `site` value becomes a label on every metric; keep it short, e.g. `paris`).
2. Add to `.env`: `NBU2_HOSTNAME`, `NBU2_APIKEY` (an API key generated **on that
   master**) and `NBU2_SKIP_CERTIFICATE=false`; put that master's CA in `nbu-ca/` too (§10).
3. `docker compose up -d` — this recreates the exporter container with the new
   variables. A restart is required: sites are built once at startup, and a config
   reload (SIGHUP or file change) only logs that the server list changed.

`docker-compose.yml` forwards `NBU1_*` and `NBU2_*` into the container (the `.env` file
itself is not mounted). For a third master, add `NBU3_*` lines to the `environment:`
block as well. Each site detects its own API version, so masters on different
NetBackup releases can coexist; a master that is down only sets `nbu_up{site}=0` and the
other sites keep reporting.

Alternatively start from `docs/config-examples/config-multisite.yaml`.

---

## 4. Start the stack

```bash
docker compose up -d                              # builds the exporter image locally
# or: docker compose -f docker-compose.ghcr.yml up -d   # published image
```

This starts:
- **nbu_exporter** on port 9440
- **Prometheus** on port 9090
- **Grafana** on port 3000 (login: `admin` / `admin`)

---

## 5. Verify

```bash
# Exporter health
curl http://localhost:9440/health

# Metrics endpoint (should list ~20+ nbu_* metrics)
curl -s http://localhost:9440/metrics | grep "^nbu_" | cut -d'{' -f1 | sort -u

# Every master reachable: nbu_up 1 per site; on NetBackup 11.1/11.2 version="14.0"
curl -s http://localhost:9440/metrics | grep -E '^nbu_(up|api_version)'

# Version negotiation (one line per site) and any error/warning with its message (logs are pretty-printed JSON)
docker logs nbu_exporter 2>&1 | grep -A1 -E 'detected API version|"level": "(error|warning)"'

# Prometheus scraping (should return "1")
curl -s 'http://localhost:9090/api/v1/query?query=up{job="netbackup"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['result'][0]['value'][1])"

# Alert rules loaded: 4 groups, 17 rules
curl -s http://localhost:9090/api/v1/rules | python3 -c \
  "import sys,json; g=json.load(sys.stdin)['data']['groups']; print(len(g),'groups',sum(len(x['rules']) for x in g),'rules')"

# Opt-in collectors produce series (only those you enabled)
curl -s http://localhost:9440/metrics | grep -E '^nbu_(tape|disk_pool|client|alerts|malware|catalog|slo)_' | cut -d'{' -f1 | sort | uniq -c
```

If a check fails, run with `--trace` to log the raw API responses (bodies only — never
headers or the API key); stop it with Ctrl-C after the first collection cycle:

```bash
docker compose run --rm nbu_exporter --config /etc/nbu_exporter/config.yaml --trace
```

Open Grafana at `http://localhost:3000` → Dashboards → Browse → **NetBackup** folder.

---

## 6. Alerting rules

Prometheus loads four rule files automatically (mounted into `/etc/prometheus/rules/`
by docker-compose and listed under `rule_files:` in `prometheus.yml`):

| File | Alerts |
|---|---|
| `deploy/prometheus/nbu.rules.yml` | Core, per site: exporter down, API unreachable, metrics stale, backup success rate low, jobs queued, storage almost full / warning |
| `deploy/prometheus/rules-perclient.yml` | Per-client compliance: backup staleness, tape-copy / replication freshness, failure rate |
| `deploy/prometheus/rules-tape.yml` | Tape & disk health: drive down/disabled, scratch-pool low, disk-pool volume degraded |
| `deploy/prometheus/rules-multisite.yml` | Inter-site backup-volume divergence |

The per-client, tape, and multi-site rules reference the opt-in `nbu_client_*` and
`nbu_tape_*` / `nbu_disk_pool_*` metrics — enable the matching collectors (see §7) for
them to fire.

To send alerts, add an Alertmanager config to `prometheus.yml`:

```yaml
alerting:
  alertmanagers:
    - static_configs:
        - targets: ['alertmanager:9093']
```

Key alerts:

| Alert | Default threshold | Severity |
|---|---|---|
| `NbuClientBackupStale` | 25 h without successful backup | warning |
| `NbuClientBackupCritical` | 48 h without successful backup | critical |
| `NbuClientNoRecentTapeCopy` | 26 h without DUPLICATION | warning |
| `NbuClientNoRecentReplication` | 28 h without IMPORT | warning |
| `NbuTapeDriveDown` | ≥1 drive DOWN for 15 min | warning |
| `NbuTapeDriveDisabled` | ≥1 drive DISABLED for 30 min | warning |
| `NbuTapePoolScratchLow` | Scratch pool < 5 volumes | warning |
| `NbuDiskPoolVolumeDegraded` | disk-pool volume not UP for 10 min | warning |
| `NbuInterSiteDivergence` | Site < 30 % of cross-site average | warning |

---

## 7. Optional collectors

### Tape metrics (NBU 10.5+)

`collectors.tape.enabled: true`. Adds:
- `nbu_tape_drives_count` / `nbu_tape_drive_info` — drive status per robot / per drive
- `nbu_tape_media_count` — media inventory per pool
- `nbu_tape_pool_partially_full` — partially-full volumes per pool
- `nbu_disk_pool_volume_count` — disk pool volume states

The `NbuTapePoolScratchLow` alert watches a pool named `Scratch`; edit
`deploy/prometheus/rules-tape.yml` if your scratch pool has another name.

### Per-client lifecycle metrics

Enable to track per-client compliance (required for the Lifecycle and Multi-site dashboards to show per-client rows):

```yaml
collectors:
  perClient:
    enabled: true
    allowlist:         # exact client names — an empty list emits nothing
      - srv-db-01
      - srv-app-01
```

Adds:
- `nbu_client_jobs_count{site, client, action, status}` — job counts per client
- `nbu_client_last_job_success_seconds{site, client, policy, action}` — timestamp of last success

The last success per client and phase (BACKUP / DUPLICATION / IMPORT) is looked up in the
API every cycle, not only inside the scraping window, so it survives exporter restarts. A
client that has **never** had a DUPLICATION or IMPORT has no series for that phase, so the
"no recent tape copy / replication" alerts cannot flag it. Replication (IMPORT) jobs show
on the **target** master.

---

## 8. Dashboards

| Dashboard | What it shows |
|---|---|
| **Overview** | Health tiles per site, backup success rate, storage usage, alert summary |
| **Jobs** | Success rate, volume, duration p50/p95, dedup ratio, queued jobs |
| **Storage** | Capacity gauges per storage unit, used vs total |
| **Data Protection** | Alerts by severity, malware scans, catalog SLOs |
| **Tape & Disks** | Drive status, media inventory, pool levels, disk pool volumes |
| **Lifecycle** | Per-client compliance history (state timeline), BACKUP / DUPLICATION / IMPORT success rates, overdue clients |
| **Multi-site** | Cross-site backup volume, replication rate, divergence ratio, per-site comparison |

All dashboards have a `$site` variable (top left) to filter by site or show all at once.

---

## 9. Multi-site Prometheus setup

For two separate exporter instances (one per site), use a single Prometheus with two scrape targets:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'netbackup'
    scrape_interval: 60s
    scrape_timeout: 30s
    static_configs:
      - targets:
          - 'nbu-exporter-paris:9440'
          - 'nbu-exporter-lyon:9440'
```

Each exporter tags its metrics with `site` from its config. Prometheus aggregates them; Grafana filters with `$site`.

---

## 10. TLS: trust the NetBackup CA (keep certificate verification on)

The master's REST API (port 1556) presents a certificate signed by the NetBackup CA,
which is not in the system trust store. Instead of `insecureSkipVerify: true`, give the
exporter that CA — no config option needed, Go reads `SSL_CERT_DIR` on Linux:

```bash
mkdir -p nbu-ca
# Fetch the issuing CA from the master (last certificate of the chain it presents):
openssl s_client -connect master.my.domain:1556 -showcerts </dev/null 2>/dev/null \
  | awk '/BEGIN CERT/{c++; p=1} p{print > ("nbu-ca/cert" c ".pem")} /END CERT/{p=0}'
for f in nbu-ca/*.pem; do openssl x509 -in "$f" -noout -subject -issuer; done
```

Keep the certificate whose subject is the CA (subject = issuer, or the last one of the
chain) and delete the master's own leaf certificate. If the master only sends its leaf
(a single file whose issuer is not itself), get the NetBackup CA certificate from the
backup team / the primary server (NetBackup web service trust store, typically
`/usr/openv/var/webtruststore/cacert.pem`) and put it in `nbu-ca/`.

Then uncomment the two `nbu-ca` lines in `docker-compose.yml` (volume + `SSL_CERT_DIR`),
set `NBU1_SKIP_CERTIFICATE=false` and recreate: `docker compose up -d`. Both masters'
CAs can live in the same directory. The `host` you configure must match a name in the
master's certificate (use its FQDN, not an IP, unless the certificate lists the IP).

Verified: with the CA directory the exporter connects with verification enabled; without
it the collection fails with `x509: certificate signed by unknown authority`.

## 11. Production hardening

```yaml
# config.yaml — recommended for production
nbuservers:
  - site: "primary"
    # ...
    insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"   # keep false; trust the CA (§10)
```

```bash
# Change Grafana default password immediately
GF_ADMIN_PASSWORD=<strong-password> docker compose up -d
```

Consider:
- Running the stack behind a reverse proxy (nginx/Traefik) with TLS termination
- Storing the NBU API key in a secrets manager and injecting via env var
- Persisting Prometheus data with a named volume (already configured in docker-compose.yml)
- Setting `collectionInterval: "5m"` (default) and `scrapingInterval: "1h"` — don't scrape more often than NBU's job window

---

## Troubleshooting

### Exporter starts but no `nbu_*` metrics

```bash
docker compose logs nbu_exporter | grep -E "ERROR|version"
```

Common causes:
- Wrong API key → regenerate in NBU UI
- NBU API not reachable → check firewall on port 1556
- `x509: certificate signed by unknown authority` → trust the NetBackup CA (§10); `NBU1_SKIP_CERTIFICATE=true` only to confirm the diagnosis
- `HTTP 406` on every version → the master is older than 10.0, or a proxy rewrites the `Accept` header
- Config edited but nothing changed → changes are validated on save but applied only after `docker compose restart nbu_exporter`

### Tape metrics missing

```bash
curl -s http://localhost:9440/metrics | grep nbu_tape
```

Empty → `collectors.tape.enabled: false`, the exporter was not restarted after enabling it, the API key cannot read devices, or NBU < 10.5. The logs name the endpoint that was skipped.

### Grafana dashboards empty

1. Check Prometheus is scraping: `http://localhost:9090/targets`
2. Check the datasource in Grafana: Settings → Data Sources → Prometheus → Test
3. Query `nbu_up` in Explore — if no data, scraping isn't working

### Check-rules (optional)

```bash
# Validate alerting rules syntax (requires promtool in PATH)
make check-rules
```

---

## Related docs

- [Configuration reference](getting-started/configuration.md)
- [All metrics](metrics.md)
- [Docker usage](docker.md)
