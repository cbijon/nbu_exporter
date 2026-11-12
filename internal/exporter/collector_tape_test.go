package exporter

import (
	"context"
	"encoding/json"
	"errors"
	neturl "net/url"
	"os"
	"strings"
	"testing"

	"github.com/fjacquet/nbu_exporter/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// tapeRoutedClient is a NetBackupClient mock. byPath serves a fixture per endpoint
// path substring ("" => return an error, to exercise graceful degradation). byPage
// serves paginated fixtures keyed by path substring then page key: the page[after]
// cursor when present (cursor-paginated endpoints), else the page[offset] value, else
// "" for the first request. A key not in the map returns an empty page.
type tapeRoutedClient struct {
	t      *testing.T
	byPath map[string]string
	byPage map[string]map[string]string
	calls  map[string]int
}

func (c *tapeRoutedClient) FetchData(_ context.Context, rawURL string, target interface{}) error {
	c.t.Helper()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	for sub, pages := range c.byPage {
		if strings.Contains(rawURL, sub) {
			c.calls[sub]++
			u, err := neturl.Parse(rawURL)
			require.NoError(c.t, err)
			q := u.Query()
			key := q.Get("page[offset]")
			if q.Has("page[after]") {
				key = q.Get("page[after]")
			} else if key == "0" {
				key = ""
			}
			fixture, ok := pages[key]
			if !ok { // past the last page -> empty result
				return json.Unmarshal([]byte(`{"data":[]}`), target)
			}
			if fixture == "" { // simulate a fetch error at this offset (mid-pagination)
				return errors.New("endpoint unavailable")
			}
			data, err := os.ReadFile(fixture)
			require.NoError(c.t, err)
			return json.Unmarshal(data, target)
		}
	}
	for sub, fixture := range c.byPath {
		if strings.Contains(rawURL, sub) {
			if fixture == "" {
				return errors.New("endpoint unavailable")
			}
			data, err := os.ReadFile(fixture)
			require.NoError(c.t, err)
			return json.Unmarshal(data, target)
		}
	}
	c.t.Fatalf("unexpected URL: %s", rawURL)
	return nil
}
func (c *tapeRoutedClient) DetectAPIVersion(context.Context) (string, error) {
	return models.APIVersion140, nil
}
func (c *tapeRoutedClient) Close() error { return nil }

func TestTapeCollector_Drives(t *testing.T) {
	client := &tapeRoutedClient{t: t, byPath: map[string]string{
		"/storage/drives":              "../../testdata/api-versions/drives-response.json",
		"/storage/tape-media":          "",
		"/storage/robots-device-hosts": "",
		"/storage/tape-volume-pools":   "",
		"/storage/disk-pools":          "",
	}}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 64)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)

	driveCounts := map[string]float64{} // "drive_type|robot_type|status" -> value
	hosts := map[string]string{}        // drive_name -> media_server
	infoCount := 0
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		require.Equal(t, "site1", labelValue(&d, "site"))
		desc := m.Desc().String()
		switch {
		case strings.Contains(desc, "nbu_tape_drives_count"):
			key := labelValue(&d, "drive_type") + "|" + labelValue(&d, "robot_type") + "|" + labelValue(&d, "status")
			driveCounts[key] = d.GetGauge().GetValue()
		case strings.Contains(desc, "nbu_tape_drive_info"):
			require.Equal(t, float64(1), d.GetGauge().GetValue())
			require.NotEmpty(t, labelValue(&d, "drive_name"), "drive name comes from data[].id")
			require.NotEmpty(t, labelValue(&d, "media_server"), "device host falls back to devicePaths[0]")
			hosts[labelValue(&d, "drive_name")] = labelValue(&d, "media_server")
			infoCount++
		}
	}
	require.Equal(t, float64(2), driveCounts["DT_HCART|TLD|DRIVE_STATUS_UP"])
	require.Equal(t, float64(1), driveCounts["DT_HCART|TLD|DRIVE_STATUS_DOWN"])
	require.Equal(t, 3, infoCount, "one nbu_tape_drive_info per drive")
	require.Equal(t, "ms2.example.com", hosts["IBM.ULT3580-HH9.002"], "deviceHost taken from devicePaths when the attribute is empty")
}

