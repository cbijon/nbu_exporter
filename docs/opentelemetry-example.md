# OpenTelemetry Integration Example

This guide demonstrates how to run the NBU Exporter with OpenTelemetry distributed tracing using Docker Compose.

## Overview

The example stack includes:

- **NBU Exporter**: Collects NetBackup metrics with OpenTelemetry tracing enabled
- **OpenTelemetry Collector**: Receives and processes traces from the exporter
- **Jaeger**: Stores and visualizes distributed traces
- **Prometheus** (optional): Scrapes metrics from the exporter

## Prerequisites

- Docker and Docker Compose installed
- NetBackup server accessible from your Docker host
- Valid NetBackup API key

## Quick Start

### 1. Configure the Exporter

Edit `config.yaml` to enable OpenTelemetry:

```yaml
server:
    host: "0.0.0.0"            # required in a container
    port: "9440"
    uri: "/metrics"
    scrapingInterval: "1h"
    collectionInterval: "5m"
    logName: "stdout"

nbuservers:
    - site: "primary"
      scheme: "https"
      uri: "/netbackup"
      host: "master.my.domain"   # Your NetBackup primary server
      port: "1556"
      apiKey: "your-api-key-here"
      # apiVersion omitted: auto-detected (14.0 on NetBackup 11.1)

opentelemetry:
    enabled: true
    endpoint: "otel-collector:4317"
    insecure: true
    samplingRate: 1.0  # Trace every collection cycle (testing)
```

`docker-compose-otel.yaml` forwards `NBU1_*` / `NBU2_*` from `.env` like the main compose file;
a `${NBU1_HOSTNAME}` left unset makes the exporter fail at startup.

### 2. Start the Stack

```bash
# Start all services
docker compose -f docker-compose-otel.yaml up -d

# Check service status
docker compose -f docker-compose-otel.yaml ps

# View logs
docker compose -f docker-compose-otel.yaml logs -f nbu_exporter
```

### 3. Access the Services

- **NBU Exporter Metrics**: http://localhost:9440/metrics
- **NBU Exporter Health**: http://localhost:9440/health
- **Jaeger UI**: http://localhost:16686
- **Prometheus**: http://localhost:9090 (if enabled)
- **Collector Metrics**: http://localhost:8888/metrics
- **Collector Health**: http://localhost:13133

### 4. View Traces in Jaeger

1. Open Jaeger UI: http://localhost:16686
2. Select service: `nbu-exporter`
3. Click "Find Traces"
4. Click on a trace to view details

## Understanding the Trace Hierarchy

NetBackup is polled by a background loop every `collectionInterval`, never on scrape. Each fetch of
a collection cycle is the root of its own trace:

```
netbackup.fetch_storage
└── http.request (GET /storage/storage-units)

netbackup.fetch_jobs
├── netbackup.fetch_job_page (first page)
│   └── http.request (GET /admin/jobs?filter=endTime gt ...)
├── netbackup.fetch_job_page (page[after]=<cursor>)
│   └── http.request (GET /admin/jobs?page[after]=...)
└── netbackup.fetch_live_jobs (state ne 'DONE')
    └── http.request

subcollector.<name>   (one per enabled collector)
```

Each Prometheus scrape only produces a short `prometheus.scrape` span that reads the latest
snapshot. See the [Trace Analysis Guide](trace-analysis-guide.md) for details.

## Analyzing Traces

### Find Slow Collections

1. In Jaeger UI, go to "Search" tab
2. Select service: `nbu-exporter` and operation `netbackup.fetch_jobs`
3. Set "Min Duration" to filter slow traces (e.g., 30s)
4. Click "Find Traces"

### Identify Bottlenecks

1. Click on a slow trace
2. Examine the span timeline
3. Look for spans with long durations
4. Check span attributes for details:
   - `http.status_code`: HTTP response status
   - `http.duration_ms`: Request duration
   - `netbackup.total_pages`: Number of pages fetched
   - `netbackup.total_jobs`: Number of jobs retrieved

### Common Patterns

**Slow storage fetch:**
```
netbackup.fetch_storage: 15.2s
└── http.request: 15.1s (http.status_code=200)
```
**Diagnosis**: NetBackup storage API is slow. Check server performance.

**High pagination:**
```
netbackup.fetch_jobs: 45.3s
├── netbackup.fetch_job_page: 15.2s
├── netbackup.fetch_job_page: 15.1s
└── netbackup.fetch_job_page: 15.0s
```
**Diagnosis**: Many job pages. Consider reducing `scrapingInterval` (the job look-back window).

**API errors:**
```
http.request: 5.2s (http.status_code=500)
```
**Diagnosis**: NetBackup API error. Check span events for error details.

## Configuration Options

### Sampling Rates

Adjust `samplingRate` based on your needs. Always set it: an omitted value is `0.0`, which samples
nothing.

**Development (trace everything):**
```yaml
opentelemetry:
    samplingRate: 1.0  # 100% of traces
```

**Production (sample 10%):**
```yaml
opentelemetry:
    samplingRate: 0.1  # 10% of traces
```

