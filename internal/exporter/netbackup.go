// Package exporter provides data fetching and processing logic for NetBackup API endpoints.
// It handles pagination, metric aggregation, and data transformation for Prometheus metrics.
package exporter

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/fjacquet/nbu_exporter/internal/models"
	"github.com/fjacquet/nbu_exporter/internal/telemetry"
	"github.com/fjacquet/nbu_exporter/internal/utils"
	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	pageLimit        = "100"                    // Default page size for API pagination
	jobPageLimit     = "100"                    // Maximum allowed by NetBackup API for jobs endpoint
	storageTypeTape  = "Tape"                   // Storage type identifier for tape units (excluded from metrics)
	storagePath      = "/storage/storage-units" // API endpoint for storage unit data
	jobsPath         = "/admin/jobs"            // API endpoint for job data
	sizeTypeFree     = "free"                   // Metric dimension for free capacity
	sizeTypeUsed     = "used"                   // Metric dimension for used capacity
	bytesPerKilobyte = 1024                     // Conversion factor from kilobytes to bytes
	bytesPerMegabyte = 1024 * 1024              // Conversion factor from megabytes to bytes
	jobStateQueued   = "QUEUED"                 // Job state indicating the job is waiting for a resource
	liveJobsFilter   = "state ne 'DONE'"        // OData filter for jobs still running/queued (no endTime yet)

	// Pre-allocation capacity hints for job metrics.
	// Typical environments have 20-100 unique job type/policy/status combinations:
	//   - Job types: ~5-10 (BACKUP, RESTORE, VERIFY, etc.)
	//   - Policy types: ~5-20
	//   - Status codes: ~10-20 common values
	// These values are intentionally generous to avoid reallocations in most cases.
	expectedJobMetricKeys    = 100 // Capacity hint for job size/count maps
	expectedStatusMetricKeys = 50  // Capacity hint for job status count map
)

// FetchStorage retrieves storage unit information from the NetBackup API and returns
// typed metric values. It fetches all storage units and extracts free and used capacity
// metrics for non-tape storage.
//
// Tape storage units are excluded from metrics as they don't report meaningful capacity values.
//
// Parameters:
//   - ctx: Context for request cancellation and timeout
//   - client: NetBackup API client
//
// Returns a slice of StorageMetricValue and an error if the API request fails.
//
// FetchStorage is a backward-compatible wrapper over FetchStorageFull that
// returns only the free/used capacity metrics. New callers that also need
// per-unit attributes (capacity, concurrency, info) should use FetchStorageFull.
func FetchStorage(ctx context.Context, client *NbuClient) ([]StorageMetricValue, error) {
	metrics, _, err := FetchStorageFull(ctx, client)
	return metrics, err
}

// FetchStorageFull retrieves storage unit information from the NetBackup API and
// returns both the free/used capacity metrics and the per-unit attributes
// (StorageUnitInfo) derived from the same response.
//
// Tape storage units are excluded as they don't report meaningful capacity values.
func FetchStorageFull(ctx context.Context, client *NbuClient) ([]StorageMetricValue, []StorageUnitInfo, error) {
	// Start child span "netbackup.fetch_storage" from parent context
	ctx, span := client.tracing.StartSpan(ctx, "netbackup.fetch_storage", trace.SpanKindClient)
	defer span.End()

	var storages models.Storages

	url := client.cfg.BuildURL(storagePath, map[string]string{
		QueryParamLimit:  pageLimit,
		QueryParamOffset: "0",
	})

	if err := client.FetchData(ctx, url, &storages); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, nil, fmt.Errorf("failed to fetch storage data: %w", err)
	}

	var metrics []StorageMetricValue
	var units []StorageUnitInfo
	for _, data := range storages.Data {
		// Skip tape storage units
		if data.Attributes.StorageType == storageTypeTape {
			continue
		}

		attr := data.Attributes
		stuName := attr.Name
		stuType := attr.StorageServerType

		metrics = append(metrics, StorageMetricValue{
			Key:   StorageMetricKey{Name: stuName, Type: stuType, Size: sizeTypeFree},
			Value: float64(attr.FreeCapacityBytes),
		})
		metrics = append(metrics, StorageMetricValue{
			Key:   StorageMetricKey{Name: stuName, Type: stuType, Size: sizeTypeUsed},
			Value: float64(attr.UsedCapacityBytes),
		})

		units = append(units, StorageUnitInfo{
			Name:               stuName,
			Type:               stuType,
			SubType:            attr.StorageSubType,
			TotalCapacityBytes: float64(attr.TotalCapacityBytes),
			MaxConcurrentJobs:  float64(maxConcurrency(attr.MaxConcurrentJobs, attr.MaxConcurrentDrives)),
			MaxFragmentBytes:   float64(attr.MaxFragmentSizeMegabytes) * bytesPerMegabyte,
			IsCloud:            attr.IsCloudSTU,
			WormCapable:        attr.WormCapable,
			UseWorm:            attr.UseWorm,
			ReplicationCapable: attr.ReplicationCapable,
			InstantAccess:      attr.InstantAccessEnabled,
		})
	}

	// Batch span attributes for storage operation
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrNetBackupEndpoint, storagePath),
		attribute.Int(telemetry.AttrNetBackupStorageUnits, len(storages.Data)),
		attribute.String(telemetry.AttrNetBackupAPIVersion, client.cfg.NbuServer.APIVersion),
	}
	span.SetAttributes(attrs...)

	log.Debugf("Fetched storage data for %d storage units", len(storages.Data))
	return metrics, units, nil
}

