# Deployment Verification Guide

This guide covers checking a NetBackup exporter deployment end to end: pre-checks
against the primary (master) server, deployment, post-deploy checks, multi-site,
optional collectors, alert rules, rollback and troubleshooting.

For the one-page version see [deployment-quick-reference.md](deployment-quick-reference.md).
For the full Docker stack (exporter + Prometheus + Grafana) see [deploy-stack.md](deploy-stack.md).

## Supported versions

The exporter detects the NetBackup REST API version **per site** at startup. It probes
14.0 → 13.0 → 12.0 → 10.0 and keeps the first one the primary server accepts (HTTP 406
means "not supported, try the next one").

| API version | NetBackup |
|-------------|-----------|
| 14.0 | 11.1 / 11.2 |
| 13.0 | 11.0 |
| 12.0 | 10.5 |
| 10.0 | 10.0 – 10.4 |

Leave `apiVersion` unset. You can pin it per site, but a pin stops the exporter from
picking up a newer API after a NetBackup upgrade.

## 1. Pre-checks

Run these from the host (or container network) where the exporter will run.

```bash
# The primary server answers on 1556 (TCP + TLS)
nc -zv master.my.domain 1556
openssl s_client -connect master.my.domain:1556 -showcerts </dev/null | head -20

# The API key works and the server speaks API 14.0 (NetBackup 11.1+)
curl -s -H "Authorization: <key>" \
  -H "Accept: application/vnd.netbackup+json;version=14.0" \
  "https://master.my.domain:1556/netbackup/admin/jobs?page[limit]=1"
```

What the responses mean:

- **200 with a `data` array**: the key is valid and API 14.0 is available.
- **406**: the server doesn't support 14.0. Retry with `version=13.0`, `12.0`, then `10.0` to find out which NetBackup release you have. Auto-detection does the same thing.
- **401**: the API key is wrong, expired or revoked. Create a new one in the NetBackup web UI.
- **TLS error**: the NetBackup CA isn't trusted on this host. Add `--cacert nbu-ca.pem` to the test, and see [TLS](#tls) for the exporter.

## 2. Configuration

The main form is an `nbuservers:` list, with one entry per primary server:

```yaml
server:
  host: "0.0.0.0"          # must be 0.0.0.0 in a container; "localhost" only for a local bare-metal binary
  port: "9440"
  uri: "/metrics"
  scrapingInterval: "1h"   # job look-back window
  collectionInterval: "5m" # background poll; /metrics serves the latest snapshot
  logName: "stdout"        # or a file path whose parent directory exists

nbuservers:
  - site: "primary"        # required, unique; becomes the `site` label
    scheme: "https"
    uri: "/netbackup"
    host: "${NBU1_HOSTNAME}"
    port: "1556"
    apiKey: "${NBU1_APIKEY}"
    insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
    # apiVersion: omit to auto-detect
```

- `${VAR}` and `${VAR:-default}` work in `host`, `apiKey` and `insecureSkipVerify`. The binary also reads a `.env` file from the working directory or from next to `config.yaml`. Real environment variables take precedence.
- The legacy single `nbuserver:` block is still accepted. It becomes a one-entry list (the site name is the host) and logs a deprecation warning. Move to `nbuservers:`.
- More examples are in [config-examples/](config-examples/README.md).

### TLS

Keep certificate verification on and trust the NetBackup CA. Go reads `SSL_CERT_DIR` /
`SSL_CERT_FILE`. With Compose, put the CA PEM in `./nbu-ca/`, uncomment the
`./nbu-ca:/etc/nbu_exporter/ca:ro` volume, and set
`SSL_CERT_DIR=/etc/ssl/certs:/etc/nbu_exporter/ca` (see [deploy-stack.md §10](deploy-stack.md)).
Use `insecureSkipVerify: true` only as a last resort, and only on a trusted network.

## 3. Deploy

Before you upgrade, back up `config.yaml` (and `.env`), and write down the image tag or
binary you are running now so you can roll back.

### Docker Compose

```bash
docker compose up -d                              # builds the image locally
# or, from the published image:
docker compose -f docker-compose.ghcr.yml up -d   # ghcr.io/${NBU_IMAGE_OWNER:-fjacquet}/nbu_exporter:${NBU_TAG:-latest}
```

