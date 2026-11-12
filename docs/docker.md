# Docker Deployment

## Build Image

```bash
# Using Makefile
make docker

# Or manually
docker build -t nbu_exporter .
```

The Docker image uses a multi-stage build with Alpine Linux for minimal footprint.

## Run Container

```bash
# Using Makefile (port 9440) — uses the config.yaml baked into the image, which
# references ${NBU1_HOSTNAME} / ${NBU1_APIKEY}; prefer the manual form below
make run-docker

# Manual with custom configuration
docker run -d \
  --name nbu_exporter \
  -p 9440:9440 \
  -e NBU1_HOSTNAME=master.my.domain \
  -e NBU1_APIKEY=your-api-key \
  -v $(pwd)/config.yaml:/etc/nbu_exporter/config.yaml:ro \
  nbu_exporter
```

`config.yaml` must set `server.host: "0.0.0.0"` in a container — with `"localhost"` Prometheus
cannot reach the exporter, yet the `/livez` healthcheck still passes. The image logs to stdout
(`logName: "stdout"`); no log volume is needed.

## Docker Compose

```yaml
services:
  nbu_exporter:
    image: nbu_exporter:latest
    container_name: nbu_exporter
    ports:
      - "9440:9440"
    volumes:
      - ./config.yaml:/etc/nbu_exporter/config.yaml:ro
      # NetBackup CA (keep TLS verification on), see "TLS" below:
      # - ./nbu-ca:/etc/nbu_exporter/ca:ro
    environment:
      - NBU1_HOSTNAME=${NBU1_HOSTNAME}
      - NBU1_APIKEY=${NBU1_APIKEY}
      - NBU1_SKIP_CERTIFICATE=${NBU1_SKIP_CERTIFICATE:-false}
      # - SSL_CERT_DIR=/etc/ssl/certs:/etc/nbu_exporter/ca
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://127.0.0.1:9440/livez"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s
```

The exporter serves `/metrics`, `/health` (always 200, JSON status per site), and `/livez` /
`/readyz` (static 200). The healthcheck targets `/livez`, which never depends on NetBackup
reachability.

### TLS

Keep certificate verification on and trust the NetBackup CA: put its PEM in `./nbu-ca/`, uncomment
the `nbu-ca` volume and `SSL_CERT_DIR=/etc/ssl/certs:/etc/nbu_exporter/ca` (Go reads `SSL_CERT_DIR` /
`SSL_CERT_FILE`). Full procedure: [Deploy stack §10](deploy-stack.md). `insecureSkipVerify: true` is a
last resort, for a trusted network only.

## Docker Compose with OpenTelemetry

See the [OpenTelemetry Setup Guide](opentelemetry-example.md) for a complete Docker Compose stack with OpenTelemetry Collector and Jaeger.

## Quickstart demo stack (Docker Compose)

The repository ships a self-contained demo stack — the exporter plus Prometheus and Grafana — so you
can see metrics, alerts, and dashboards end to end with a single command.

### Start the stack

```bash
# Build the exporter locally, then start exporter + Prometheus + Grafana
docker compose up -d

# Or pull the published image instead of building
docker compose -f docker-compose.ghcr.yml up -d
```

The build variant uses `docker-compose.yml` and builds the exporter from the local source. The ghcr
variant uses `docker-compose.ghcr.yml`, which pulls
`ghcr.io/${NBU_IMAGE_OWNER:-fjacquet}/nbu_exporter:${NBU_TAG:-latest}`; the two files are otherwise
identical. A fork sets `NBU_IMAGE_OWNER` to its GitHub account; pin `NBU_TAG` to a release in
production.

### Service URLs

| Service | URL | Notes |
|---|---|---|
| Exporter metrics | <http://localhost:9440/metrics> | Scraped by the in-stack Prometheus |
| Prometheus | <http://localhost:9090> | Loads the alerting rules from `deploy/prometheus/` (4 files, 17 rules) |
| Grafana | <http://localhost:3000> | Default login `admin` / `admin` |

Override the Grafana credentials with the `GF_ADMIN_USER` and `GF_ADMIN_PASSWORD` environment
variables (for example in a gitignored `.env` file).

### Host ports

If a host port is already in use (for example another Prometheus on `9090`), override the published
port without editing the compose files:

```bash
NBU_EXPORTER_PORT=12112 PROMETHEUS_PORT=19090 GRAFANA_PORT=13000 docker compose up -d
```

The defaults are `9440` (exporter), `9090` (Prometheus), and `3000` (Grafana).

### Logs

The exporter logs to stdout (`logName: "stdout"` in `config.yaml`), so view logs with
`docker compose logs -f nbu_exporter`. No log directory or bind mount is required; set `logName` to a
file path in `config.yaml` if you also want a log file.

### Auto-provisioning

The Grafana datasource and the NetBackup dashboards are provisioned automatically — no manual import
is needed. See [Dashboards](dashboards.md) for what is included. Prometheus loads the alerting rules
from `deploy/prometheus/`: `nbu.rules.yml`, `rules-perclient.yml`, `rules-tape.yml` and
`rules-multisite.yml` (17 rules in total).

### Configuration

`config.yaml` is the source of truth for the exporter; it is mounted read-only into the container. For
the single-target quickstart, set `NBU1_HOSTNAME` and `NBU1_APIKEY` (for example in a gitignored
`.env` file) so the exporter knows which NetBackup master to query; set `NBU2_*` and uncomment the
second `nbuservers:` entry for a second site. The `.env` file is not mounted into the container —
compose forwards the `NBU1_*` / `NBU2_*` variables listed in its `environment:` section.

Editing the mounted `config.yaml` is validated on the fly, but changes are applied only after
`docker compose restart nbu_exporter`.

### Tear down

```bash
# Stop the stack and remove the named volumes (Prometheus + Grafana data)
docker compose down -v
```

## Verify Container

```bash
curl -s localhost:9440/health
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'   # nbu_up 1 per site; version="14.0" on NBU 11.1
docker logs nbu_exporter 2>&1 | grep -E 'detected API version|"level": "(error|warning)"'
curl -s 'localhost:9090/api/v1/query?query=up{job="netbackup"}'
curl -s localhost:9090/api/v1/rules   # 4 groups, 17 rules
```
