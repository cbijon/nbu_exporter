package models

// Shapes below are verified against docs/veritas-10.5/storage.yaml and
// docs/veritas-11.0/storage.yaml (identical for these endpoints); NetBackup 11.1/11.2
// (API 14.0) ship no storage.yaml change for them.

// TapeDrives is the response from GET /storage/drives (JSON:API, offset-paginated).
// The drive name is the resource id (data[].id); attributes carry no driveName.
type TapeDrives struct {
	Data []struct {
		ID         string `json:"id"` // drive name
		Attributes struct {
			DriveStatus string `json:"driveStatus"` // DRIVE_STATUS_UP|DOWN|MIXED|DISABLED
			DriveType   string `json:"driveType"`   // DT_HCART, DT_DLT, ...
			RobotType   string `json:"robotType"`   // TLD, ACS, NOT_ROBOTIC, NA
			RobotNumber int    `json:"robotNumber"`
			DeviceHost  string `json:"deviceHost"`
			DevicePaths []struct {
				DeviceHost string `json:"deviceHost"`
				DriveName  string `json:"driveName"`
			} `json:"devicePaths"`
		} `json:"attributes"`
	} `json:"data"`
	Meta OffsetPagination `json:"meta"`
}

// TapeRobot is the robot a tape volume resides in (tape-media attributes.robot).
type TapeRobot struct {
	RobotType   string `json:"robotType"` // NA, NOT_ROBOTIC, ACS, TLD
	RobotNumber int    `json:"robotNumber"`
	RobotHost   string `json:"robotHost"`
}

// TapeMedia is the response from GET /storage/tape-media (JSON:API, cursor-paginated
// via page[after] and meta.pagination.next).
type TapeMedia struct {
	Data []struct {
		Attributes struct {
			Barcode     string    `json:"barcode"`
			MediaType   string    `json:"mediaType"`   // HCART, DLT, HC_CLN, ...
			VolumePool  string    `json:"volumePool"`  // e.g. "NetBackup", "Scratch"
			MediaStatus string    `json:"mediaStatus"` // free-form, e.g. "ACTIVE MULTIPLEXED"
			Robot       TapeRobot `json:"robot"`
		} `json:"attributes"`
	} `json:"data"`
	Meta CursorPagination `json:"meta"`
}

// RobotDeviceHosts is the response from GET /storage/robots-device-hosts (JSON:API).
// Each entry is a configured robot device host; data[].id is the hostname.
type RobotDeviceHosts struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// CursorPagination is the meta block of cursor-paginated endpoints (tape-media,
// tape-volume-pools, admin/jobs): next is an opaque string cursor, empty on the last page.
type CursorPagination struct {
	Pagination struct {
		Next string `json:"next"`
	} `json:"pagination"`
}

// OffsetPagination is the meta block of offset-paginated endpoints (drives,
// disk-pools): next is the offset of the next page; 0/absent on the last page.
type OffsetPagination struct {
	Pagination struct {
		Next   int `json:"next"`
		Offset int `json:"offset"`
		Last   int `json:"last"`
	} `json:"pagination"`
}

// TapeVolumePools is the response from GET /storage/tape-volume-pools (API v12.0+,
// cursor-paginated).
type TapeVolumePools struct {
	Data []struct {
		Attributes struct {
			VolumePoolName     string `json:"volumePoolName"`
			Description        string `json:"description"`
			PartiallyFullMedia int    `json:"partiallyFullMedia"`
			PoolType           string `json:"poolType"` // REGULAR_MEDIA_POOL, SCRATCH_MEDIA_POOL, CATALOG_MEDIA_POOL, AVAILABLE_MEDIA_POOL
		} `json:"attributes"`
	} `json:"data"`
	Meta CursorPagination `json:"meta"`
}

// DiskVolume is one volume entry nested inside a disk pool (API v12.0+).
type DiskVolume struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	State string `json:"state"` // UP / DOWN / UNKNOWN
}

// DiskPools is the response from GET /storage/disk-pools (API v12.0+, offset-paginated).
type DiskPools struct {
	Data []struct {
		Attributes struct {
			Name            string       `json:"name"`
			SType           string       `json:"sType"`
			StorageCategory string       `json:"storageCategory"` // ADVANCED_DISK / CLOUD / MSDP / OPEN_STORAGE
			DiskPoolState   string       `json:"diskPoolState"`   // UP / DOWN / TRANSIENT
			DiskVolumes     []DiskVolume `json:"diskVolumes"`
		} `json:"attributes"`
	} `json:"data"`
	Meta OffsetPagination `json:"meta"`
}
