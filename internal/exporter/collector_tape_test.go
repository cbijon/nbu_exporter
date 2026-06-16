package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fjacquet/nbu_exporter/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTapeClient is a minimal NetBackupClient mock for tape collector tests.
// It returns pre-canned responses keyed by URL path substring.
type fakeTapeClient struct {
	responses map[string]interface{}
	fetchErr  error
}

func (f *fakeTapeClient) FetchData(_ context.Context, url string, target interface{}) error {
	if f.fetchErr != nil {
		return f.fetchErr
	}
	for path, resp := range f.responses {
		if strings.Contains(url, path) {
			b, err := json.Marshal(resp)
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			return json.Unmarshal(b, target)
		}
	}
	// Return empty JSON object for any unconfigured path so tests that only set up
	// a subset of endpoints don't fail on the others.
	return json.Unmarshal([]byte(`{}`), target)
}

func (f *fakeTapeClient) DetectAPIVersion(_ context.Context) (string, error) {
	return models.APIVersion120, nil
}

func (f *fakeTapeClient) Close() error { return nil }

// driveResponse builds a single-page TapeDrives API response.
func driveResponse(drives []models.TapeDrive) models.TapeDrives {
	return models.TapeDrives{Data: drives}
}

// mediaResponse builds a single-page TapeMedia API response.
func mediaResponse(vols []models.TapeMediaVolume) models.TapeMedia {
	return models.TapeMedia{Data: vols}
}

// poolResponse builds a single-page TapeVolumePools API response.
func poolResponse(pools []models.TapeVolumePool) models.TapeVolumePools {
	return models.TapeVolumePools{Data: pools}
}

// makeDrive is a helper to construct a TapeDrive with the given attributes.
func makeDrive(driveType, robotType, status string) models.TapeDrive {
	d := models.TapeDrive{ID: driveType + "-" + status, Type: "drive"}
	d.Attributes.DriveType = driveType
	d.Attributes.RobotType = robotType
	d.Attributes.DriveStatus = status
	return d
}

// makeVol is a helper to construct a TapeMediaVolume with the given attributes.
func makeVol(pool, mediaType, robotType string) models.TapeMediaVolume {
	v := models.TapeMediaVolume{ID: "ABC001", Type: "volumeInfo"}
	v.Attributes.VolumePool = pool
	v.Attributes.MediaType = mediaType
	v.Attributes.Robot.RobotType = robotType
	return v
}

// makePool is a helper to construct a TapeVolumePool with the given attributes.
func makePool(name, poolType string, partiallyFull int) models.TapeVolumePool {
	p := models.TapeVolumePool{ID: "1", Type: "volumePoolDetails"}
	p.Attributes.VolumePoolName = name
	p.Attributes.PoolType = poolType
	p.Attributes.PartiallyFullMedia = partiallyFull
	return p
}

// minimalTapeConfig returns a Config with the minimum fields needed for BuildURL
// to produce a valid URL (scheme + host + port + uri).
func minimalTapeConfig() models.Config {
	cfg := models.Config{}
	cfg.NbuServer.Scheme = "http"
	cfg.NbuServer.Host = "localhost"
	cfg.NbuServer.Port = "1556"
	cfg.NbuServer.URI = "/netbackup"
	return cfg
}

// collectTapeMetrics runs the tape collector and returns all emitted metrics.
func collectTapeMetrics(t *testing.T, client NetBackupClient) []prometheus.Metric {
	t.Helper()
	tc := newTapeCollector(client, minimalTapeConfig(), nil)
	ch := make(chan prometheus.Metric, 64)
	err := tc.Collect(context.Background(), ch)
	close(ch)
	require.NoError(t, err)
	return collectToSlice(ch)
}

// TestTapeCollectorName verifies the collector has the expected name.
func TestTapeCollectorName(t *testing.T) {
	tc := newTapeCollector(nil, minimalTapeConfig(), nil)
	assert.Equal(t, "tape", tc.Name())
}

// TestTapeCollectorGateV30 verifies that the tape collector is NOT added to
// the sub-collector list when the API version is v3.0, even when tape is enabled.
func TestTapeCollectorGateV30(t *testing.T) {
	cfg := models.Config{}
	cfg.Collectors.Tape.Enabled = true
	c := makeMinimalCollector(models.APIVersion30, cfg)
	c.client = &NbuClient{cfg: cfg}

	subs := buildSubCollectors(c)
	for _, s := range subs {
		assert.NotEqual(t, "tape", s.Name(),
			"tape collector must not be active for API v3.0")
	}
}

