package model

import "time"

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

type TrafficConnectionDetail struct {
	ID              string    `json:"id"`
	StartedAt       time.Time `json:"startedAt"`
	LastSeenAt      time.Time `json:"lastSeenAt"`
	SourceIP        string    `json:"sourceIP,omitempty"`
	InboundUser     string    `json:"inboundUser,omitempty"`
	Host            string    `json:"host,omitempty"`
	SniffHost       string    `json:"sniffHost,omitempty"`
	DestinationIP   string    `json:"destinationIP,omitempty"`
	DestinationPort string    `json:"destinationPort,omitempty"`
	Network         string    `json:"network,omitempty"`
	InboundType     string    `json:"inboundType,omitempty"`
	InboundName     string    `json:"inboundName,omitempty"`
	Node            string    `json:"node,omitempty"`
	Route           []string  `json:"route,omitempty"`
	Rule            string    `json:"rule,omitempty"`
	RulePayload     string    `json:"rulePayload,omitempty"`
	TrafficBytes
}

type TrafficConnectionReport struct {
	Disabled     bool                      `json:"disabled"`
	MaxSizeMiB   int                       `json:"maxSizeMiB"`
	StorageError bool                      `json:"storageError"`
	NextCursor   string                    `json:"nextCursor,omitempty"`
	Items        []TrafficConnectionDetail `json:"items"`
}

type TrafficAnalysisRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	TrafficBytes
}

type TrafficAccessRow struct {
	Connection TrafficConnectionDetail `json:"connection"`
	First      time.Time               `json:"first"`
	Last       time.Time               `json:"last"`
	TrafficBytes
}

type TrafficAnalysisReport struct {
	Totals    TrafficBytes         `json:"totals"`
	Timeline  []TrafficAnalysisRow `json:"timeline"`
	Protocols []TrafficAnalysisRow `json:"protocols"`
	Targets   []TrafficAnalysisRow `json:"targets"`
	Accesses  []TrafficAccessRow   `json:"accesses"`
}
