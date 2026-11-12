package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fjacquet/nbu_exporter/internal/models"
)

// TestFetchJobDetails_BatchProcessing verifies that FetchJobDetails processes all jobs in a batch response
func TestFetchJobDetails_BatchProcessing(t *testing.T) {
	callCount := 0
	jobsInBatch := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		// Create a response with multiple jobs
		response := createJobsResponse(jobsInBatch, 0)

		w.Header().Set(contentTypeHeader, fmt.Sprintf(contentTypeNetBackupJSONFormat, "12.0"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := createTestConfig(server.URL, "12.0")
	client := NewNbuClient(cfg)

	// Initialize maps for metrics
	agg := NewJobAggregator()
	jobsSize := agg.Size
	jobsCount := agg.Count
	jobsStatusCount := agg.StatusCount

	startTime := time.Now().Add(-5 * time.Minute)

	nextCursor, err := FetchJobDetails(context.Background(), client, agg, "", startTime)
	if err != nil {
		t.Fatalf("FetchJobDetails failed: %v", err)
	}

	// Verify only 1 API call was made
	if callCount != 1 {
		t.Errorf("Expected 1 API call, got %d", callCount)
	}

	// Verify all 3 jobs were counted (all have same key: BACKUP/STANDARD/0)
	expectedKey := JobMetricKey{Action: "BACKUP", PolicyType: "STANDARD", Status: "0"}
	if jobsCount[expectedKey] != float64(jobsInBatch) {
		t.Errorf("Expected %d jobs counted, got %v", jobsInBatch, jobsCount[expectedKey])
	}

	// Verify status count
	statusKey := JobStatusKey{Action: "BACKUP", Status: "0"}
	if jobsStatusCount[statusKey] != float64(jobsInBatch) {
		t.Errorf("Expected %d status count, got %v", jobsInBatch, jobsStatusCount[statusKey])
	}

	// Verify job sizes were accumulated (each job transfers 1024KB = 1MB)
	expectedSize := float64(jobsInBatch * 1024 * 1024) // 3 jobs * 1MB each
	if jobsSize[expectedKey] != expectedSize {
		t.Errorf("Expected job size %v, got %v", expectedSize, jobsSize[expectedKey])
	}

	// Since all jobs fit in one page, nextCursor should be "" (end of pagination)
	if nextCursor != "" {
		t.Errorf("Expected empty nextCursor (end of pagination), got %q", nextCursor)
	}
}

// TestFetchJobDetails_BatchPagination verifies pagination works correctly with batch responses
func TestFetchJobDetails_BatchPagination(t *testing.T) {
	liveCalls := 0
	callCount := 0
	jobsPerPage := 100 // Simulate first page with 100 jobs
	jobsLastPage := 50 // Second page with 50 jobs
	totalJobs := jobsPerPage + jobsLastPage
	secondPageCursor := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filter") == liveJobsFilter {
			liveCalls++
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		callCount++

		var response *models.Jobs
		cursor := r.URL.Query().Get("page[after]")
		if cursor == "" {
			// First page: 100 jobs, next cursor = "CURSOR2"
			response = createJobsResponse(jobsPerPage, 0)
			response.Meta.Pagination.Next = "CURSOR2"
		} else {
			// Second page: 50 jobs, last page. Record the cursor the client sent.
			secondPageCursor = cursor
			response = createJobsResponse(jobsLastPage, jobsPerPage)
			response.Meta.Pagination.Next = ""
		}

		w.Header().Set(contentTypeHeader, fmt.Sprintf(contentTypeNetBackupJSONFormat, "12.0"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := createTestConfig(server.URL, "12.0")
	client := NewNbuClient(cfg)

	// Use FetchAllJobs to verify full pagination flow
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, jobsCountSlice, _, err := FetchAllJobs(ctx, client, "5m")
	if err != nil {
		t.Fatalf("FetchAllJobs failed: %v", err)
	}

	// Verify exactly 2 API calls were made (2 pages)
	if callCount != 2 {
		t.Errorf("Expected 2 API calls for pagination, got %d", callCount)
	}
	if liveCalls != 1 {
		t.Errorf("Expected exactly 1 live (non-DONE) jobs query, got %d", liveCalls)
	}

	// Verify the client followed the cursor: the second request carried page[after]=CURSOR2
	if secondPageCursor != "CURSOR2" {
		t.Errorf("Expected second page request to send page[after]=CURSOR2, got %q", secondPageCursor)
	}

	// Verify total jobs collected
	totalCollected := 0
	for _, m := range jobsCountSlice {
		totalCollected += int(m.Value)
	}

	if totalCollected != totalJobs {
		t.Errorf("Expected %d total jobs, got %d", totalJobs, totalCollected)
	}
}

// TestFetchJobDetails_EmptyBatch verifies empty response handling
func TestFetchJobDetails_EmptyBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return empty jobs response
		response := &models.Jobs{}
		response.Data = nil // No jobs

		w.Header().Set(contentTypeHeader, fmt.Sprintf(contentTypeNetBackupJSONFormat, "12.0"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := createTestConfig(server.URL, "12.0")
	client := NewNbuClient(cfg)

	agg := NewJobAggregator()
	jobsCount := agg.Count
	jobsStatusCount := agg.StatusCount

	startTime := time.Now().Add(-5 * time.Minute)

	nextCursor, err := FetchJobDetails(context.Background(), client, agg, "", startTime)
	if err != nil {
		t.Fatalf("FetchJobDetails failed: %v", err)
	}

	// Verify returns "" (end of pagination) for empty response
	if nextCursor != "" {
		t.Errorf("Expected empty nextCursor for empty batch, got %q", nextCursor)
	}

	// Verify no panic with empty slice - maps should be empty
	if len(jobsCount) != 0 {
		t.Errorf("Expected empty jobsCount map, got %d entries", len(jobsCount))
	}

	if len(jobsStatusCount) != 0 {
		t.Errorf("Expected empty jobsStatusCount map, got %d entries", len(jobsStatusCount))
	}
}

// TestFetchJobDetails_MixedJobTypes verifies different job types are counted separately
func TestFetchJobDetails_MixedJobTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create response with different job types
		response := createMixedJobsResponse()

		w.Header().Set(contentTypeHeader, fmt.Sprintf(contentTypeNetBackupJSONFormat, "12.0"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := createTestConfig(server.URL, "12.0")
	client := NewNbuClient(cfg)

	agg := NewJobAggregator()
	jobsCount := agg.Count
	jobsStatusCount := agg.StatusCount

	startTime := time.Now().Add(-5 * time.Minute)

	_, err := FetchJobDetails(context.Background(), client, agg, "", startTime)
	if err != nil {
		t.Fatalf("FetchJobDetails failed: %v", err)
	}

	// Verify BACKUP/VMWARE/0 job counted
	backupKey := JobMetricKey{Action: "BACKUP", PolicyType: "VMWARE", Status: "0"}
	if jobsCount[backupKey] != 1 {
		t.Errorf("Expected 1 BACKUP/VMWARE/0 job, got %v", jobsCount[backupKey])
	}

	// Verify RESTORE/STANDARD/1 job counted
	restoreKey := JobMetricKey{Action: "RESTORE", PolicyType: "STANDARD", Status: "1"}
	if jobsCount[restoreKey] != 1 {
		t.Errorf("Expected 1 RESTORE/STANDARD/1 job, got %v", jobsCount[restoreKey])
	}

	// Verify BACKUP/STANDARD/150 job counted
	failedKey := JobMetricKey{Action: "BACKUP", PolicyType: "STANDARD", Status: "150"}
	if jobsCount[failedKey] != 1 {
		t.Errorf("Expected 1 BACKUP/STANDARD/150 job, got %v", jobsCount[failedKey])
	}

	// Verify status counts are separate
	if jobsStatusCount[JobStatusKey{Action: "BACKUP", Status: "0"}] != 1 {
		t.Error("Expected 1 BACKUP/0 status count")
	}
	if jobsStatusCount[JobStatusKey{Action: "RESTORE", Status: "1"}] != 1 {
		t.Error("Expected 1 RESTORE/1 status count")
	}
	if jobsStatusCount[JobStatusKey{Action: "BACKUP", Status: "150"}] != 1 {
		t.Error("Expected 1 BACKUP/150 status count")
	}
}

// Helper functions for creating test responses

// createJobsResponse creates a jobs response with the specified number of identical jobs
func createJobsResponse(numJobs int, startID int) *models.Jobs {
	response := &models.Jobs{}
	response.Data = make([]models.JobData, numJobs)

	for i := 0; i < numJobs; i++ {
		response.Data[i].Type = "job"
		response.Data[i].ID = fmt.Sprintf("job-%d", startID+i)
		response.Data[i].Attributes.JobID = 12345 + startID + i
		response.Data[i].Attributes.JobType = "BACKUP"
		response.Data[i].Attributes.PolicyType = "STANDARD"
		response.Data[i].Attributes.Status = 0
		response.Data[i].Attributes.KilobytesTransferred = 1024 // 1MB
	}

	response.Meta.Pagination.Next = ""

	return response
}

// createMixedJobsResponse creates a response with different job types
func createMixedJobsResponse() *models.Jobs {
	response := &models.Jobs{}
	response.Data = make([]models.JobData, 3)

	// Job 1: BACKUP/VMWARE/0
	response.Data[0].Type = "job"
	response.Data[0].ID = "12345"
	response.Data[0].Attributes.JobID = 12345
	response.Data[0].Attributes.JobType = "BACKUP"
	response.Data[0].Attributes.PolicyType = "VMWARE"
	response.Data[0].Attributes.Status = 0
	response.Data[0].Attributes.KilobytesTransferred = 52428800

	// Job 2: RESTORE/STANDARD/1
	response.Data[1].Type = "job"
	response.Data[1].ID = "12346"
	response.Data[1].Attributes.JobID = 12346
	response.Data[1].Attributes.JobType = "RESTORE"
	response.Data[1].Attributes.PolicyType = "STANDARD"
	response.Data[1].Attributes.Status = 1
	response.Data[1].Attributes.KilobytesTransferred = 10485760

	// Job 3: BACKUP/STANDARD/150 (failed)
	response.Data[2].Type = "job"
	response.Data[2].ID = "12347"
	response.Data[2].Attributes.JobID = 12347
	response.Data[2].Attributes.JobType = "BACKUP"
	response.Data[2].Attributes.PolicyType = "STANDARD"
	response.Data[2].Attributes.Status = 150
	response.Data[2].Attributes.KilobytesTransferred = 0

	response.Meta.Pagination.Next = ""

	return response
}

// TestFetchAllJobsFull_LiveJobs verifies the second, point-in-time pass: jobs that are
// not DONE (no endTime yet, so never matched by the windowed endTime filter) feed the
// state and queue breakdowns, but not the windowed count/bytes/status metrics.
func TestFetchAllJobsFull_LiveJobs(t *testing.T) {
	var filters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter := r.URL.Query().Get("filter")
		filters = append(filters, filter)
		response := &models.Jobs{}
		if filter == liveJobsFilter {
			response.Data = make([]models.JobData, 2)
			response.Data[0].Attributes = models.JobAttributes{JobID: 2, JobType: "BACKUP", State: "ACTIVE"}
			response.Data[1].Attributes = models.JobAttributes{JobID: 3, JobType: "DUPLICATION", State: jobStateQueued, JobQueueReason: 5}
		} else {
			response.Data = make([]models.JobData, 1)
			response.Data[0].Attributes = models.JobAttributes{JobID: 1, JobType: "BACKUP", State: "DONE", KilobytesTransferred: 1}
		}
		w.Header().Set(contentTypeHeader, fmt.Sprintf(contentTypeNetBackupJSONFormat, "14.0"))
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewNbuClient(createTestConfig(server.URL, "14.0"))
	agg, err := FetchAllJobsFull(context.Background(), client, "5m")
	if err != nil {
		t.Fatalf("FetchAllJobsFull failed: %v", err)
	}
	if len(filters) != 2 || !strings.HasPrefix(filters[0], "endTime gt ") || filters[1] != liveJobsFilter {
		t.Fatalf("expected windowed then live query, got %q", filters)
	}
	if got := agg.StateCount[JobStateKey{Action: "BACKUP", State: "ACTIVE"}]; got != 1 {
		t.Errorf("ACTIVE state count = %v, want 1", got)
	}
	if got := agg.QueuedCount[JobQueueKey{Action: "DUPLICATION", Reason: "5"}]; got != 1 {
		t.Errorf("queued count by reason = %v, want 1", got)
	}
	total := 0.0
	for _, v := range agg.Count {
		total += v
	}
	if total != 1 {
		t.Errorf("windowed job count = %v, want 1 (live jobs must not be counted)", total)
	}
}

// A failing live pass degrades only the ACTIVE/QUEUED breakdown.
func TestFetchAllJobsFull_LiveJobsFailureIsNonFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filter") == liveJobsFilter {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errorCode":9001}`))
			return
		}
		w.Header().Set(contentTypeHeader, fmt.Sprintf(contentTypeNetBackupJSONFormat, "14.0"))
		_, _ = w.Write([]byte(`{"data":[{"attributes":{"jobId":1,"jobType":"BACKUP","state":"DONE"}}]}`))
	}))
	defer server.Close()

	agg, err := FetchAllJobsFull(context.Background(), NewNbuClient(createTestConfig(server.URL, "14.0")), "5m")
	if err != nil {
		t.Fatalf("live-pass failure must not fail the jobs fetch: %v", err)
	}
	if agg.StateCount[JobStateKey{Action: "BACKUP", State: "DONE"}] != 1 {
		t.Errorf("windowed metrics must survive a live-pass failure")
	}
}

// HandlePagination stops when the server echoes the cursor it was given instead of
// looping until the context deadline.
func TestHandlePagination_StopsOnEchoedCursor(t *testing.T) {
	calls := 0
	err := HandlePagination(context.Background(), func(_ context.Context, cursor string) (string, error) {
		calls++
		return "SAME", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 fetches (first page + echoed cursor), got %d", calls)
	}
}

func TestMaxConcurrency(t *testing.T) {
	if maxConcurrency(8, 0) != 8 || maxConcurrency(0, 4) != 4 || maxConcurrency(0, 0) != 0 {
		t.Error("maxConcurrency must prefer maxConcurrentJobs and fall back to maxConcurrentDrives")
	}
}

// After a restart the scraping window may hold no job for a client; its last tape copy
// (30h ago here) must still be reported so the overdue panels/alerts can see it.
func TestFetchAllJobsFull_SeedsClientLastSuccessOutsideWindow(t *testing.T) {
	old := time.Now().Add(-30 * time.Hour).UTC().Format(time.RFC3339)
	var lookups []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set(contentTypeHeader, fmt.Sprintf(contentTypeNetBackupJSONFormat, "14.0"))
		if q.Get("sort") == "-endTime" {
			lookups = append(lookups, q.Get("filter"))
			if strings.Contains(q.Get("filter"), "jobType eq 'DUPLICATION'") {
				_, _ = fmt.Fprintf(w, `{"data":[{"attributes":{"jobId":7,"jobType":"DUPLICATION","clientName":"db01","policyName":"SLP-Tape","status":0,"state":"DONE","endTime":%q}}]}`, old)
				return
			}
		}
		_, _ = w.Write([]byte(`{"data":[]}`)) // empty window, nothing live, no other success
	}))
	defer server.Close()

	cfg := createTestConfig(server.URL, "14.0")
	cfg.Collectors.PerClient.Enabled = true
	cfg.Collectors.PerClient.Allowlist = []string{"db01", "db01"} // duplicate entry is looked up once
	agg, err := FetchAllJobsFull(context.Background(), NewNbuClient(cfg), "1h")
	if err != nil {
		t.Fatalf("FetchAllJobsFull: %v", err)
	}
	if len(lookups) != 3 {
		t.Errorf("expected 3 lookups (BACKUP, DUPLICATION, IMPORT) for one client, got %d: %q", len(lookups), lookups)
	}
	key := ClientSuccessKey{Client: "db01", Policy: "SLP-Tape", Action: "DUPLICATION"}
	ts, ok := agg.ClientLastSuccess[key]
	if !ok {
		t.Fatalf("last tape copy outside the window not seeded: %v", agg.ClientLastSuccess)
	}
	if age := time.Since(time.Unix(int64(ts), 0)); age < 29*time.Hour || age > 31*time.Hour {
		t.Errorf("seeded timestamp age = %v, want ~30h", age)
	}
}