// TestTapeCollectorGateV12Plus verifies that the tape collector IS included for
// API versions v12.0, v13.0, and v14.0.
func TestTapeCollectorGateV12Plus(t *testing.T) {
	for _, ver := range []string{models.APIVersion120, models.APIVersion130, models.APIVersion140} {
		t.Run("API_v"+ver, func(t *testing.T) {
			cfg := models.Config{}
			cfg.Collectors.Tape.Enabled = true
			c := makeMinimalCollector(ver, cfg)
			c.client = &NbuClient{cfg: cfg}

			subs := buildSubCollectors(c)
			found := false
			for _, s := range subs {
				if s.Name() == "tape" {
					found = true
				}
			}
			assert.True(t, found,
				"tape collector must be active for API version %s", ver)
		})
	}
}

// TestTapeCollectorDrives verifies that nbu_tape_drives_count is emitted and
// aggregates correctly by (drive_type, robot_type, status).
func TestTapeCollectorDrives(t *testing.T) {
	client := &fakeTapeClient{
		responses: map[string]interface{}{
			tapeDrivesPath:     driveResponse([]models.TapeDrive{
				makeDrive("DT_HCART", "TLD", "DRIVE_STATUS_UP"),
				makeDrive("DT_HCART", "TLD", "DRIVE_STATUS_UP"),
				makeDrive("DT_HCART", "TLD", "DRIVE_STATUS_DOWN"),
			}),
			tapeMediaPath:      mediaResponse(nil),
			tapeVolumePoolPath: poolResponse(nil),
		},
	}

	tc := newTapeCollector(client, minimalTapeConfig(), nil)
	ch := make(chan prometheus.Metric, 64)
	err := tc.Collect(context.Background(), ch)
	close(ch)
	require.NoError(t, err)

	metrics := collectToSlice(ch)
	driveMetrics := 0
	for _, m := range metrics {
		if m.Desc() == tc.descDrives {
			driveMetrics++
		}
	}
	// 2 UP + 1 DOWN → 2 distinct label combinations
	assert.Equal(t, 2, driveMetrics, "expected 2 drive metric series (UP and DOWN)")
}

// TestTapeCollectorMedia verifies that nbu_tape_media_count is emitted and
// aggregates correctly by (pool, media_type, robot_type).
func TestTapeCollectorMedia(t *testing.T) {
	client := &fakeTapeClient{
		responses: map[string]interface{}{
			tapeDrivesPath: driveResponse(nil),
			tapeMediaPath: mediaResponse([]models.TapeMediaVolume{
				makeVol("PoolA", "HCART", "TLD"),
				makeVol("PoolA", "HCART", "TLD"),
				makeVol("PoolB", "HCART3", "TLD"),
			}),
			tapeVolumePoolPath: poolResponse(nil),
		},
	}

	tc := newTapeCollector(client, minimalTapeConfig(), nil)
	ch := make(chan prometheus.Metric, 64)
	err := tc.Collect(context.Background(), ch)
	close(ch)
	require.NoError(t, err)

	metrics := collectToSlice(ch)
	mediaMetrics := 0
	for _, m := range metrics {
		if m.Desc() == tc.descMedia {
			mediaMetrics++
		}
	}
	// PoolA/HCART/TLD + PoolB/HCART3/TLD → 2 distinct series
	assert.Equal(t, 2, mediaMetrics, "expected 2 media metric series")
}

// TestTapeCollectorPools verifies that nbu_tape_pool_partially_full is emitted
// with the correct value per pool.
func TestTapeCollectorPools(t *testing.T) {
	client := &fakeTapeClient{
		responses: map[string]interface{}{
			tapeDrivesPath: driveResponse(nil),
			tapeMediaPath:  mediaResponse(nil),
			tapeVolumePoolPath: poolResponse([]models.TapeVolumePool{
				makePool("NetBackup", "REGULAR_MEDIA_POOL", 3),
				makePool("Scratch", "SCRATCH_MEDIA_POOL", 0),
			}),
		},
	}

	tc := newTapeCollector(client, minimalTapeConfig(), nil)
	ch := make(chan prometheus.Metric, 64)
	err := tc.Collect(context.Background(), ch)
	close(ch)
	require.NoError(t, err)

	metrics := collectToSlice(ch)
	poolMetrics := 0
	for _, m := range metrics {
		if m.Desc() == tc.descPoolPartial {
			poolMetrics++
		}
	}
	assert.Equal(t, 2, poolMetrics, "expected 2 pool metric series (one per pool)")
}