// maxConcurrency returns a storage unit's concurrency limit: maxConcurrentJobs for
// disk units, or maxConcurrentDrives for tape units (which have no maxConcurrentJobs).
func maxConcurrency(jobs, drives int) int {
	if jobs > 0 {
		return jobs
	}
	return drives
}

// aggregateJob folds a single job's attributes into every job-derived metric.
// Centralizing the per-job logic keeps FetchJobDetails focused on pagination.
func aggregateJob(agg *JobAggregator, attr models.JobAttributes) {
	status := strconv.Itoa(attr.Status)
	jobKey := JobMetricKey{Action: attr.JobType, PolicyType: attr.PolicyType, Status: status}
	statusKey := JobStatusKey{Action: attr.JobType, Status: status}
	policyKey := JobPolicyKey{Action: attr.JobType, PolicyType: attr.PolicyType}

	// Existing metrics: bytes, count, status count.
	agg.Count[jobKey]++
	agg.StatusCount[statusKey]++
	agg.Size[jobKey] += float64(attr.KilobytesTransferred * bytesPerKilobyte)

	// State breakdown (default empty state to "UNKNOWN" to avoid an empty label).
	state := attr.State
	if state == "" {
		state = "UNKNOWN"
	}
	agg.StateCount[JobStateKey{Action: attr.JobType, State: state}]++

	// Queued jobs by reason code.
	if state == jobStateQueued {
		reason := strconv.Itoa(attr.JobQueueReason)
		agg.QueuedCount[JobQueueKey{Action: attr.JobType, Reason: reason}]++
	}

	// Files transferred and dedup-ratio mean (per action/policy). Only jobs that report
	// a ratio contribute: tape duplications, imports and non-deduplicating storage
	// report 0, which would otherwise drag the MSDP mean toward zero.
	agg.FilesCount[policyKey] += float64(attr.NumberOfFiles)
	if attr.DedupRatio > 0 {
		agg.DedupSum[policyKey] += attr.DedupRatio
		agg.DedupCount[policyKey]++
	}

	// Duration histogram for completed jobs only (EndTime after StartTime).
	if !attr.EndTime.IsZero() && attr.EndTime.After(attr.StartTime) {
		agg.observeDuration(policyKey, attr.EndTime.Sub(attr.StartTime).Seconds())
	}

	// Per-client lifecycle (opt-in; only allowlisted clients are folded in, so the
	// maps and the persistent last-success cache stay bounded by the allowlist).
	if agg.trackClient(attr.ClientName) {
		agg.ClientCount[ClientJobKey{Client: attr.ClientName, Action: attr.JobType, Status: status}]++
		if attr.Status == 0 && !attr.EndTime.IsZero() {
			csKey := ClientSuccessKey{Client: attr.ClientName, Policy: attr.PolicyName, Action: attr.JobType}
			ts := float64(attr.EndTime.Unix())
			if existing, ok := agg.ClientLastSuccess[csKey]; !ok || ts > existing {
				agg.ClientLastSuccess[csKey] = ts
			}
		}
	}
}