func TestTapeCollector_MediaPaginated(t *testing.T) {
	client := &tapeRoutedClient{
		t: t,
		byPath: map[string]string{
			"/storage/drives":              "",
			"/storage/robots-device-hosts": "",
			"/storage/tape-volume-pools":   "",
			"/storage/disk-pools":          "",
		},
		byPage: map[string]map[string]string{
			"/storage/tape-media": {
				"":       "../../testdata/api-versions/tape-media-page1.json",
				"ABC002": "../../testdata/api-versions/tape-media-page2.json",
			},
		},
	}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 64)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)

	media := map[string]float64{} // "pool|media_type|robot_type" -> value
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		if strings.Contains(m.Desc().String(), "nbu_tape_media_count") {
			require.Equal(t, "site1", labelValue(&d, "site"))
			media[labelValue(&d, "pool")+"|"+labelValue(&d, "media_type")+"|"+labelValue(&d, "robot_type")] = d.GetGauge().GetValue()
		}
	}
	require.Equal(t, float64(3), media["NetBackup|HCART|TLD"], "both pages aggregated by pool/media_type/robot_type")
}

func TestTapeCollector_RobotHostsAndRegistration(t *testing.T) {
	client := &tapeRoutedClient{t: t, byPath: map[string]string{
		"/storage/drives":              "",
		"/storage/tape-media":          "",
		"/storage/robots-device-hosts": "../../testdata/api-versions/robots-device-hosts-response.json",
		"/storage/tape-volume-pools":   "",
		"/storage/disk-pools":          "",
	}}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 16)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)

	var got float64
	found := false
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		if strings.Contains(m.Desc().String(), "nbu_tape_robot_device_hosts") {
			require.Equal(t, "site1", labelValue(&d, "site"))
			got = d.GetGauge().GetValue()
			found = true
		}
	}
	require.True(t, found, "nbu_tape_robot_device_hosts must be emitted")
	require.Equal(t, float64(2), got)
}

// All endpoints fail -> Collect returns nil and emits nothing (graceful degradation).
func TestTapeCollector_GracefulDegradation(t *testing.T) {
	client := &tapeRoutedClient{t: t, byPath: map[string]string{
		"/storage/drives":              "",
		"/storage/tape-media":          "",
		"/storage/robots-device-hosts": "",
		"/storage/tape-volume-pools":   "",
		"/storage/disk-pools":          "",
	}}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 4)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)
	require.Empty(t, ch)
}

// The tape collector is built only when collectors.tape is enabled.
func TestBuildSubCollectorsFor_Tape(t *testing.T) {
	cfg := testConfig()
	cfg.Collectors.Tape.Enabled = true
	subs := buildSubCollectorsFor(&errClient{}, cfg, "site1")
	found := false
	for _, s := range subs {
		if s.Name() == "tape" {
			found = true
		}
	}
	require.True(t, found, "tape collector should be built when collectors.tape.enabled")
}

// TestTapeCollector_MediaPartialOnError verifies that a mid-pagination fetch error
// still emits the counts accumulated from earlier pages (degraded-but-partial).
func TestTapeCollector_MediaPartialOnError(t *testing.T) {
	client := &tapeRoutedClient{
		t: t,
		byPath: map[string]string{
			"/storage/drives":              "",
			"/storage/robots-device-hosts": "",
			"/storage/tape-volume-pools":   "",
			"/storage/disk-pools":          "",
		},
		byPage: map[string]map[string]string{
			"/storage/tape-media": {
				"":       "../../testdata/api-versions/tape-media-page1.json",
				"ABC002": "", // page 2 errors mid-pagination
			},
		},
	}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 32)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)

	media := map[string]float64{}
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		if strings.Contains(m.Desc().String(), "nbu_tape_media_count") {
			media[labelValue(&d, "pool")+"|"+labelValue(&d, "media_type")+"|"+labelValue(&d, "robot_type")] = d.GetGauge().GetValue()
		}
	}
	require.Equal(t, float64(2), media["NetBackup|HCART|TLD"], "page-1 counts emitted despite the page-2 error")
}

