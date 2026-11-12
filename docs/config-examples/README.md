# Configuration Examples

Example `config.yaml` files for common deployments. All of them use the `nbuservers:`
list and leave `apiVersion` unset, so each site detects its API version at startup.

| File | Use when |
|------|----------|
| `config-auto-detect.yaml` | **Recommended start.** One primary server on any supported release. All collectors are listed, disabled. |
| `config-multisite.yaml` | Two or more primary servers, each with its own `site` label. |
| `config-netbackup-11.yaml` | NetBackup 11.x. alerts/malware/catalog/slo/tape are enabled (the first four need 11.1+). |
| `config-netbackup-10.5.yaml` | NetBackup 10.5. The tape collector is enabled. |
| `config-netbackup-10.0.yaml` | NetBackup 10.0 – 10.4. Core job and storage metrics only. |

The release-specific files differ only in which collectors they enable. The connection
settings are the same in all of them.

## Quick start

```bash
cp docs/config-examples/config-auto-detect.yaml config.yaml
export NBU1_HOSTNAME=master.my.domain NBU1_APIKEY=<key>   # or put them in .env
./bin/nbu_exporter --config config.yaml
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'
```

## API version detection

With no `apiVersion`, the exporter tries API versions in descending order **for each
site** and keeps the first one the primary server accepts. HTTP 406 means "not
supported, try the next one".

| API version | NetBackup |
|-------------|-----------|
| 14.0 | 11.1 / 11.2 |
| 13.0 | 11.0 |
| 12.0 | 10.5 |
| 10.0 | 10.0 – 10.4 |

`nbu_api_version{site,version}` shows the version in use. Detection runs in the
background for each site, so an unreachable site never blocks startup. That site
reports `nbu_up{site}=0` until it answers.

`apiVersion` (one of `14.0`, `13.0`, `12.0`, `10.0`) is an optional per-site pin. Leave
it unset: a pin stops the exporter from picking up a newer API after a NetBackup upgrade.

## Field reference

### `server`

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `host` | Yes | — | Bind address. Must be `0.0.0.0` in a container. Use `localhost` only for a bare-metal binary scraped locally. |
| `port` | Yes | — | Listen port, usually `9440`. |
| `uri` | Yes | — | Metrics path, usually `/metrics`. |
| `scrapingInterval` | Yes | — | Job look-back window (for example `1h`). |
| `collectionInterval` | No | `5m` | How often the background loop polls every site. `/metrics` serves the latest snapshot and never calls NetBackup during a scrape. The effective job window is `max(scrapingInterval, collectionInterval)`. |
| `cacheTTL` | No | `5m` | Storage metrics cache TTL. |
| `logName` | No | — | `stdout`, or a file path whose parent directory exists. |

### `nbuservers[]`

One entry per primary (master) server.

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `site` | Yes | — | Unique name, emitted as the `site` label. |
| `scheme` | Yes | — | `https` (or `http`). |
| `uri` | No | `/netbackup` | API base path. |
| `host` | Yes | — | Primary server hostname. Accepts `${VAR}`. |
| `port` | Yes | — | API port, usually `1556`. |
| `apiKey` | Yes | — | API key from the NetBackup web UI. Accepts `${VAR}`. |
| `insecureSkipVerify` | No | `false` | Bool, or a string / `${VAR}` that resolves to a bool. Last resort only. |
| `apiVersion` | No | auto-detect | Optional pin. Leave unset. |

`${VAR}` and `${VAR:-default}` are expanded in `host`, `apiKey` and `insecureSkipVerify`.
The exporter also loads a `.env` file from the working directory or from next to
`config.yaml`. Real environment variables take precedence.

**Legacy:** a single `nbuserver:` block (with no `nbuservers:` list) is still accepted.
The exporter maps it to a one-entry list whose `site` is the host and logs a deprecation
warning. Replace it with an `nbuservers:` list.

### `collectors` (all disabled by default)

| Collector | Requires | Metrics |
|-----------|----------|---------|
| `alerts` | API 14.0 (NBU 11.1+) | `nbu_alerts_count` |
| `malware` | API 14.0 | `nbu_malware_*` |
| `catalog` | API 14.0 | `nbu_catalog_images_count` |
| `slo` | API 14.0 | `nbu_slo_count` |
| `tape` | API 12.0+ (NBU 10.5+) | drives, tape media, volume pools, disk-pool volumes |
| `perClient` | — | per-client last success and job counts for the clients in `allowlist` |

`perClient.allowlist` takes exact client names. **An empty allowlist produces no
per-client series.** There is no "all clients" mode.

### `opentelemetry` (optional)

```yaml
opentelemetry: { enabled: false, endpoint: "localhost:4317", insecure: true, samplingRate: 0.1 }
```

## Reload behaviour

SIGHUP or a change to the file makes the exporter re-read and **validate** the config.
If the new config is invalid, the exporter rejects it and keeps the running config. If
it is valid, nothing changes yet: sites, collectors, intervals and the allowlist take
effect on **restart**. If the `nbuservers` list changed, the exporter logs a RESTART
warning.

## Security

- Keep API keys out of version control. Reference them as `${NBU1_APIKEY}` and supply them through the environment, a `.env` file, or a systemd `EnvironmentFile`.
- Keep TLS verification on and trust the NetBackup CA through `SSL_CERT_DIR` / `SSL_CERT_FILE`. With Docker Compose, use the `nbu-ca` volume (see [deploy-stack.md §10](../deploy-stack.md)).

## Troubleshooting

| Error | Fix |
|-------|-----|
| `nbuservers[0] (primary): apiKey is required` | The `${VAR}` is unset or empty. Export it, or forward it into the container. |
| `nbuservers: duplicate site name` | Give every entry a unique `site`. |
| `unsupported API version` | Remove `apiVersion` to auto-detect, or use 14.0 / 13.0 / 12.0 / 10.0. |
| `nbu_up{site}` stays 0 | Run the API test from [deployment-verification.md](../deployment-verification.md#1-pre-checks). |

## See also

- [deployment-verification.md](../deployment-verification.md)
- [getting-started/configuration.md](../getting-started/configuration.md)
- [deploy-stack.md](../deploy-stack.md)
