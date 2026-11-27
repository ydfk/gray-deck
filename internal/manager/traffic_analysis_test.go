package manager

import (
	"testing"
	"time"

	"mihomo-manager/internal/model"
)

func TestTrafficAnalysisDrillsIntoIPTimeProtocolAndTarget(t *testing.T) {
	at := time.Date(2026, 11, 27, 1, 58, 0, 0, time.UTC)
	collector := newDetailTestCollector(t, at, defaultTrafficPolicy(1))
	collector.record(at, nil)
	video := trafficConnection{ID: "video", Start: at.Add(time.Minute), Chains: []string{"Video node", "Auto"}}
	video.Metadata.SourceIP, video.Metadata.Network, video.Metadata.Host = "192.0.2.5", "tcp", "VIDEO.example"
	dns := trafficConnection{ID: "dns", Start: at.Add(3 * time.Minute)}
	dns.Metadata.SourceIP, dns.Metadata.Network, dns.Metadata.Host = "192.0.2.5", "udp", "dns.example"
	other := trafficConnection{ID: "other", Start: at.Add(3 * time.Minute)}
	other.Metadata.SourceIP = "192.0.2.6"

	video.Download = 100
	collector.record(at.Add(time.Minute), []trafficConnection{video})
	video.Download, dns.Upload, other.Download = 700, 10, 900
	collector.record(at.Add(3*time.Minute), []trafficConnection{video, dns, other})
	now := at.Add(4 * time.Minute)
	if err := collector.save(now); err != nil {
		t.Fatal(err)
	}
	query := TrafficAnalysisQuery{TrafficConnectionQuery: TrafficConnectionQuery{Kind: "ip", Subject: "192.0.2.5"}}
	month, err := collector.analysis(now, query)
	if err != nil || month.Totals != (model.TrafficBytes{Upload: 10, Download: 700}) ||
		len(month.Targets) != 2 || month.Targets[0].Key != "video.example" ||
		len(month.Protocols) != 2 || month.Protocols[0].Key != "tcp" ||
		len(month.Accesses) != 2 || month.Accesses[0].Connection.Node != "Video node" {
		t.Fatalf("IP 归因或用量排序不正确：%+v，%v", month, err)
	}
	query.Day = "2026-11-27"
	day, err := collector.analysis(now, query)
	if err != nil || len(day.Timeline) != 2 || day.Timeline[0].Download != 100 ||
		day.Timeline[1].Download != 600 || day.Timeline[1].Key != "2026-11-27T10:00:00+08:00" {
		t.Fatalf("小时分布错误：%+v，%v", day, err)
	}
	query.Hour, query.Protocol, query.Target = day.Timeline[1].Key, "tcp", "video.example"
	hour, err := collector.analysis(now, query)
	if err != nil || hour.Totals != (model.TrafficBytes{Download: 600}) || len(hour.Timeline) != 1 ||
		hour.Timeline[0].Label != "10:01" || len(hour.Accesses) != 1 ||
		hour.Accesses[0].Connection.Download != 700 || hour.Accesses[0].Download != 600 {
		t.Fatalf("协议、域名及所选时段的单连接用量错误：%+v，%v", hour, err)
	}
}
