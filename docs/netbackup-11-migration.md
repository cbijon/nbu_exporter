# Upgrading NetBackup to 11.x

## Overview

This guide covers what to do on the exporter side when a NetBackup primary (master)
server is upgraded to 11.0, 11.1 or 11.2. With the recommended configuration (no
`apiVersion` pin) nothing has to change: the exporter picks up the new API version on
its next start.

| NetBackup version | API version | Notes |
|-------------------|-------------|-------|
| 11.1 / 11.2       | 14.0        | Enables the alerts / malware / catalog / SLO collectors |
| 11.0              | 13.0        | |
| 10.5              | 12.0        | Tape collector requires 12.0+ |
| 10.0 – 10.4       | 10.0        | |

### Automatic version detection

When `apiVersion` is not set, the exporter probes each site at startup in this order and
keeps the first version the master accepts (an unsupported version answers HTTP 406):

1. 14.0 (NetBackup 11.1 / 11.2)
2. 13.0 (NetBackup 11.0)
3. 12.0 (NetBackup 10.5)
4. 10.0 (NetBackup 10.0 – 10.4)

Detection runs **per site** and **only at startup**. After a NetBackup upgrade, restart
the exporter so it detects the new version.

## Recommended configuration

Use the `nbuservers:` list (one entry per master) and leave `apiVersion` unset:

```yaml
server:
  host: "0.0.0.0"          # "localhost" only for a bare-metal binary scraped locally
  port: "9440"
  uri: "/metrics"
  scrapingInterval: "1h"   # job look-back window
  collectionInterval: "5m" # background poll; /metrics serves the latest snapshot
  logName: "stdout"

nbuservers:
  - site: "primary"        # required, unique; becomes the `site` label
    scheme: "https"
    uri: "/netbackup"
    host: "${NBU1_HOSTNAME}"
    port: "1556"
    apiKey: "${NBU1_APIKEY}"
    insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
    # apiVersion: "14.0"   # optional pin; omit to auto-detect
```

`host`, `apiKey` and `insecureSkipVerify` accept `${VAR}` / `${VAR:-default}`; values can
come from a `.env` file next to `config.yaml` or in the working directory (real
environment variables win).

Keep certificate verification on and trust the NetBackup CA instead of setting
`insecureSkipVerify: true` — see [deploy-stack.md §10](deploy-stack.md#10-tls-trust-the-netbackup-ca-keep-certificate-verification-on).

### Migrating an older configuration

Older guides used a single `nbuserver:` block with an `apiVersion` pin. That block is still
accepted (it becomes a one-entry list with site `default` and logs a deprecation
warning), but convert it when you touch the file:

```yaml
# Before
nbuserver:
  host: "master.my.domain"
  apiVersion: "12.0"        # pin — blocks the move to 13.0/14.0
  apiKey: "..."
  ...

# After
nbuservers:
  - site: "primary"
    host: "master.my.domain"
    apiKey: "${NBU1_APIKEY}"
    ...                     # no apiVersion
```

Notes:

- **Remove the `apiVersion` pin.** A pin to 12.0 or 13.0 keeps working on 11.x (the
  older API versions remain available), but the exporter then never uses 14.0 and the
  14.0-only collectors do not work.
- `domain`, `domainType` and `contentType` are not needed: the `Accept` header is built
  from the API version in use.
- The `site` label changes from the master's hostname (what a legacy `nbuserver:` block
  uses as its site) to the name you choose. Dashboards filter on `$site`, but recording
  rules, alerts or saved queries that hard-code the old `site` value must be updated.

## Upgrade procedure

1. **Before the NetBackup upgrade**, note the current version per site:

   ```bash
   curl -s localhost:9440/metrics | grep '^nbu_api_version'
   ```

2. If `config.yaml` still pins `apiVersion` or uses `nbuserver:`, convert it as above.
3. Upgrade NetBackup on the master.
4. **Restart the exporter** (config changes and version detection only take effect on
   restart; a reload via SIGHUP or a file change only re-validates the file):

   ```bash
   docker compose restart nbu_exporter      # compose stack
   sudo systemctl restart nbu_exporter      # systemd
   kubectl rollout restart deployment/<name>   # Helm chart
   ```

5. Verify (next section).

## Verify after the upgrade

```bash
# Exporter health (always 200, JSON status per site)
curl -s localhost:9440/health

# One nbu_up 1 per site, and the detected API version per site
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'
# Expected on NetBackup 11.1 / 11.2:
#   nbu_api_version{site="primary",version="14.0"} 1
# On 11.0: version="13.0"

# Detection result and any errors/warnings
docker logs nbu_exporter 2>&1 | grep -A1 -E 'detected API version|"level": "(error|warning)"'

# Prometheus still scrapes the exporter
curl -s 'localhost:9090/api/v1/query?query=up{job="netbackup"}'
```

Then check Grafana:

- The **Overview** dashboard shows every site as up.
- **Jobs** and **Storage** panels have data for the upgraded site with no gap beyond the
  restart.
- With a 14.0 site and the new collectors enabled, the **Data Protection** dashboard
  fills in.

