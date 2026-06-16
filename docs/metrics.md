# Metrics Reference

!!! info "The `site` label"
    **Every metric below also carries a `site` label** — its first label — identifying the
    NetBackup primary it came from. Single-site deployments have one `site` value; a
    `nbuservers:` list produces one per configured site. The per-metric tables list only the
    *additional* labels, so a `—` in the Labels column means the series carries `site` only.

## Job Metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `nbu_jobs_count` | `action`, `policy_type`, `status` | Number of jobs by type, policy, and status |
| `nbu_jobs_bytes` | `action`, `policy_type`, `status` | Total bytes transferred by jobs |
| `nbu_status_count` | `action`, `status` | Job count aggregated by action and status |
| `nbu_jobs_state_count` | `action`, `state` | Job count per lifecycle state (e.g. `ACTIVE`, `QUEUED`, `DONE`) |
| `nbu_jobs_queued_count` | `action`, `reason` | Queued job count per NetBackup queue reason code |
| `nbu_jobs_files_count` | `action`, `policy_type` | Total number of files processed by jobs |
| `nbu_jobs_dedup_ratio` | `action`, `policy_type` | Mean deduplication ratio across jobs (emitted only when jobs exist; suppressed on API v3.0) |
| `nbu_job_duration_seconds` | `action`, `policy_type` | Histogram of completed job durations in seconds |

The `nbu_job_duration_seconds` histogram covers completed jobs only (those with an
`EndTime` after `StartTime`). Bucket upper bounds (seconds): 60, 300, 900, 1800,
3600, 7200, 14400, 28800, 86400.

## Per-Client Metrics

These metrics track the backup lifecycle per client and policy across all job types
(BACKUP, DUPLICATION, IMPORT). They enable alerting on missed backups, tape copies, and
replication jobs independently.

| Metric | Labels | Description |
|--------|--------|-------------|
| `nbu_client_jobs_count` | `client`, `action`, `status` | Number of jobs per client, action type, and exit status |
| `nbu_client_last_job_success_seconds` | `client`, `policy`, `action` | Unix timestamp of the last successful (status=0) job completion per client, policy, and action |

`nbu_client_last_job_success_seconds` is persistent across scrape windows: once a client
has had a successful job of a given type, its timestamp is retained until a newer success
is recorded. The metric does not disappear between scrapes even if no jobs ran in the
current window.

Use `time() - nbu_client_last_job_success_seconds{action="BACKUP"}` to compute the age of
the last successful backup. Alert rules for 25h (BACKUP), 26h (DUPLICATION), and 28h
(IMPORT) thresholds are in `deploy/prometheus/nbu-lifecycle.rules.yml`.

## Multi-Site (Constant Labels)

When monitoring multiple NetBackup master servers, each exporter instance can be configured
with a `site` label via the `nbuservers[].site` field in `config.yaml`. This label is
attached as a **constant label** on every metric emitted by that instance, allowing
multi-site federation without label collisions:

```promql
nbu_jobs_bytes{site="site-a", action="BACKUP"}
nbu_jobs_bytes{site="site-b", action="BACKUP"}
```

See [Configuration](getting-started/configuration.md) for the `nbuservers` multi-site setup.

## Storage Metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `nbu_disk_bytes` | `name`, `type`, `size` | Storage unit capacity; `size` is `free` or `used` |
| `nbu_disk_capacity_bytes` | `name`, `type` | Authoritative total capacity reported by the API |
| `nbu_storage_max_concurrent_jobs` | `name`, `type` | Maximum concurrent jobs the unit accepts |
| `nbu_storage_max_fragment_size_bytes` | `name`, `type` | Maximum fragment size in bytes |
| `nbu_storage_info` | `name`, `type`, `subtype`, `is_cloud`, `worm_capable`, `use_worm`, `replication_capable`, `instant_access` | Storage unit capabilities (value always `1`) |