A fork sets `NBU_IMAGE_OWNER` to its own GitHub account. In production, pin `NBU_TAG` to
a release. Compose does **not** mount `.env` into the container, so every variable that
`config.yaml` references must be forwarded in the service `environment:` block (the
shipped files forward `NBU1_*` and `NBU2_*`).

### systemd (binary)

```bash
make build-release
sudo install -m 0755 bin/nbu_exporter /usr/local/bin/nbu_exporter
sudo install -m 0644 deploy/nbu_exporter.service /etc/systemd/system/
# API keys in /etc/nbu_exporter/nbu_exporter.env (0600, root:nbu); config in /etc/nbu_exporter/config.yaml
sudo systemctl daemon-reload && sudo systemctl enable --now nbu_exporter
```

See [systemd.md](systemd.md). With journald logging, set `logName: ""`.

### Helm

`charts/nbu-exporter` renders the `nbuservers:` format without a version pin.

## 4. Post-deploy checks

```bash
# Process health: always HTTP 200, JSON per site: {"sites":[{"site":"primary","ok":true,"last_scrape":"..."}]}
curl -s localhost:9440/health

# One nbu_up 1 per site; nbu_api_version shows the detected version (version="14.0" on NetBackup 11.1)
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'

# Detected version and any errors or warnings
docker logs nbu_exporter 2>&1 | grep -A1 -E 'detected API version|"level": "(error|warning)"'
# systemd: journalctl -u nbu_exporter | grep -A1 -E 'detected API version|"level": "(error|warning)"'

# Prometheus scrapes the exporter (value "1")
curl -s 'localhost:9090/api/v1/query?query=up{job="netbackup"}'

# Rules loaded: 4 groups, 17 rules
curl -s localhost:9090/api/v1/rules
```

Then confirm that data is flowing:

```bash
curl -s localhost:9440/metrics | grep -cE '^nbu_(jobs_count|status_count|disk_bytes)'   # > 0
```

- `/livez` and `/readyz` return a static 200. The Docker healthcheck calls `/livez`. These endpoints show that the process is up, not that NetBackup is reachable. To check NetBackup reachability, use `nbu_up{site}`.
- `/metrics` never calls NetBackup during a scrape. It serves the snapshot from the last `collectionInterval` poll, so after a restart data can take up to one poll to appear.
- Job metrics come from two queries: a windowed query (endTime within `scrapingInterval`) and a second query for jobs that are not yet `DONE` (running or queued).
- `nbu_response_time_ms{site}` is how long the storage-units request took.
- In Grafana, the dashboards have a `$site` variable (`label_values(nbu_up, site)`). Check that every site appears in it.

## 5. Multi-site checks

Each entry in `nbuservers:` is polled on its own and detects its own API version.

```bash
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'
# nbu_up{site="primary"} 1
# nbu_up{site="secondary"} 1
# nbu_api_version{site="primary",version="14.0"} 1
# nbu_api_version{site="secondary",version="13.0"} 1
```

- If a site is down, only its `nbu_up{site}` drops to 0. The other sites keep reporting.
- `/health` lists every site with its own `ok` and `err`.
- Site names must be unique. A duplicate name fails config validation at startup.

## 6. Optional collectors

All collectors are disabled by default. Enable them under `collectors:`, restart, then
check that their series appear:

| Collector | Requires | Check |
|-----------|----------|-------|
| `alerts` | API 14.0 (NBU 11.1+) | `nbu_alerts_count` |
| `malware` | API 14.0 | `nbu_malware_scan_count`, `nbu_malware_files_scanned` |
| `catalog` | API 14.0 | `nbu_catalog_images_count` |
| `slo` | API 14.0 | `nbu_slo_count` |
| `tape` | API 12.0+ (NBU 10.5+) | `nbu_tape_drives_count`, `nbu_tape_media_count`, `nbu_tape_pool_partially_full`, `nbu_disk_pool_volume_count` |
| `perClient` | — | `nbu_client_last_successful_backup_timestamp_seconds`, `nbu_client_last_job_success_seconds` |