// aggregateLiveJob folds a not-yet-finished job (ACTIVE, QUEUED, ...) into the
// point-in-time state and queue breakdowns only. Volume, count, status and duration
// metrics stay scoped to jobs that finished inside the scraping window.
func aggregateLiveJob(agg *JobAggregator, attr models.JobAttributes) {
	state := attr.State
	if state == "" {
		state = "UNKNOWN"
	}
	agg.StateCount[JobStateKey{Action: attr.JobType, State: state}]++
	if state == jobStateQueued {
		reason := strconv.Itoa(attr.JobQueueReason)
		agg.QueuedCount[JobQueueKey{Action: attr.JobType, Reason: reason}]++
	}
}

// lifecycleActions are the job types whose last success the lifecycle dashboard and
// alerts track per client: primary backup, tape copy (SLP duplication) and inter-site
// replication (AIR import).
var lifecycleActions = []string{"BACKUP", "DUPLICATION", "IMPORT"}

// SeedClientLastSuccess queries, for every allowlisted client and lifecycle action, the
// most recent successful job (status 0, newest endTime) and folds it into
// agg.ClientLastSuccess. Without it the last-success timestamps only come from jobs
// that finished inside the scraping window: after a restart, a client whose last tape
// copy is older than the window has no series at all, so it shows neither compliant
// nor overdue and NbuClientNoRecentTapeCopy / NbuClientNoRecentReplication can never
// fire for it. Lookup failures are logged per client and skipped.
func SeedClientLastSuccess(ctx context.Context, client *NbuClient, agg *JobAggregator, allowlist []string) {
	seen := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		if _, dup := seen[name]; dup || !agg.trackClient(name) {
			continue
		}
		seen[name] = struct{}{}
		for _, action := range lifecycleActions {
			if ctx.Err() != nil {
				return
			}
			url := client.cfg.BuildURL(jobsPath, map[string]string{
				QueryParamFilter: fmt.Sprintf("clientName eq '%s' and jobType eq '%s' and status eq 0",
					odataQuoteString(name), action),
				QueryParamSort:  "-endTime",
				QueryParamLimit: "1",
			})
			var jobs models.Jobs
			if err := client.FetchData(ctx, url, &jobs); err != nil {
				log.WithError(err).WithField("client", name).WithField("action", action).
					Warn("perClient: last-success lookup failed; skipping")
				continue
			}
			if len(jobs.Data) == 0 || jobs.Data[0].Attributes.EndTime.IsZero() {
				continue // no successful job of this kind on record
			}
			a := jobs.Data[0].Attributes
			key := ClientSuccessKey{Client: name, Policy: a.PolicyName, Action: action}
			ts := float64(a.EndTime.Unix())
			if existing, ok := agg.ClientLastSuccess[key]; !ok || ts > existing {
				agg.ClientLastSuccess[key] = ts
			}
		}
	}
}