// TestTapeCollector_PoolsAndDiskPools verifies the two API v12.0+ endpoints:
// /storage/tape-volume-pools -> nbu_tape_pool_partially_full, and
// /storage/disk-pools -> nbu_disk_pool_volume_count (counted by volume state).
func TestTapeCollector_PoolsAndDiskPools(t *testing.T) {
	client := &tapeRoutedClient{t: t, byPath: map[string]string{
		"/storage/drives":              "",
		"/storage/tape-media":          "",
		"/storage/robots-device-hosts": "",
		"/storage/tape-volume-pools":   "../../testdata/api-versions/tape-volume-pools-response.json",
		"/storage/disk-pools":          "../../testdata/api-versions/disk-pools-response.json",
	}}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 64)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)

	pools := map[string]float64{}    // "pool_name|pool_type" -> value
	diskVols := map[string]float64{} // "pool_name|storage_category|state" -> value
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		require.Equal(t, "site1", labelValue(&d, "site"))
		desc := m.Desc().String()
		switch {
		case strings.Contains(desc, "nbu_tape_pool_partially_full"):
			pools[labelValue(&d, "pool_name")+"|"+labelValue(&d, "pool_type")] = d.GetGauge().GetValue()
		case strings.Contains(desc, "nbu_disk_pool_volume_count"):
			diskVols[labelValue(&d, "pool_name")+"|"+labelValue(&d, "storage_category")+"|"+labelValue(&d, "state")] = d.GetGauge().GetValue()
		}
	}
	require.Equal(t, float64(3), pools["NetBackup|REGULAR_MEDIA_POOL"], "partiallyFullMedia surfaced per pool")
	require.Equal(t, float64(0), pools["Scratch|SCRATCH_MEDIA_POOL"])
	require.Equal(t, float64(2), diskVols["dp1|MSDP|UP"], "disk volumes counted by state")
	require.Equal(t, float64(1), diskVols["dp1|MSDP|DOWN"])
}

// writeFixture writes body to a temp file and returns its path.
func writeFixture(t *testing.T, body string) string {
	t.Helper()
	p := t.TempDir() + "/fixture.json"
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// A backend that keeps returning the same cursor (or ignores page[after]) must not
// make the collector loop: it stops after the repeated cursor instead of multiplying
// counts up to the page cap.
func TestTapeCollector_MediaCursorEchoDoesNotLoop(t *testing.T) {
	page := writeFixture(t, `{"data":[{"attributes":{"barcode":"X1","mediaType":"HCART","volumePool":"Scratch","robot":{"robotType":"TLD"}}}],
		"meta":{"pagination":{"next":"X1"}}}`)
	client := &tapeRoutedClient{
		t: t,
		byPath: map[string]string{
			"/storage/drives": "", "/storage/robots-device-hosts": "",
			"/storage/tape-volume-pools": "", "/storage/disk-pools": "",
		},
		byPage: map[string]map[string]string{"/storage/tape-media": {"": page, "X1": page}},
	}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 32)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)
	var total float64
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		if strings.Contains(m.Desc().String(), "nbu_tape_media_count") {
			total += d.GetGauge().GetValue()
		}
	}
	require.Equal(t, 2, client.calls["/storage/tape-media"], "second page repeats the cursor -> stop")
	require.Equal(t, float64(2), total)
}

// Offset-paginated endpoints follow meta.pagination.next and stop when it does not
// advance (here the backend echoes next=0 on every page).
func TestTapeCollector_DiskPoolsOffsetPagination(t *testing.T) {
	p1 := writeFixture(t, `{"data":[{"attributes":{"name":"dp1","storageCategory":"MSDP","diskVolumes":[{"state":"UP"}]}}],
		"meta":{"pagination":{"offset":0,"next":1}}}`)
	p2 := writeFixture(t, `{"data":[{"attributes":{"name":"dp2","storageCategory":"ADVANCED_DISK","diskVolumes":[{"state":"DOWN"}]}}],
		"meta":{"pagination":{"offset":1,"next":0}}}`)
	client := &tapeRoutedClient{
		t: t,
		byPath: map[string]string{
			"/storage/drives": "", "/storage/robots-device-hosts": "",
			"/storage/tape-volume-pools": "", "/storage/tape-media": "",
		},
		byPage: map[string]map[string]string{"/storage/disk-pools": {"": p1, "1": p2}},
	}
	c := newTapeCollector(client, testConfig(), "site1")
	ch := make(chan prometheus.Metric, 32)
	require.NoError(t, c.Collect(context.Background(), ch))
	close(ch)
	got := map[string]float64{}
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		if strings.Contains(m.Desc().String(), "nbu_disk_pool_volume_count") {
			got[labelValue(&d, "pool_name")+"|"+labelValue(&d, "state")] = d.GetGauge().GetValue()
		}
	}
	require.Equal(t, 2, client.calls["/storage/disk-pools"])
	require.Equal(t, map[string]float64{"dp1|UP": 1, "dp2|DOWN": 1}, got)
}

func TestNextOffset(t *testing.T) {
	require.Equal(t, 100, nextOffset(0, 100, 100))
	require.Equal(t, -1, nextOffset(0, 0, 100), "next=0 on the first page means last page")
	require.Equal(t, -1, nextOffset(100, 100, 100), "a non-advancing next stops the loop")
	require.Equal(t, -1, nextOffset(0, 100, 0), "an empty page stops the loop")
}