If a pin is still configured, the log shows
`Using explicitly configured API version: X (bypassing detection)` instead of a detected
version.

## New collectors on API 14.0 (NetBackup 11.1+)

API 14.0 adds endpoints used by four opt-in collectors (all disabled by default):

```yaml
collectors:
  alerts:  { enabled: true }   # nbu_alerts_count
  malware: { enabled: true }   # nbu_malware_* (scan results)
  catalog: { enabled: true }   # nbu_catalog_images_count
  slo:     { enabled: true }   # nbu_slo_count
```

- Restart the exporter after enabling them.
- The API key needs read access to those resources (alerts, malware, catalog, service
  catalog / SLOs).
- On a site still below 14.0 the endpoints are missing; the collector logs the failure
  and is skipped for that cycle. Core job and storage metrics are not affected.
- Metric details: [metrics.md](metrics.md). Dashboard: Data Protection
  ([dashboards.md](dashboards.md)).

The tape collector (`collectors.tape`) and per-client collector (`collectors.perClient`)
are independent of 11.x: tape needs API 12.0+ (NetBackup 10.5+); per-client emits
nothing until its `allowlist` lists exact client names.

## Multi-site with masters on different versions

Each `nbuservers` entry is polled independently and detects its own API version, so a
single exporter can monitor masters during a staggered upgrade:

```yaml
nbuservers:
  - site: "primary"          # already on 11.1  -> detects 14.0
    scheme: "https"
    uri: "/netbackup"
    host: "master.my.domain"
    port: "1556"
    apiKey: "${NBU1_APIKEY}"
  - site: "secondary"        # still on 10.5    -> detects 12.0
    scheme: "https"
    uri: "/netbackup"
    host: "master2.my.domain"
    port: "1556"
    apiKey: "${NBU2_APIKEY}"
```

- `nbu_api_version{site,version}` shows the version of each site.
- A site that is down (for example during its upgrade) only sets its `nbu_up{site}` to
  0; the other sites keep reporting.
- 14.0 collectors run on every site; on sites below 14.0 they log and skip.
- After upgrading the second master, restart the exporter so that site re-detects.

See [deploy-stack.md](deploy-stack.md) for the multi-site Prometheus and Grafana setup.

## Rollback

If the exporter misbehaves with the new API version, pin a lower version on the affected
site and restart:

```yaml
nbuservers:
  - site: "primary"
    host: "master.my.domain"
    apiVersion: "13.0"     # temporary; NetBackup 11.x still accepts 13.0 / 12.0
    ...
```

- The 14.0-only collectors do not work while the pin is in place.
- Remove the pin once the issue is fixed.
- Rolling back NetBackup itself needs no exporter change if no pin is set: restart the
  exporter and it detects the lower version.

To roll back the exporter itself, redeploy the previous image tag
(`NBU_TAG=<version> docker compose -f docker-compose.ghcr.yml up -d`) or binary, with the
`config.yaml` backup taken before the change.

## Troubleshooting

### Version detection fails

```
automatic API version detection failed: failed to detect compatible NetBackup API version
Attempted versions: [14.0 13.0 12.0 10.0]
```

Every version was rejected or the master could not be reached. Check, in order:

- Network / DNS / firewall from the exporter host to `host:1556`.
- TLS: `x509: certificate signed by unknown authority` means the NetBackup CA is not
  trusted ([deploy-stack.md §10](deploy-stack.md#10-tls-trust-the-netbackup-ca-keep-certificate-verification-on)).
- Authentication: `Authentication failed (HTTP 401)` means the API key is invalid or
  expired; generate a new one in the NetBackup web UI.
- The NetBackup version itself (`bpgetconfig -g | grep VERSION` on the master).

Test the API manually (trusting the CA with `--cacert` rather than `-k`):

```bash
curl --cacert nbu-ca/<netbackup-ca>.pem \
     -H "Authorization: $NBU1_APIKEY" \
     -H "Accept: application/vnd.netbackup+json;version=14.0" \
     "https://master.my.domain:1556/netbackup/admin/jobs?page[limit]=1"
```

HTTP 406 for `version=14.0` means the master is older than 11.1.

### HTTP 406 with a pinned version

The configured `apiVersion` is higher than the master supports (for example a 14.0 pin
on an 11.0 master). Remove the pin or lower it.

### Still on the old version after the upgrade

- The exporter was not restarted (detection runs only at startup), or
- `apiVersion` is still pinned on that site (look for
  `Using explicitly configured API version` in the logs).

### Metrics missing after the upgrade

- Check `nbu_up{site}` and the exporter logs for errors on that site.
- For the 14.0 collectors: confirm `nbu_api_version` shows `14.0` for the site and the
  API key permissions.
- Run once with `--debug`, or `--trace` to log API response bodies (headers and the API
  key are never logged).

## Related docs

- [Deploying the full monitoring stack](deploy-stack.md)
- [Metrics reference](metrics.md)
- [Dashboards](dashboards.md)
- [API 10.5 migration (historical)](api-10.5-migration.md)
- [Changelog](changelog.md)
