# Deployment Quick Reference

Full guide: [deployment-verification.md](deployment-verification.md).

## Pre-checks

```bash
nc -zv master.my.domain 1556
curl -s -H "Authorization: <key>" \
  -H "Accept: application/vnd.netbackup+json;version=14.0" \
  "https://master.my.domain:1556/netbackup/admin/jobs?page[limit]=1"
# 200 = key OK + API 14.0 (NBU 11.1+) · 406 = older API, try 13.0/12.0/10.0 · 401 = bad key
```

API version per NetBackup release: 14.0 = 11.1/11.2 · 13.0 = 11.0 · 12.0 = 10.5 · 10.0 = 10.0–10.4.
The exporter detects it **per site**, so leave `apiVersion` unset.

## Minimal config

```yaml
server:
  host: "0.0.0.0"          # 0.0.0.0 in a container
  port: "9440"
  uri: "/metrics"
  scrapingInterval: "1h"
  collectionInterval: "5m"
  logName: "stdout"
nbuservers:
  - site: "primary"
    scheme: "https"
    uri: "/netbackup"
    host: "${NBU1_HOSTNAME}"
    port: "1556"
    apiKey: "${NBU1_APIKEY}"
    insecureSkipVerify: "${NBU1_SKIP_CERTIFICATE:-false}"
```

To trust the NetBackup CA, put the PEM in `./nbu-ca/`, uncomment the `nbu-ca` volume and
set `SSL_CERT_DIR=/etc/ssl/certs:/etc/nbu_exporter/ca` ([deploy-stack.md §10](deploy-stack.md)).

## Deploy

```bash
make check-rules                                   # validate + test the 4 rule files
docker compose up -d                               # local build
docker compose -f docker-compose.ghcr.yml up -d    # published image (NBU_IMAGE_OWNER / NBU_TAG)
sudo systemctl restart nbu_exporter                # systemd binary
```

## Verify

```bash
curl -s localhost:9440/health                                       # 200, per-site JSON
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'    # nbu_up 1 per site; version="14.0" on 11.1
docker logs nbu_exporter 2>&1 | grep -A1 -E 'detected API version|"level": "(error|warning)"'
curl -s 'localhost:9090/api/v1/query?query=up{job="netbackup"}'     # "1"
curl -s localhost:9090/api/v1/rules                                 # 4 groups, 17 rules
```

## Config changes

SIGHUP and file changes **validate** the config only. To apply a change, restart:
`docker compose restart nbu_exporter` or `sudo systemctl restart nbu_exporter`.

## Rollback

```bash
NBU_TAG=<previous-tag> docker compose -f docker-compose.ghcr.yml up -d
# systemd: restore the previous /usr/local/bin/nbu_exporter + config.yaml, then restart
```

## Common issues

| Symptom | Fix |
|---------|-----|
| Exits at startup | Read the validation error. Check that every `${VAR}` is forwarded to the container. |
| `nbu_up{site}` 0 | Check `/health` `err`, then run the pre-check curl against that site. |
| x509 unknown authority | Trust the NetBackup CA. Use `insecureSkipVerify` only as a last resort. |
| Not reachable on 9440 | Set `server.host: "0.0.0.0"`. |
| Collector metrics missing | Enable it under `collectors:` and restart. alerts/malware/catalog/slo need API 14.0; `perClient` needs a non-empty allowlist. |
| Need API payloads | `./bin/nbu_exporter -c config.yaml --trace` (logs bodies only, never headers). |