// TestTapeCollectorGracefulDegradation verifies that a drives failure does not
// prevent pool and media metrics from being emitted.
func TestTapeCollectorGracefulDegradation(t *testing.T) {
	drives := &fakeTapeClient{fetchErr: fmt.Errorf("drives endpoint unavailable")}
	// Use a client that only fails on drives
	gracefulClient := &gracefulTapeClient{
		drivesErr: fmt.Errorf("drives endpoint unavailable"),
		mediaResp: mediaResponse([]models.TapeMediaVolume{makeVol("Pool", "HCART", "TLD")}),
		poolResp:  poolResponse([]models.TapeVolumePool{makePool("Pool", "REGULAR_MEDIA_POOL", 1)}),
	}
	_ = drives // declared above for clarity only

	tc := newTapeCollector(gracefulClient, minimalTapeConfig(), nil)
	ch := make(chan prometheus.Metric, 64)
	err := tc.Collect(context.Background(), ch)
	close(ch)

	// Drives failed → first error is returned, but other endpoints still emit
	assert.Error(t, err, "expected error from drives failure")

	metrics := collectToSlice(ch)
	mediaFound, poolFound := false, false
	for _, m := range metrics {
		if m.Desc() == tc.descMedia {
			mediaFound = true
		}
		if m.Desc() == tc.descPoolPartial {
			poolFound = true
		}
	}
	assert.True(t, mediaFound, "media metrics must be emitted even when drives fail")
	assert.True(t, poolFound, "pool metrics must be emitted even when drives fail")
}

// diskPoolResponse builds a single-page DiskPools API response.
func diskPoolResponse(pools []models.DiskPool) models.DiskPools {
	return models.DiskPools{Data: pools}
}

// makeDiskPool is a helper to construct a DiskPool with the given volumes.
func makeDiskPool(name, category string, volumes []models.DiskVolume) models.DiskPool {
	dp := models.DiskPool{ID: name, Type: "diskPool"}
	dp.Attributes.Name = name
	dp.Attributes.StorageCategory = category
	dp.Attributes.DiskPoolState = "UP"
	dp.Attributes.DiskVolumes = volumes
	return dp
}

// makeDiskVolume is a helper to construct a DiskVolume with a given state.
func makeDiskVolume(name, state string) models.DiskVolume {
	return models.DiskVolume{Name: name, ID: name, State: state}
}

// TestTapeCollectorDiskPools verifies that nbu_disk_pool_volume_count is emitted
// and aggregated correctly by (pool_name, storage_category, state).
func TestTapeCollectorDiskPools(t *testing.T) {
	client := &fakeTapeClient{
		responses: map[string]interface{}{
			tapeDrivesPath:     driveResponse(nil),
			tapeMediaPath:      mediaResponse(nil),
			tapeVolumePoolPath: poolResponse(nil),
			diskPoolsPath: diskPoolResponse([]models.DiskPool{
				makeDiskPool("msdp-pool", "MSDP", []models.DiskVolume{
					makeDiskVolume("vol-1", "UP"),
					makeDiskVolume("vol-2", "UP"),
					makeDiskVolume("vol-3", "DOWN"),
				}),
				makeDiskPool("adv-pool", "ADVANCED_DISK", []models.DiskVolume{
					makeDiskVolume("vol-a", "UP"),
				}),
			}),
		},
	}

	tc := newTapeCollector(client, minimalTapeConfig(), nil)
	ch := make(chan prometheus.Metric, 64)
	err := tc.Collect(context.Background(), ch)
	close(ch)
	require.NoError(t, err)

	metrics := collectToSlice(ch)
	diskPoolMetrics := 0
	for _, m := range metrics {
		if m.Desc() == tc.descDiskPoolVolumes {
			diskPoolMetrics++
		}
	}
	// msdp-pool: 2 UP + 1 DOWN → 2 series; adv-pool: 1 UP → 1 series = 3 total
	assert.Equal(t, 3, diskPoolMetrics, "expected 3 disk-pool volume metric series")
}

// gracefulTapeClient simulates a partial failure: drives endpoint fails,
// media and pool endpoints succeed.
type gracefulTapeClient struct {
	drivesErr  error
	mediaResp  models.TapeMedia
	poolResp   models.TapeVolumePools
}

func (g *gracefulTapeClient) FetchData(_ context.Context, url string, target interface{}) error {
	switch {
	case strings.Contains(url, tapeDrivesPath):
		return g.drivesErr
	case strings.Contains(url, tapeMediaPath):
		b, _ := json.Marshal(g.mediaResp)
		return json.Unmarshal(b, target)
	case strings.Contains(url, tapeVolumePoolPath):
		b, _ := json.Marshal(g.poolResp)
		return json.Unmarshal(b, target)
	case strings.Contains(url, diskPoolsPath):
		b, _ := json.Marshal(models.DiskPools{})
		return json.Unmarshal(b, target)
	}
	return fmt.Errorf("no response for %s", url)
}

func (g *gracefulTapeClient) DetectAPIVersion(_ context.Context) (string, error) {
	return models.APIVersion120, nil
}

func (g *gracefulTapeClient) Close() error { return nil }