// FetchLiveJobs pages through every job that is not DONE (filter state ne 'DONE') and
// folds it with aggregateLiveJob. The windowed endTime filter used by FetchJobDetails
// can never match these jobs — they have no endTime yet — so without this pass the
// ACTIVE/QUEUED state counts and nbu_jobs_queued_count would always be empty.
func FetchLiveJobs(ctx context.Context, client *NbuClient, agg *JobAggregator) error {
	ctx, span := client.tracing.StartSpan(ctx, "netbackup.fetch_live_jobs", trace.SpanKindClient)
	defer span.End()

	err := HandlePagination(ctx, func(ctx context.Context, cursor string) (string, error) {
		params := map[string]string{
			QueryParamLimit:  jobPageLimit,
			QueryParamSort:   "jobId",
			QueryParamFilter: liveJobsFilter,
		}
		if cursor != "" {
			params[QueryParamAfter] = cursor
		}
		var jobs models.Jobs
		if err := client.FetchData(ctx, client.cfg.BuildURL(jobsPath, params), &jobs); err != nil {
			return "", fmt.Errorf("failed to fetch live jobs: %w", err)
		}
		for _, job := range jobs.Data {
			aggregateLiveJob(agg, job.Attributes)
		}
		if len(jobs.Data) == 0 {
			return "", nil
		}
		return jobs.Meta.Pagination.Next, nil // HandlePagination stops on an echoed cursor
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	span.SetStatus(codes.Ok, "live jobs fetched")
	return nil
}

// FetchJobDetails retrieves a page of job records (up to 100) at the given cursor and
// folds each job into the aggregator. /admin/jobs is cursor-paginated (NetBackup API
// >= 9.0): it processes all jobs in the response and returns the next-page cursor from
// the API's pagination metadata.
//
// Parameters:
//   - ctx: Context for request cancellation and timeout
//   - client: Configured NetBackup API client
//   - agg: Aggregator accumulating all job-derived metrics across pages
//   - cursor: Page cursor (empty string for the first page; pass Meta.Pagination.Next for subsequent pages)
//   - startTime: Filter to only include jobs completed after this time
//
// Returns:
//   - Next-page cursor (empty string when there are no more jobs)
//   - Error if the API request fails
func FetchJobDetails(
	ctx context.Context,
	client *NbuClient,
	agg *JobAggregator,
	cursor string,
	startTime time.Time,
) (string, error) {
	// Start child span "netbackup.fetch_job_page" from parent context
	ctx, span := client.tracing.StartSpan(ctx, "netbackup.fetch_job_page", trace.SpanKindClient)
	defer span.End()

	var jobs models.Jobs

	queryParams := map[string]string{
		QueryParamLimit:  jobPageLimit, // Fetch up to 100 jobs per page for efficiency
		QueryParamSort:   "jobId",
		QueryParamFilter: fmt.Sprintf("endTime gt %s", utils.ConvertTimeToNBUDate(startTime)),
	}
	// Cursor pagination: omit page[after] on the first page; follow the response's
	// next cursor thereafter (NetBackup API >= 9.0 returns opaque string cursors).
	if cursor != "" {
		queryParams[QueryParamAfter] = cursor
	}

	url := client.cfg.BuildURL(jobsPath, queryParams)

	if err := client.FetchData(ctx, url, &jobs); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("failed to fetch job details: %w", err)
	}

	span.SetAttributes(attribute.Int(telemetry.AttrNetBackupJobsInPage, len(jobs.Data)))

	// No more jobs to process
	if len(jobs.Data) == 0 {
		span.SetStatus(codes.Ok, "No more jobs to process")
		return "", nil
	}

	// Process ALL jobs in the batch response
	for _, job := range jobs.Data {
		aggregateJob(agg, job.Attributes)
	}

	// An empty next cursor signals the last page.
	span.SetStatus(codes.Ok, "Page fetched successfully")
	return jobs.Meta.Pagination.Next, nil
}

// HandlePagination drives cursor-based pagination: it calls fetchFunc with the current
// cursor (empty on the first page) and follows the returned next cursor until it is empty.
// Context cancellation is checked before each page fetch.
//
// The fetch function should:
//   - Accept a context and the current cursor (empty string for the first page)
//   - Return the next cursor (empty string when there are no more pages)
//   - Return an error if the fetch operation fails
//
// Returns an error if:
//   - Context is cancelled (ctx.Err())
//   - The fetch function returns an error
//
// Example:
//
//	err := HandlePagination(ctx, func(ctx context.Context, cursor string) (string, error) {
//	    return FetchJobDetails(ctx, client, agg, cursor, startTime)
//	})
func HandlePagination(ctx context.Context, fetchFunc func(ctx context.Context, cursor string) (string, error)) error {
	cursor := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			next, err := fetchFunc(ctx, cursor)
			if err != nil {
				return err
			}
			if next == "" {
				return nil
			}
			if next == cursor {
				// A backend that echoes the cursor would otherwise loop until the
				// context deadline, re-aggregating the same page every time.
				log.Warnf("pagination: server returned the same cursor %q twice; stopping", next)
				return nil
			}
			cursor = next
		}
	}
}