**High-frequency (minimal overhead):**
```yaml
opentelemetry:
    samplingRate: 0.01  # 1% of traces
```

### Collector Configuration

The OpenTelemetry Collector can be customized in `otel-collector-config.yaml`:

**Add additional exporters:**
```yaml
exporters:
  otlp:
    endpoint: tempo:4317
    tls:
      insecure: true
```

**Adjust batch processing:**
```yaml
processors:
  batch:
    timeout: 5s
    send_batch_size: 512
```

**Change log level:**
```yaml
exporters:
  logging:
    loglevel: debug  # For troubleshooting
```

## Troubleshooting

### Traces Not Appearing

**Check exporter logs:**
```bash
docker compose -f docker-compose-otel.yaml logs nbu_exporter
```

**Look for:**
- `OpenTelemetry initialized successfully (endpoint: ..., sampling: ...)`
- `Successfully detected API version: 14.0` (on NetBackup 11.1)

**If you see:**
- `Failed to initialize OpenTelemetry: ... Continuing without tracing.`

**Solution:** Ensure the collector is running:
```bash
docker compose -f docker-compose-otel.yaml ps otel-collector
```

### Collector Connection Issues

**Check collector health:**
```bash
curl http://localhost:13133
```

**Check collector logs:**
```bash
docker compose -f docker-compose-otel.yaml logs otel-collector
```

**Verify network connectivity:**
```bash
docker compose -f docker-compose-otel.yaml exec nbu_exporter ping otel-collector
```

### Jaeger Not Receiving Traces

**Check Jaeger logs:**
```bash
docker compose -f docker-compose-otel.yaml logs jaeger
```

**Verify collector is exporting to Jaeger:**
```bash
docker compose -f docker-compose-otel.yaml logs otel-collector | grep jaeger
```

**Check collector metrics:**
```bash
curl http://localhost:8888/metrics | grep exporter
```

### High Memory Usage

**Reduce batch size in collector:**
```yaml
processors:
  batch:
    send_batch_size: 512  # Reduce from 1024
```

**Lower memory limit:**
```yaml
processors:
  memory_limiter:
    limit_mib: 256  # Reduce from 512
```

**Reduce sampling rate:**
```yaml
opentelemetry:
    samplingRate: 0.1  # Reduce from 1.0
```

## Production Deployment

### Security Considerations

**Enable TLS for OTLP:**
```yaml
opentelemetry:
    insecure: false  # Enable TLS
```

**Configure collector with TLS:**
```yaml
receivers:
  otlp:
    protocols:
      grpc:
        tls:
          cert_file: /etc/certs/server.crt
          key_file: /etc/certs/server.key
```

**Use secrets management:**

Don't commit API keys to version control. Reference them from `config.yaml`
(`apiKey: "${NBU1_APIKEY}"`) and forward the variable through the service's `environment:` list,
fed from a gitignored `.env` file or your secret store.

### Resource Limits

Add resource limits to prevent resource exhaustion:

```yaml
services:
  nbu_exporter:
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 256M
        reservations:
          cpus: '0.25'
          memory: 128M
```

### Monitoring

**Monitor collector health:**
```bash
# Add to your monitoring system
curl http://localhost:13133
```

**Monitor collector metrics:**
```bash
# Scrape with Prometheus
curl http://localhost:8888/metrics
```

**Key metrics to watch:**
- `otelcol_receiver_accepted_spans`: Spans received
- `otelcol_exporter_sent_spans`: Spans exported
- `otelcol_processor_batch_batch_send_size`: Batch sizes
- `otelcol_exporter_send_failed_spans`: Export failures

## Stopping the Stack

```bash
# Stop all services
docker compose -f docker-compose-otel.yaml down

# Stop and remove volumes
docker compose -f docker-compose-otel.yaml down -v
```

## Alternative Backends

### Grafana Tempo

Replace Jaeger with Tempo for long-term trace storage:

```yaml
services:
  tempo:
    image: grafana/tempo:latest
    command: ["-config.file=/etc/tempo.yaml"]
    volumes:
      - ./tempo.yaml:/etc/tempo.yaml
    ports:
      - "3200:3200"   # Tempo
      - "4317:4317"   # OTLP gRPC
```

Update collector config:
```yaml
exporters:
  otlp:
    endpoint: tempo:4317
    tls:
      insecure: true
```

### AWS X-Ray

Export traces to AWS X-Ray:

```yaml
exporters:
  awsxray:
    region: us-east-1
```

### Honeycomb

Export traces to Honeycomb:

```yaml
exporters:
  otlp:
    endpoint: api.honeycomb.io:443
    headers:
      x-honeycomb-team: YOUR_API_KEY
```

## Further Reading

- [OpenTelemetry Documentation](https://opentelemetry.io/docs/)
- [Jaeger Documentation](https://www.jaegertracing.io/docs/)
- [OTLP Specification](https://opentelemetry.io/docs/specs/otlp/)
- [Collector Configuration](https://opentelemetry.io/docs/collector/configuration/)