`nbu_disk_capacity_bytes` is a separate metric (not a `size="total"` label) so that
`sum(nbu_disk_bytes{name=X})` continues to equal total capacity (free + used).

!!! note
    Disk storage units only. Tape storage unit capacity is not available via the REST API;
    use the `nbu_tape_media_count` metric (API v12.0+) for tape inventory instead.

## Tape Storage Metrics (opt-in, requires API v12.0+)

These metrics require NetBackup 10.5 (API v12.0+) and are disabled by default.
Enable with `collectors.tape.enabled: true` in `config.yaml`. On API v3.0 (NBU 10.0–10.4)
a warning is logged at startup and the endpoints are not contacted.

| Metric | Labels | Description |
|--------|--------|-------------|
| `nbu_tape_drives_count` | `drive_type`, `robot_type`, `status` | Number of tape drives grouped by drive type (DT_HCART/DT_DLT/…), robot type (TLD/ACS/…), and status (DRIVE_STATUS_UP/DOWN/…) |
| `nbu_tape_media_count` | `pool`, `media_type`, `robot_type` | Number of tape media volumes grouped by volume pool, media type, and robot type |
| `nbu_tape_pool_partially_full` | `pool_name`, `pool_type` | Number of partially full media volumes in each tape volume pool |
| `nbu_disk_pool_volume_count` | `pool_name`, `storage_category`, `state` | Number of disk volumes per disk pool grouped by state (UP/DOWN/UNKNOWN); `storage_category` is ADVANCED_DISK/MSDP/CLOUD/CLOUD_CATALYST/OPEN_STORAGE |

!!! note
    Tape **job** information (DUPLICATION, VAULT actions) is already available via the
    existing `nbu_client_jobs_count` and `nbu_client_last_job_success_seconds` metrics,
    which work on all API versions including v3.0. The tape storage metrics above are
    for drive health and media inventory monitoring.

### Enabling the tape collector

```yaml
collectors:
  tape: { enabled: true }   # requires API v12.0+ (NBU 10.5+)
```

## NetBackup 11.2 opt-in collectors

These metrics require NetBackup 11.2 (API `version=14.0`) endpoints and are exposed
by four optional sub-collectors. **All default to disabled** — enable only the ones
your appliance and account permissions support. They are graceful: a failing
endpoint is logged and skipped without affecting core storage/jobs metrics or
flipping `nbu_up` to `0`.

| Metric | Type | Labels | Source endpoint | Source API attribute |
|--------|------|--------|-----------------|----------------------|
| `nbu_alerts_count` | Gauge | `severity`, `category` | `GET /manage/alerts` | Alerts grouped by `severity` + `category` |
| `nbu_malware_files_scanned` | Gauge | — | `GET /malware/latest-scan-results` | Sum of `numberOfFilesScanned` |
| `nbu_malware_files_infected` | Gauge | — | `GET /malware/latest-scan-results` | Sum of `numberOfFilesImpacted` |
| `nbu_malware_scan_count` | Gauge | `status` | `GET /malware/latest-scan-results` | Results grouped by `scanState` |
| `nbu_catalog_images_count` | Gauge | `malware_status`, `anomaly_status` | `GET /catalog/images` | `meta.pagination.count` per filter combination |
| `nbu_slo_count` | Gauge | — | `GET /servicecatalog/slos` | Total number of `data[]` entries |

Notes on the implemented attributes (these differ from early guesses):

- **Malware infected count** reads `numberOfFilesImpacted`, not
  `numberOfFilesInfected`. The 11.2 `latest-scan-results` response uses
  `numberOfFilesImpacted`.
- **Malware scan status** is grouped by `scanState` (enum values such as
  `SCAN_COMPLETED` / `SCAN_FAILED`), exposed via the `status` label.
- **Catalog posture** is collected with count-only queries (`page[limit]=1`,
  reading `meta.pagination.count`) issued once per curated combination of
  `malwareStatus` × `anomalyStatus`, keeping label cardinality bounded.
