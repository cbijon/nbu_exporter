package models

// TapeVolumePool is one entry from GET /storage/tape-volume-pools (API v12.0+).
type TapeVolumePool struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		VolumePoolName     string `json:"volumePoolName"`
		Description        string `json:"description"`
		PartiallyFullMedia int    `json:"partiallyFullMedia"`
		PoolType           string `json:"poolType"`
	} `json:"attributes"`
}

// TapeVolumePools is the paginated response from GET /storage/tape-volume-pools.
type TapeVolumePools struct {
	Data []TapeVolumePool `json:"data"`
	Meta struct {
		Pagination struct {
			Next int `json:"next"`
		} `json:"pagination"`
	} `json:"meta"`
}

// TapeDrive is one entry from GET /storage/drives (API v12.0+).
type TapeDrive struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		DriveType   string `json:"driveType"`
		RobotType   string `json:"robotType"`
		DriveStatus string `json:"driveStatus"`
	} `json:"attributes"`
}

// TapeDrives is the paginated response from GET /storage/drives.
type TapeDrives struct {
	Data []TapeDrive `json:"data"`
	Meta struct {
		Pagination struct {
			Next int `json:"next"`
		} `json:"pagination"`
	} `json:"meta"`
}

// TapeMediaVolume is one entry from GET /storage/tape-media (API v12.0+).
type TapeMediaVolume struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		MediaType  string  `json:"mediaType"`
		VolumePool string  `json:"volumePool"`
		Robot      struct {
			RobotType string `json:"robotType"`
		} `json:"robot"`
		MediaStatus string  `json:"mediaStatus"`
		KiloBytes   float64 `json:"kiloBytes"`
		Mounts      int     `json:"mounts"`
	} `json:"attributes"`
}

// TapeMedia is the paginated response from GET /storage/tape-media.
type TapeMedia struct {
	Data []TapeMediaVolume `json:"data"`
	Meta struct {
		Pagination struct {
			Next int `json:"next"`
		} `json:"pagination"`
	} `json:"meta"`
}