```bash
curl -s localhost:9440/metrics | grep -E '^nbu_(alerts|malware|catalog|slo|tape|disk_pool|client)_' | cut -d'{' -f1 | sort | uniq -c
```

- `perClient` uses exact client names in `allowlist`. **An empty allowlist produces no per-client series**, and there is no "all clients" mode.
- On each poll, the exporter reads the last success per client and action (BACKUP, DUPLICATION, IMPORT) from the API, so the values survive restarts. A client that has never had a DUPLICATION or IMPORT has no series for that action.
- If the same series is emitted twice, the duplicate is dropped and a warning is logged (`dropped N duplicate metric series`). `/metrics` does not return a 500.

## 7. Alert rules

Both compose files mount four rule files from `deploy/prometheus/`: `nbu.rules.yml`,
`rules-perclient.yml`, `rules-tape.yml` and `rules-multisite.yml`, for 17 rules in total.
Validate and unit-test them before you deploy:

```bash
make check-rules    # promtool check rules + promtool test rules on all four files
```

`NbuTapePoolScratchLow` assumes a volume pool named `Scratch`. If your pool has a
different name, adjust the rule.

## 8. Config changes and reload

SIGHUP (`systemctl reload nbu_exporter`, `docker kill -s HUP nbu_exporter`) and file
changes make the exporter re-read and **validate** `config.yaml`, using the same secret
resolution as at startup. If an edit is invalid, the exporter rejects it, logs it, and
keeps running with the current config.

Validated changes are **not applied live**. Sites, collectors, intervals and the
allowlist take effect only after a **restart**. If the `nbuservers` list changed, the
exporter logs a RESTART warning.

```bash
docker compose restart nbu_exporter     # or: sudo systemctl restart nbu_exporter
```

## 9. Rollback

```bash
# Compose (published image): pin the previous release and recreate
NBU_TAG=<previous-tag> docker compose -f docker-compose.ghcr.yml up -d

# systemd: restore the previous binary and config
sudo install -m 0755 backups/nbu_exporter /usr/local/bin/nbu_exporter
sudo cp backups/config.yaml /etc/nbu_exporter/config.yaml
sudo systemctl restart nbu_exporter
```

After a rollback, run the [post-deploy checks](#4-post-deploy-checks) again.

## 10. Troubleshooting

| Symptom | Likely cause / fix |
|---------|--------------------|
| Exporter exits at startup | Config validation failed. The error names the field, for example `nbuservers[0] (primary): apiKey is required`. A `${VAR}` that isn't set resolves to empty, so check that the variable is forwarded to the container. |
| `nbu_up{site}` is 0 | The primary server is unreachable, the API key was rejected, or TLS failed. Check `/health` (`err`) and the logs for that site, and repeat the [pre-checks](#1-pre-checks). |
| TLS `x509: certificate signed by unknown authority` | Trust the NetBackup CA ([TLS](#tls)) rather than setting `insecureSkipVerify`. |
| Unexpected `nbu_api_version` | A pinned `apiVersion` is still in the config. Remove it to auto-detect. |
| Scrape works but no job series | There were no jobs in the `scrapingInterval` window, or the first poll hasn't finished yet. Keep `scrapingInterval >= collectionInterval`. |
| Container not reachable on 9440 | `server.host` is `localhost` inside the container. Set it to `0.0.0.0`. |
| Collector series missing | The collector isn't enabled, the exporter wasn't restarted after the change, the site's API version is below what the collector needs, or (`perClient`) the allowlist is empty. |
| Config edit has no effect | Reload only validates the config. Restart the exporter. |

To diagnose further, run the exporter in the foreground:

```bash
./bin/nbu_exporter -c config.yaml --debug    # verbose logs
./bin/nbu_exporter -c config.yaml --trace    # also logs API response bodies (never headers or the API key)
```

## Related documentation

- [deployment-quick-reference.md](deployment-quick-reference.md): one-page cheat sheet
- [deploy-stack.md](deploy-stack.md): full Compose stack, CA trust (§10)
- [systemd.md](systemd.md), [docker.md](docker.md)
- [metrics.md](metrics.md): metric reference
- [config-examples/](config-examples/README.md): example configurations
