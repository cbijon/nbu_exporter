package exporter

import (
	"context"
	"strconv"

	"github.com/fjacquet/nbu_exporter/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

const (
	tapeDrivesPath     = "/storage/drives"
	tapeMediaPath      = "/storage/tape-media"
	tapeVolumePoolPath = "/storage/tape-volume-pools"
	diskPoolsPath      = "/storage/disk-pools"
)

// tapeCollector is an opt-in sub-collector that emits tape and disk-pool
// infrastructure metrics from four endpoints (all require NBU 10.5 / API v12.0+):
//
//   - GET /storage/drives            → nbu_tape_drives_count
//   - GET /storage/tape-media        → nbu_tape_media_count
//   - GET /storage/tape-volume-pools → nbu_tape_pool_partially_full
//   - GET /storage/disk-pools        → nbu_disk_pool_volume_count
//
// Each endpoint is fetched independently with full pagination. A failure on one
// endpoint is logged and skipped so the others can still emit.
// The collector is registered only when the detected API version is >= v12.0
// (gate applied in buildSubCollectors).
type tapeCollector struct {
	client              NetBackupClient
	cfg                 models.Config
	site                string
	descDrives          *prometheus.Desc
	descMedia           *prometheus.Desc
	descPoolPartial     *prometheus.Desc
	descDiskPoolVolumes *prometheus.Desc
}

func newTapeCollector(client NetBackupClient, cfg models.Config, site string) *tapeCollector {
	return &tapeCollector{
		client: client,
		cfg:    cfg,
		site:   site,
		descDrives: prometheus.NewDesc(
			"nbu_tape_drives_count",
			"Number of tape drives grouped by drive type, robot type, and drive status",
			[]string{"site", "drive_type", "robot_type", "status"}, nil,
		),
		descMedia: prometheus.NewDesc(
			"nbu_tape_media_count",
			"Number of tape media volumes grouped by pool, media type, and robot type",
			[]string{"site", "pool", "media_type", "robot_type"}, nil,
		),
		descPoolPartial: prometheus.NewDesc(
			"nbu_tape_pool_partially_full",
			"Number of partially full tape media volumes in each volume pool",
			[]string{"site", "pool_name", "pool_type"}, nil,
		),
		descDiskPoolVolumes: prometheus.NewDesc(
			"nbu_disk_pool_volume_count",
			"Number of disk volumes per disk pool, grouped by pool name, storage category, and volume state (UP/DOWN/UNKNOWN)",
			[]string{"site", "pool_name", "storage_category", "state"}, nil,
		),
	}
}

func (t *tapeCollector) Name() string { return "tape" }

// Collect fetches all four tape/disk-pool endpoints independently and emits their
// metrics. A failure on one endpoint is logged and skipped so the others can
// still emit (graceful per-endpoint degradation).
func (t *tapeCollector) Collect(ctx context.Context, ch chan<- prometheus.Metric) error {
	var firstErr error

	if err := t.collectDrives(ctx, ch); err != nil {
		log.WithError(err).Warn("tape: drive fetch failed; skipping nbu_tape_drives_count")
		firstErr = err
	}
	if err := t.collectMedia(ctx, ch); err != nil {
		log.WithError(err).Warn("tape: media fetch failed; skipping nbu_tape_media_count")
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := t.collectPools(ctx, ch); err != nil {
		log.WithError(err).Warn("tape: volume-pool fetch failed; skipping nbu_tape_pool_partially_full")
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := t.collectDiskPools(ctx, ch); err != nil {
		log.WithError(err).Warn("tape: disk-pool fetch failed; skipping nbu_disk_pool_volume_count")
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// collectDrives paginates GET /storage/drives and emits nbu_tape_drives_count
// grouped by (drive_type, robot_type, status).
func (t *tapeCollector) collectDrives(ctx context.Context, ch chan<- prometheus.Metric) error {
	type driveKey struct{ driveType, robotType, status string }
	counts := map[driveKey]float64{}

	offset := 0
	for {
		url := t.cfg.BuildURL(tapeDrivesPath, map[string]string{
			QueryParamLimit:  pageLimit,
			QueryParamOffset: strconv.Itoa(offset),
		})
		var resp models.TapeDrives
		if err := t.client.FetchData(ctx, url, &resp); err != nil {
			return err
		}
		for _, d := range resp.Data {
			k := driveKey{
				driveType: d.Attributes.DriveType,
				robotType: d.Attributes.RobotType,
				status:    d.Attributes.DriveStatus,
			}
			counts[k]++
		}
		if resp.Meta.Pagination.Next == 0 || len(resp.Data) == 0 {
			break
		}
		offset = resp.Meta.Pagination.Next
	}

	for k, v := range counts {
		ch <- prometheus.MustNewConstMetric(
			t.descDrives, prometheus.GaugeValue, v,
			t.site, k.driveType, k.robotType, k.status,
		)
	}
	return nil
}

// collectMedia paginates GET /storage/tape-media and emits nbu_tape_media_count
// grouped by (pool, media_type, robot_type).
func (t *tapeCollector) collectMedia(ctx context.Context, ch chan<- prometheus.Metric) error {
	type mediaKey struct{ pool, mediaType, robotType string }
	counts := map[mediaKey]float64{}

	offset := 0
	for {
		url := t.cfg.BuildURL(tapeMediaPath, map[string]string{
			QueryParamLimit:  pageLimit,
			QueryParamOffset: strconv.Itoa(offset),
		})
		var resp models.TapeMedia
		if err := t.client.FetchData(ctx, url, &resp); err != nil {
			return err
		}
		for _, v := range resp.Data {
			k := mediaKey{
				pool:      v.Attributes.VolumePool,
				mediaType: v.Attributes.MediaType,
				robotType: v.Attributes.Robot.RobotType,
			}
			counts[k]++
		}
		if resp.Meta.Pagination.Next == 0 || len(resp.Data) == 0 {
			break
		}
		offset = resp.Meta.Pagination.Next
	}

	for k, v := range counts {
		ch <- prometheus.MustNewConstMetric(
			t.descMedia, prometheus.GaugeValue, v,
			t.site, k.pool, k.mediaType, k.robotType,
		)
	}
	return nil
}

// collectPools paginates GET /storage/tape-volume-pools and emits
// nbu_tape_pool_partially_full per (pool_name, pool_type).
func (t *tapeCollector) collectPools(ctx context.Context, ch chan<- prometheus.Metric) error {
	offset := 0
	for {
		url := t.cfg.BuildURL(tapeVolumePoolPath, map[string]string{
			QueryParamLimit:  pageLimit,
			QueryParamOffset: strconv.Itoa(offset),
		})
		var resp models.TapeVolumePools
		if err := t.client.FetchData(ctx, url, &resp); err != nil {
			return err
		}
		for _, p := range resp.Data {
			ch <- prometheus.MustNewConstMetric(
				t.descPoolPartial, prometheus.GaugeValue,
				float64(p.Attributes.PartiallyFullMedia),
				t.site, p.Attributes.VolumePoolName, p.Attributes.PoolType,
			)
		}
		if resp.Meta.Pagination.Next == 0 || len(resp.Data) == 0 {
			break
		}
		offset = resp.Meta.Pagination.Next
	}
	return nil
}

// collectDiskPools paginates GET /storage/disk-pools and emits
// nbu_disk_pool_volume_count grouped by (pool_name, storage_category, state).
// Each disk pool's volumes are counted by their state (UP/DOWN/UNKNOWN).
func (t *tapeCollector) collectDiskPools(ctx context.Context, ch chan<- prometheus.Metric) error {
	type poolVolumeKey struct{ poolName, storageCategory, state string }
	counts := map[poolVolumeKey]float64{}

	offset := 0
	for {
		url := t.cfg.BuildURL(diskPoolsPath, map[string]string{
			QueryParamLimit:  pageLimit,
			QueryParamOffset: strconv.Itoa(offset),
		})
		var resp models.DiskPools
		if err := t.client.FetchData(ctx, url, &resp); err != nil {
			return err
		}
		for _, pool := range resp.Data {
			for _, vol := range pool.Attributes.DiskVolumes {
				k := poolVolumeKey{
					poolName:        pool.Attributes.Name,
					storageCategory: pool.Attributes.StorageCategory,
					state:           vol.State,
				}
				counts[k]++
			}
		}
		if resp.Meta.Pagination.Next == 0 || len(resp.Data) == 0 {
			break
		}
		offset = resp.Meta.Pagination.Next
	}

	for k, v := range counts {
		ch <- prometheus.MustNewConstMetric(
			t.descDiskPoolVolumes, prometheus.GaugeValue, v,
			t.site, k.poolName, k.storageCategory, k.state,
		)
	}
	return nil
}