- **SLO count** is a single unlabeled gauge. The 11.2 SLO response has no
  per-SLO enforcement-type attribute, so the originally planned
  `enforcement_type` label was dropped.

### Enabling the collectors

Add a `collectors` block to your `config.yaml` (each collector is a
`{ enabled: false }` toggle; all default to disabled):

```yaml
collectors:
  alerts:  { enabled: false }
  malware: { enabled: false }
  catalog: { enabled: false }
  slo:     { enabled: false }
```

!!! note "Job metrics and the missing 11.2 `admin.yaml`"
    Job metrics (`GET /admin/jobs`) are validated against the NetBackup 11.0
    (`version=13.0`) spec because `admin.yaml` is absent from the local
    `docs/veritas-11.2/` bundle. The endpoint is backward-compatible under
    `version=14.0`, so the existing job metrics remain correct. Obtaining the
    11.2 `admin.yaml` is a follow-up item to confirm no new job attributes were
    added.

## System Metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `nbu_up` | — | `1` if any collection succeeded, `0` if all collections failed |
| `nbu_api_version` | `version` | Currently active NetBackup API version (14.0, 13.0, 12.0, or 10.0) |
| `nbu_response_time_ms` | — | NetBackup API response time in milliseconds |
| `nbu_last_scrape_timestamp_seconds` | `source` | Unix timestamp of the last successful collection (`source`: `storage` or `jobs`) |

## Label Encoding

Metrics use pipe-delimited keys internally, split into labels:

- **Storage**: `name|type|size` (e.g., "pool1|AdvancedDisk|free")
- **Jobs**: `action|policy_type|status` (e.g., "BACKUP|Standard|0")

The `site` label is prepended at emission time (it is not part of these internal keys), so every
emitted series is `site` followed by the labels above.

## Prometheus Configuration

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'netbackup'
    static_configs:
      - targets: ['localhost:9440']
    scrape_interval: 60s
    scrape_timeout: 30s
```

Alerting rules are provided in `grafana/alerts.yml` (load via `rule_files`).

## Dashboards

The Grafana dashboards are **generated** by `python3 grafana/build_dashboards.py`
(pure Python stdlib). Never hand-edit the JSON in `grafana/` — the generator is the
single source of truth and a metric-reference validator fails the build on any
unknown `nbu_*` name. Regenerate after changing the builders in `grafana/gen/`.

Four focused dashboards live in the `grafana/` directory:

| File | uid | Focus |
|------|-----|-------|
| `grafana/nbu-overview.json` | `nbu-overview` | One-screen health + headline KPIs |
| `grafana/nbu-jobs.json` | `nbu-jobs` | Backup outcomes, states, volume, queue, durations, dedup |
| `grafana/nbu-storage.json` | `nbu-storage` | Capacity utilization, storage units, limits |
| `grafana/nbu-dataprotection.json` | `nbu-dataprotection` | Alerts, malware scans, catalog posture, SLOs (11.2) |
| `grafana/nbu-lifecycle.json` | `nbu-lifecycle` | Per-client backup lifecycle: last success age, compliance gauges, failure rate (BACKUP / DUPLICATION / IMPORT) |
| `grafana/nbu-tape.json` | `nbu-tape` | Tape drives status, media inventory by pool/type, volume pool health, disk pool volume state (API v12.0+) |

The dashboards cross-link to each other via the shared `netbackup` tag (a tag-based
dashboard-links dropdown) and use the `${datasource}` template variable so they work
on any server. Jobs adds a `policy_type` variable and Storage adds a `storage_unit`
variable for per-unit / per-policy filtering.

The legacy "NBU Statistics" dashboard (the original 2021 export) was retired; its
views now live in the Storage and Jobs dashboards.

To import: load the JSON into Grafana and select your Prometheus datasource.
