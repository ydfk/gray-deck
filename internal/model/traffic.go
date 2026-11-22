package model

type TrafficBytes struct {
	Upload   uint64 `json:"upload"`
	Download uint64 `json:"download"`
}

type TrafficRow struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	TrafficBytes
}

type TrafficPeriod struct {
	ID      string `json:"id"`
	Start   string `json:"start"`
	End     string `json:"end"`
	Partial bool   `json:"partial"`
}

type TrafficReport struct {
	CurrentPeriod string          `json:"currentPeriod"`
	Period        TrafficPeriod   `json:"period"`
	Periods       []TrafficPeriod `json:"periods"`
	CycleStart    string          `json:"cycleStart"`
	CycleEnd      string          `json:"cycleEnd"`
	PeriodOpen    bool            `json:"periodOpen"`
	Retention     int             `json:"retentionMonths"`
	MaxSizeMiB    int             `json:"maxSizeMiB"`
	Timezone      string          `json:"timezone"`
	Sampling      bool            `json:"sampling"`
	LastSampleAt  string          `json:"lastSampleAt"`
	StorageError  bool            `json:"storageError"`
	Totals        TrafficBytes    `json:"totals"`
	Items         []TrafficRow    `json:"items"`
}
