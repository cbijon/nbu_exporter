# Quick Start

## 1. Build

```bash
git clone https://github.com/fjacquet/nbu_exporter.git
cd nbu_exporter
make cli
```

## 2. Configure

The shipped `config.yaml` reads the NetBackup primary server and API key from the environment
(`nbuservers:` list, API version auto-detected). Put them in a gitignored `.env`, which the binary
loads from the working directory:

```bash
cp .env.example .env
# edit .env — set NBU1_HOSTNAME=master.my.domain and NBU1_APIKEY=...
```

Keep TLS verification on and trust the NetBackup CA (`SSL_CERT_FILE` / `SSL_CERT_DIR`, see
[Configuration](configuration.md#tls)); `NBU1_SKIP_CERTIFICATE=true` is a last resort. See
[Configuration](configuration.md) for all options.

## 3. Run

```bash
./bin/nbu_exporter --config config.yaml
```

The exporter exposes:

- **Metrics**: `http://localhost:9440/metrics` (latest snapshot; NetBackup is polled in the background every `collectionInterval`)
- **Health**: `http://localhost:9440/health` (always 200, JSON status)
- **Probes**: `http://localhost:9440/livez`, `http://localhost:9440/readyz` (static 200)

Check that every site is up and which API version it uses:

```bash
curl -s localhost:9440/health
curl -s localhost:9440/metrics | grep -E '^nbu_(up|api_version)'   # nbu_up 1 per site; version="14.0" on NBU 11.1
```

## 4. Add to Prometheus

```yaml
scrape_configs:
  - job_name: 'netbackup'
    static_configs:
      - targets: ['localhost:9440']
    scrape_interval: 60s
    scrape_timeout: 30s
```

## Command Line Options

```
Flags:
  -c, --config string   Path to configuration file (required)
  -d, --debug           Enable debug mode
  -h, --help            Help for nbu_exporter
      --trace           Log every NetBackup API response body (live-appliance
                        payload validation; bodies only, never headers; very verbose)
```

### Validating against a live appliance

`--trace` logs the method, URL, status, and body of every NetBackup API
response so payload shapes can be checked against a real master server.
Request and response headers are never logged (the `Authorization` API key
cannot leak), and credential-bearing endpoints (login/token/API-key) are
skipped entirely:

```bash
./bin/nbu_exporter --config config.yaml --trace
# bodies are logged on each background collection cycle (the first one runs at startup)
```