// FetchAllJobs aggregates job statistics by iterating over paginated job data.
// Uses batch pagination (100 jobs per request) to minimize API calls.
//
// It calculates the start time based on the scraping interval and fetches all jobs
// that completed after that time. The function returns three typed slices with
// aggregated statistics:
//   - jobsSize: Total bytes transferred per job type/policy/status
//   - jobsCount: Number of jobs per job type/policy/status
//   - statusCount: Number of jobs per action/status (simplified aggregation)
//
// Parameters:
//   - ctx: Context for request cancellation and timeout
//   - client: Configured NetBackup API client
//   - scrapingInterval: Duration string (e.g., "5m", "1h") defining the time window
//
// Returns typed slices and an error if:
//   - The scraping interval cannot be parsed
//   - Any API request fails during pagination
//
// FetchAllJobs is a backward-compatible wrapper over FetchAllJobsFull that
// returns only the size/count/status slices. New callers that also need the
// state, dedup, files, queued, and duration metrics should use FetchAllJobsFull.
func FetchAllJobs(
	ctx context.Context,
	client *NbuClient,
	scrapingInterval string,
) (jobsSize []JobMetricValue, jobsCount []JobMetricValue, statusCount []JobStatusMetricValue, err error) {
	agg, err := FetchAllJobsFull(ctx, client, scrapingInterval)
	if err != nil {
		return nil, nil, nil, err
	}

	jobsSize = make([]JobMetricValue, 0, len(agg.Size))
	jobsCount = make([]JobMetricValue, 0, len(agg.Count))
	statusCount = make([]JobStatusMetricValue, 0, len(agg.StatusCount))
	for key, value := range agg.Size {
		jobsSize = append(jobsSize, JobMetricValue{Key: key, Value: value})
	}
	for key, value := range agg.Count {
		jobsCount = append(jobsCount, JobMetricValue{Key: key, Value: value})
	}
	for key, value := range agg.StatusCount {
		statusCount = append(statusCount, JobStatusMetricValue{Key: key, Value: value})
	}
	return jobsSize, jobsCount, statusCount, nil
}

// FetchAllJobsFull aggregates all job-derived statistics over the scraping window
// and returns the populated JobAggregator. It uses batch pagination (100 jobs per
// request) and filters to jobs that completed after now-scrapingInterval.
//
// Returns an error if the scraping interval cannot be parsed or any API request
// fails during pagination.
func FetchAllJobsFull(
	ctx context.Context,
	client *NbuClient,
	scrapingInterval string,
) (*JobAggregator, error) {
	// Start child span "netbackup.fetch_jobs" from parent context
	ctx, span := client.tracing.StartSpan(ctx, "netbackup.fetch_jobs", trace.SpanKindClient)
	defer span.End()

	duration, parseErr := time.ParseDuration("-" + scrapingInterval)
	if parseErr != nil {
		span.RecordError(parseErr)
		span.SetStatus(codes.Error, parseErr.Error())
		return nil, fmt.Errorf("invalid scraping interval %s: %w", scrapingInterval, parseErr)
	}

	startTime := time.Now().Add(duration).UTC()
	log.Debugf("Fetching jobs since %s", startTime.Format(time.RFC3339))

	// Single aggregator threaded through every page; comparable struct keys enable
	// direct map lookups, and capacity hints reduce reallocation during processing.
	agg := NewJobAggregator()
	// Opt-in per-client lifecycle tracking, bounded by the allowlist (default off =>
	// no per-client work). The per-client config is global across sites.
	agg.EnablePerClient(client.cfg.Collectors.PerClient.Enabled, client.cfg.Collectors.PerClient.Allowlist)

	// Track page count
	pageCount := 0

	err := HandlePagination(ctx, func(ctx context.Context, cursor string) (string, error) {
		pageCount++
		return FetchJobDetails(ctx, client, agg, cursor, startTime)
	})

	// Calculate total jobs fetched
	totalJobs := 0
	for _, count := range agg.Count {
		totalJobs += int(count)
	}

	// Batch span attributes for job operation
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrNetBackupEndpoint, jobsPath),
		attribute.String(telemetry.AttrNetBackupTimeWindow, scrapingInterval),
		attribute.String(telemetry.AttrNetBackupStartTime, startTime.Format(time.RFC3339)),
		attribute.Int(telemetry.AttrNetBackupTotalJobs, totalJobs),
		attribute.Int(telemetry.AttrNetBackupTotalPages, pageCount),
	}
	span.SetAttributes(attrs...)

	// Handle errors in job fetching
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Per-client last success, looked up independently of the scraping window so the
	// lifecycle metrics are right after a restart and for clients idle in the window.
	if client.cfg.Collectors.PerClient.Enabled {
		SeedClientLastSuccess(ctx, client, agg, client.cfg.Collectors.PerClient.Allowlist)
	}

	// Point-in-time pass for jobs still running or waiting. A failure here degrades
	// only the ACTIVE/QUEUED breakdown; the windowed metrics above are kept.
	if liveErr := FetchLiveJobs(ctx, client, agg); liveErr != nil {
		log.WithError(liveErr).Warn("jobs: live (non-DONE) job fetch failed; ACTIVE/QUEUED counts omitted this cycle")
	}

	span.SetStatus(codes.Ok, fmt.Sprintf("Fetched %d jobs across %d pages", totalJobs, pageCount))
	return agg, nil
}
