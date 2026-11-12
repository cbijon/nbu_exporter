# Trace Analysis Guide

This guide provides detailed instructions for querying and analyzing traces from the NBU Exporter to diagnose performance issues and optimize your monitoring setup.

## Table of Contents

- [Understanding Trace Structure](#understanding-trace-structure)
- [Common Queries](#common-queries)
- [Performance Analysis](#performance-analysis)
- [Troubleshooting Scenarios](#troubleshooting-scenarios)
- [Best Practices](#best-practices)

## Understanding Trace Structure

### Span Hierarchy

NetBackup is never called on a Prometheus scrape. A background loop polls every site on
`server.collectionInterval` (default `5m`) and publishes a snapshot; `/metrics` only serves that
snapshot. Traces therefore come in two families.

**Collection cycle** (per site, every `collectionInterval`). The fetches run in parallel and each
one is the root of its own trace:

```
netbackup.fetch_storage
└── http.request (GET /storage/storage-units)

netbackup.fetch_jobs
├── netbackup.fetch_job_page (first page, no cursor)
│   └── http.request (GET /admin/jobs?filter=endTime gt ...)
├── netbackup.fetch_job_page (page[after]=<cursor>)
│   └── http.request (GET /admin/jobs?page[after]=...)
├── ...
└── netbackup.fetch_live_jobs (filter state ne 'DONE')
    └── http.request (GET /admin/jobs?filter=state ne 'DONE')

subcollector.<name>          (one per enabled collector: alerts, tape, ...)
└── http.request ...
```

**Scrape** (every Prometheus scrape): a short `prometheus.scrape` span (kind server) with no
children — it only reads the latest snapshot.

**Startup**: `netbackup.detect_version`, one per site whose `apiVersion` is not pinned. It probes
14.0 → 13.0 → 12.0 → 10.0 and records a `version_attempt_failed` event for each rejected version.

Spans carry no `site` attribute: in a multi-site setup, tell sites apart by the host in `http.url`.

### Span Attributes

#### Storage Fetch (netbackup.fetch_storage)

| Attribute | Type | Description | Example |
|-----------|------|-------------|---------|
| `netbackup.endpoint` | string | API endpoint path | "/storage/storage-units" |
| `netbackup.storage_units` | int | Number of storage units retrieved | 6 |
| `netbackup.api_version` | string | API version used | "14.0" |

#### Job Fetch (netbackup.fetch_jobs)

| Attribute | Type | Description | Example |
|-----------|------|-------------|---------|
| `netbackup.endpoint` | string | API endpoint path | "/admin/jobs" |
| `netbackup.time_window` | string | Job look-back window | "1h" |
| `netbackup.start_time` | string | Window start (RFC 3339) | "2026-01-15T10:00:00Z" |
| `netbackup.total_jobs` | int | Total jobs retrieved | 1523 |
| `netbackup.total_pages` | int | Number of pages fetched | 16 |

#### HTTP Request (http.request)

| Attribute | Type | Description | Example |
|-----------|------|-------------|---------|
| `http.method` | string | HTTP method | "GET" |
| `http.url` | string | Full request URL | "<https://master.my.domain:1556/netbackup/admin/jobs>" |
| `http.status_code` | int | HTTP response status code | 200 |
| `http.request_content_length` | int | Request size in bytes | 0 |
| `http.response_content_length` | int | Response size in bytes | 48211 |
| `http.duration_ms` | float | Request duration in milliseconds | 2341 |

## Common Queries

### Jaeger UI Queries

#### Find All Job Collections

```
Service: nbu-exporter
Operation: netbackup.fetch_jobs
```

#### Find Slow Job Collections (> 30 seconds)

```
Service: nbu-exporter
Operation: netbackup.fetch_jobs
Min Duration: 30s
```

#### Find Failed Fetches

```
Service: nbu-exporter
Tags: error=true
```

#### Find Specific Time Range

```
Service: nbu-exporter
Lookback: Custom
Start: 2026-01-15 10:00
End: 2026-01-15 11:00
```

#### Find High Pagination

```
Service: nbu-exporter
Tags: netbackup.total_pages>10
```

### TraceQL Queries (Tempo/Grafana)

#### Find Slow API Calls

```traceql
{ span.http.duration_ms > 5000 }
```

#### Find Failed HTTP Requests

```traceql
{ span.http.status_code >= 400 }
```

#### Find Cycles with Many Jobs

```traceql
{ span.netbackup.total_jobs > 1000 }
```

#### Find Storage Fetch Issues

```traceql
{ name = "netbackup.fetch_storage" && duration > 10s }
```

#### Aggregate by Status Code

```traceql
{ span.http.status_code } | count() by (span.http.status_code)
```

## Performance Analysis

### Identifying Bottlenecks

#### Step 1: Find Slow Traces

1. Open Jaeger UI
2. Select service: `nbu-exporter`
3. Select operation `netbackup.fetch_jobs` (or `netbackup.fetch_storage`)
4. Set "Min Duration" to 30s and click "Find Traces"
5. Sort by duration (longest first)

#### Step 2: Analyze Span Timeline

Click on a slow trace and examine the waterfall view:

**Example 1: Slow Storage Fetch**

```
netbackup.fetch_storage: 30.1s ⚠️ BOTTLENECK
└── http.request: 30.0s
```

**Diagnosis**: Storage API is slow
**Action**: Check NetBackup server performance, verify network latency

**Example 2: High Pagination**

```
netbackup.fetch_jobs: 46.2s ⚠️ BOTTLENECK
├── netbackup.fetch_job_page: 15.4s
├── netbackup.fetch_job_page: 15.3s
├── netbackup.fetch_job_page: 15.5s
└── netbackup.fetch_live_jobs: 0.4s
```

**Diagnosis**: Too many job pages (high job volume)
**Action**: Reduce `scrapingInterval` (the job look-back window) from 1h to 30m — the effective
window is max(`scrapingInterval`, `collectionInterval`)

**Example 3: API Errors**

```
netbackup.fetch_jobs: 5.3s (error)
└── netbackup.fetch_job_page: 5.2s ⚠️ ERROR
    └── http.request: 5.2s (http.status_code=500)
```

**Diagnosis**: NetBackup API errors (pagination stops at the failing page)
**Action**: Check NetBackup server logs, verify API key permissions

#### Step 3: Examine Span Attributes

Click on a span to view its attributes:

**Key metrics to check:**

- `http.duration_ms`: Request latency
- `http.status_code`: Success/failure status
- `netbackup.total_pages`: Pagination count
- `netbackup.total_jobs`: Job volume

### Performance Metrics

#### Baseline Performance

**Normal cycle (< 1000 jobs, 6 storage units):**

```
netbackup.fetch_storage: 1-2s
netbackup.fetch_jobs: 6-10s
└── 1-10 pages @ 1-5s each
```

**High-volume cycle (> 5000 jobs):**

```
netbackup.fetch_storage: 1-2s
netbackup.fetch_jobs: 40-60s
└── 50+ pages @ 1-5s each
```

Storage and jobs run in parallel, so a site's cycle lasts about as long as its slowest fetch. A
cycle is bounded by a 2-minute timeout.

#### Performance Targets

| Metric | Target | Warning | Critical |
|--------|--------|---------|----------|
| Job collection (`netbackup.fetch_jobs`) | < 30s | 30-60s | > 60s |
| Storage fetch | < 5s | 5-10s | > 10s |
| Job page fetch | < 5s | 5-10s | > 10s |
| HTTP status codes | 200 | 4xx | 5xx |

## Troubleshooting Scenarios

### Scenario 1: Collections Timing Out

**Symptoms:**

- `nbu_up{site="..."}` drops to 0, or `nbu_last_scrape_timestamp_seconds` stops advancing
- Job metrics missing while storage metrics are present
- `netbackup.fetch_jobs` traces close to 2 minutes

Prometheus scrapes themselves do not time out: `/metrics` serves the snapshot without calling
NetBackup.

**Analysis:**

```
Query: Operation: netbackup.fetch_jobs, Min Duration: 60s
Look for: High netbackup.total_pages
```

**Solutions:**

1. Reduce `scrapingInterval` from 1h to 30m
2. Optimize NetBackup server performance
3. Check network latency to the master

### Scenario 2: Intermittent Failures

**Symptoms:**

- Some cycles succeed, others fail
- HTTP 500 errors in traces
- Spans tagged `error=true`

**Analysis:**

```
Query: Tags: error=true
Look for: http.status_code >= 500
```

**Solutions:**

1. Check NetBackup server logs for errors
2. Verify API key has correct permissions
3. Check network connectivity
4. Review NetBackup server resource usage

### Scenario 3: Slow Storage Fetch

**Symptoms:**

- `nbu_response_time_ms{site}` high (it measures the storage-units request)
- `netbackup.fetch_storage` > 10s

**Analysis:**

```
Query: Operation: netbackup.fetch_storage, Min Duration: 10s
Look for: http.duration_ms in child spans
```

**Solutions:**

1. Check NetBackup storage service status
2. Verify network latency to NetBackup server
3. Review NetBackup server disk I/O
4. Check for storage unit issues

### Scenario 4: High Pagination

**Symptoms:**

- Long `netbackup.fetch_jobs` durations
- Many `netbackup.fetch_job_page` spans
- `netbackup.total_pages` > 10

**Analysis:**

```
Query: Tags: netbackup.total_pages>10
Look for: Pattern of multiple page fetches
```

**Solutions:**

1. Reduce `scrapingInterval` to fetch fewer jobs
2. Optimize NetBackup job retention policies

### Scenario 5: API Version Issues

**Symptoms:**

- HTTP 406 errors
- Version detection failures at startup
- `nbu_api_version{site,version}` not the expected value (`version="14.0"` on NetBackup 11.1)

**Analysis:**

```
Query: Operation: netbackup.detect_version
Look for: version_attempt_failed events (failure_reason=version_not_supported = HTTP 406), netbackup.detected_version
```

**Solutions:**

1. Verify NetBackup version compatibility (14.0 = 11.1/11.2, 13.0 = 11.0, 12.0 = 10.5, 10.0 = 10.0–10.4)
2. Remove any `apiVersion` pin so the site auto-detects
3. Review NetBackup API logs

## Best Practices

### Query Optimization

**Use specific time ranges:**

```
Lookback: Last 1 hour (instead of Last 24 hours)
```

**Filter by operation:**

```
Operation: netbackup.fetch_jobs (instead of all operations)
```

**Use tags for precision:**

```
Tags: netbackup.api_version=14.0
```

### Sampling Strategy

Always set `samplingRate` explicitly: an omitted value is `0.0`, which samples nothing.

**Development:**

```yaml
samplingRate: 1.0  # Trace everything
```

**Production (normal load):**

```yaml
samplingRate: 0.1  # 10% sampling
```

**Production (high load):**

```yaml
samplingRate: 0.01  # 1% sampling
```

### Alerting on Traces

**Create alerts for:**

- Job collection duration > 60s
- HTTP status codes >= 500
- High pagination (> 10 pages)
- Failed fetches

**Example Prometheus alert** (from exporter metrics, not traces):

```yaml
- alert: NBUCollectionStale
  expr: time() - nbu_last_scrape_timestamp_seconds > 3 * 300
  annotations:
    summary: "NBU collection is stale"
    description: "{{ $labels.site }} has not been collected for {{ $value }}s"
```

### Regular Analysis

**Daily:**

- Check for failed fetches
- Review slow traces (> 30s)
- Monitor HTTP error rates

**Weekly:**

- Analyze pagination trends
- Review API version distribution
- Check sampling effectiveness

**Monthly:**

- Optimize scraping and collection intervals
- Review trace retention policies
- Update performance baselines

## Advanced Analysis

### Comparing Traces

**Compare before/after optimization:**

1. Find baseline trace (before changes)
2. Note key metrics (duration, pages, etc.)
3. Make configuration changes and restart the exporter (config edits are validated on reload but applied only on restart)
4. Find new trace (after changes)
5. Compare metrics side-by-side

**Example comparison:**

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| Total duration | 48.3s | 12.1s | 75% faster |
| Total pages | 15 | 3 | 80% reduction |
| Job count | 5234 | 1156 | Reduced scope |

### Correlation with Metrics

**Link traces to Prometheus metrics:**

1. Note trace timestamp
2. Query Prometheus for same time range
3. Correlate trace spans with metric values
4. Identify patterns

**Example:**

```
Trace shows: netbackup.total_jobs = 5234, netbackup.total_pages = 53
Calculation: 5234 / 53 ≈ 99 jobs per page (page size is 100)
```

### Exporting Trace Data

**Export for analysis:**

1. In Jaeger UI, click "JSON" on trace view
2. Save trace JSON
3. Analyze with custom tools
4. Generate reports

**Example Python analysis:**

```python
import json

with open('trace.json') as f:
    trace = json.load(f)

for span in trace['spans']:
    if span['operationName'] == 'http.request':
        duration = span['duration'] / 1000  # Convert to ms
        print(f"HTTP request: {duration}ms")
```

## Span Attributes Reference

### Complete Attribute List

| Span | Attribute | Type | Description |
|------|-----------|------|-------------|
| netbackup.fetch_storage | netbackup.endpoint | string | API endpoint |
| netbackup.fetch_storage | netbackup.storage_units | int | Storage units retrieved |
| netbackup.fetch_storage | netbackup.api_version | string | API version |
| netbackup.fetch_jobs | netbackup.endpoint | string | API endpoint |
| netbackup.fetch_jobs | netbackup.time_window | string | Job look-back window |
| netbackup.fetch_jobs | netbackup.start_time | string | Window start |
| netbackup.fetch_jobs | netbackup.total_jobs | int | Total jobs retrieved |
| netbackup.fetch_jobs | netbackup.total_pages | int | Pages fetched |
| netbackup.fetch_job_page | netbackup.jobs_in_page | int | Jobs in this page |
| netbackup.detect_version | netbackup.attempted_versions | string[] | Versions probed |
| netbackup.detect_version | netbackup.detected_version | string | Version selected |
| http.request | http.method | string | HTTP method |
| http.request | http.url | string | Request URL |
| http.request | http.status_code | int | Response status |
| http.request | http.request_content_length | int | Request size |
| http.request | http.response_content_length | int | Response size |
| http.request | http.duration_ms | float | Request duration |

## Further Reading

- [OpenTelemetry Tracing Specification](https://opentelemetry.io/docs/specs/otel/trace/)
- [Jaeger Query Documentation](https://www.jaegertracing.io/docs/latest/frontend-ui/)
- [TraceQL Documentation](https://grafana.com/docs/tempo/latest/traceql/)
- [Distributed Tracing Best Practices](https://opentelemetry.io/docs/concepts/observability-primer/#distributed-tracing)
