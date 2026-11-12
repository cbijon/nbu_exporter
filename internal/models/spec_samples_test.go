package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The samples under testdata/spec-samples are generated from the Veritas OpenAPI
// specs (scripts/gen_spec_samples.py): every property present with its spec type.
// Decoding them proves each model field has a JSON type compatible with the spec,
// and the non-empty checks prove the fields the collectors actually read live where
// the model expects them (a wrongly nested field decodes silently to its zero value).

func decodeSpecSample(t *testing.T, name string, target interface{}) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "spec-samples", name+".json"))
	if err != nil {
		t.Fatalf("read sample %s: %v (regenerate with scripts/gen_spec_samples.py)", name, err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("%s: model is not compatible with the spec response: %v", name, err)
	}
}

func requireSet(t *testing.T, sample, field string, ok bool) {
	t.Helper()
	if !ok {
		t.Errorf("%s: %s is empty after decoding the spec sample (wrong JSON name or nesting?)", sample, field)
	}
}

func TestSpecSample_Jobs(t *testing.T) {
	var r Jobs
	decodeSpecSample(t, "jobs", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	a := r.Data[0].Attributes
	requireSet(t, "jobs", "jobType", a.JobType != "")
	requireSet(t, "jobs", "policyType", a.PolicyType != "")
	requireSet(t, "jobs", "policyName", a.PolicyName != "")
	requireSet(t, "jobs", "clientName", a.ClientName != "")
	requireSet(t, "jobs", "state", a.State != "")
	requireSet(t, "jobs", "status", a.Status != 0)
	requireSet(t, "jobs", "kilobytesTransferred", a.KilobytesTransferred != 0)
	requireSet(t, "jobs", "numberOfFiles", a.NumberOfFiles != 0)
	requireSet(t, "jobs", "dedupRatio", a.DedupRatio != 0)
	requireSet(t, "jobs", "jobQueueReason", a.JobQueueReason != 0)
	requireSet(t, "jobs", "startTime", !a.StartTime.IsZero())
	requireSet(t, "jobs", "endTime", !a.EndTime.IsZero())
	requireSet(t, "jobs", "meta.pagination.next (cursor)", r.Meta.Pagination.Next != "")
}

func TestSpecSample_StorageUnits(t *testing.T) {
	var r Storages
	decodeSpecSample(t, "storage-units", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	a := r.Data[0].Attributes
	requireSet(t, "storage-units", "name", a.Name != "")
	requireSet(t, "storage-units", "storageType", a.StorageType != "")
	requireSet(t, "storage-units", "freeCapacityBytes", a.FreeCapacityBytes != 0)
	requireSet(t, "storage-units", "usedCapacityBytes", a.UsedCapacityBytes != 0)
	requireSet(t, "storage-units", "totalCapacityBytes", a.TotalCapacityBytes != 0)
	requireSet(t, "storage-units", "maxConcurrentJobs (disk)", a.MaxConcurrentJobs != 0)
	requireSet(t, "storage-units", "maxConcurrentDrives (tape)", a.MaxConcurrentDrives != 0)
}

func TestSpecSample_Drives(t *testing.T) {
	var r TapeDrives
	decodeSpecSample(t, "drives", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	d := r.Data[0]
	requireSet(t, "drives", "id (drive name)", d.ID != "")
	requireSet(t, "drives", "driveStatus", d.Attributes.DriveStatus != "")
	requireSet(t, "drives", "driveType", d.Attributes.DriveType != "")
	requireSet(t, "drives", "robotType", d.Attributes.RobotType != "")
	requireSet(t, "drives", "devicePaths[].deviceHost", len(d.Attributes.DevicePaths) > 0 && d.Attributes.DevicePaths[0].DeviceHost != "")
}

func TestSpecSample_TapeMedia(t *testing.T) {
	var r TapeMedia
	decodeSpecSample(t, "tape-media", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	a := r.Data[0].Attributes
	requireSet(t, "tape-media", "volumePool", a.VolumePool != "")
	requireSet(t, "tape-media", "mediaType", a.MediaType != "")
	requireSet(t, "tape-media", "robot.robotType", a.Robot.RobotType != "")
	requireSet(t, "tape-media", "meta.pagination.next (cursor)", r.Meta.Pagination.Next != "")
}

func TestSpecSample_TapeVolumePools(t *testing.T) {
	var r TapeVolumePools
	decodeSpecSample(t, "tape-volume-pools", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	a := r.Data[0].Attributes
	requireSet(t, "tape-volume-pools", "volumePoolName", a.VolumePoolName != "")
	requireSet(t, "tape-volume-pools", "poolType", a.PoolType != "")
	requireSet(t, "tape-volume-pools", "partiallyFullMedia", a.PartiallyFullMedia != 0)
	requireSet(t, "tape-volume-pools", "meta.pagination.next (cursor)", r.Meta.Pagination.Next != "")
}

func TestSpecSample_DiskPools(t *testing.T) {
	var r DiskPools
	decodeSpecSample(t, "disk-pools", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	a := r.Data[0].Attributes
	requireSet(t, "disk-pools", "name", a.Name != "")
	requireSet(t, "disk-pools", "storageCategory", a.StorageCategory != "")
	requireSet(t, "disk-pools", "diskVolumes[].state", len(a.DiskVolumes) > 0 && a.DiskVolumes[0].State != "")
	requireSet(t, "disk-pools", "meta.pagination.next (offset)", r.Meta.Pagination.Next != 0)
}

func TestSpecSample_RobotDeviceHosts(t *testing.T) {
	var r RobotDeviceHosts
	decodeSpecSample(t, "robots-device-hosts", &r)
	if len(r.Data) == 0 || r.Data[0].ID == "" {
		t.Error("robots-device-hosts: data[].id is empty")
	}
}

func TestSpecSample_Alerts(t *testing.T) {
	var r Alerts
	decodeSpecSample(t, "alerts", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	a := r.Data[0].Attributes
	requireSet(t, "alerts", "severity", a.Severity != "")
	requireSet(t, "alerts", "category", a.Category != "")
}

func TestSpecSample_MalwareScans(t *testing.T) {
	var r MalwareScanResults
	decodeSpecSample(t, "malware-scans", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	a := r.Data[0].Attributes
	requireSet(t, "malware-scans", "scanState", a.ScanState != "")
	requireSet(t, "malware-scans", "numberOfFilesScanned", a.NumberOfFilesScanned != 0)
	requireSet(t, "malware-scans", "numberOfFilesImpacted", a.NumberOfFilesImpacted != 0)
}

func TestSpecSample_CatalogImages(t *testing.T) {
	var r CatalogImagesCount
	decodeSpecSample(t, "catalog-images", &r)
	requireSet(t, "catalog-images", "meta.pagination.count", r.Meta.Pagination.Count != 0)
}

func TestSpecSample_Slos(t *testing.T) {
	var r Slos
	decodeSpecSample(t, "slos", &r)
	if len(r.Data) == 0 {
		t.Fatal("no data")
	}
	requireSet(t, "slos", "data[].id", r.Data[0].ID != "")
}
