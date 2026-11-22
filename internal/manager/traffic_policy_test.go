package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mihomo-manager/internal/model"
)

func TestTrafficCustomWindowUsesBothLocalTimes(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	policy := defaultTrafficPolicy(1)
	policy.start, policy.end = trafficClock{15, 8, 30}, trafficClock{10, 18, 0}
	collector, err := openTrafficCollector(filepath.Join(t.TempDir(), "traffic-history.json"), zone, policy)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 15, 8, 30, 0, 0, zone)
	end := time.Date(2026, 11, 10, 18, 0, 0, 0, zone)
	conn := trafficConnection{ID: "stream", Start: start.Add(-time.Hour)}
	conn.Metadata.SourceIP = "192.0.2.5"
	for _, sample := range []struct {
		at       time.Time
		download uint64
	}{
		{start.Add(-2 * time.Second), 200},
		{start.Add(-time.Second), 217},
		{start, 240},
		{end.Add(-time.Second), 270},
		{end, 370},
	} {
		conn.Download = sample.download
		collector.record(sample.at.UTC(), []trafficConnection{conn})
	}
	report, err := collector.report(end.UTC(), "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Download != 53 || report.PeriodOpen ||
		report.Period.Start != "2026-10-15 08:30" || report.Period.End != "2026-11-10 18:00" {
		t.Fatalf("起止时刻或截止边界用量错误：%+v", report)
	}
	next := time.Date(2026, 11, 15, 8, 30, 0, 0, zone)
	conn.Download = 389
	collector.record(next.UTC(), []trafficConnection{conn})
	report, err = collector.report(next.UTC(), "", false, true)
	if err != nil || !report.PeriodOpen || report.Totals.Download != 19 {
		t.Fatalf("新周期错误：%+v，%v", report, err)
	}
}

func TestTrafficPolicyLoadsSameMonthWindowAndLegacyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graydeck.yaml")
	content := "traffic:\n  start: '3 06:30'\n  end: '21 22:45'\n  retention-months: 6\n  max-size-mib: 4\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := loadTrafficPolicy(path, defaultTrafficPolicy(8))
	if err != nil {
		t.Fatal(err)
	}
	month := trafficMonth(time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC))
	start, end := policy.bounds(month)
	if start.Format(trafficKeyLayout) != "2026-06-03T06:30" || end.Format(trafficKeyLayout) != "2026-06-21T22:45" ||
		policy.retentionMonths != 6 || policy.maxBytes != 4*trafficMiB {
		t.Fatalf("配置加载错误：%s，%s，%+v", start, end, policy)
	}
	if err := os.WriteFile(path, []byte("traffic: {retention-months: 24}"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err = loadTrafficPolicy(path, defaultTrafficPolicy(8))
	if err != nil || policy.start.day != 8 || policy.end != policy.start || policy.maxBytes != defaultTrafficSizeMiB*trafficMiB {
		t.Fatalf("旧起算日或默认设置失效：%+v，%v", policy, err)
	}
	if _, err := parseTrafficClock("31 24:00"); err == nil {
		t.Fatal("无效时刻应返回错误")
	}
}

func TestTrafficRetentionPrunesExpiredLegacyDaysWithoutNewSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-history.json")
	fixture := `{"startedAt":"2026-02-01T00:00:00Z","days":{
		"2026-04-30":{"192.0.2.1":{"":{"download":100}}},
		"2026-05-01":{"192.0.2.1":{"":{"download":200}}},
		"2026-06-01":{"192.0.2.1":{"":{"download":300}}}
	}}`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := defaultTrafficPolicy(1)
	policy.retentionMonths = 2
	collector, err := openTrafficCollector(path, time.UTC, policy)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := collector.save(at); err != nil {
		t.Fatal(err)
	}
	reloaded, err := openTrafficCollector(path, time.UTC, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.state.Days) != 2 || reloaded.state.PrunedThrough != "2026-04-30T00:00" {
		t.Fatalf("历史期限清理不正确：%+v", reloaded.state)
	}
	report, err := reloaded.report(at, "2026-05-01", false, false)
	if err != nil || report.Totals.Download != 200 || len(report.Periods) != 2 {
		t.Fatalf("保留周期查询错误：%+v，%v", report, err)
	}
	if _, err := reloaded.report(at, "2026-04-01", false, false); !errors.Is(err, ErrTrafficPeriod) {
		t.Fatalf("过期周期不应继续出现在查询中：%v", err)
	}
}

func TestTrafficFileCapEvictsOldestBucketsAndMarksPartialPeriod(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-history.json")
	policy := defaultTrafficPolicy(1)
	policy.maxBytes = 800
	collector, err := openTrafficCollector(path, time.UTC, policy)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	collector.record(at.AddDate(0, -2, 0), nil)
	for index := 1; index <= 4; index++ {
		key := fmt.Sprintf("2026-06-%02dT00:00", index)
		collector.state.Days[key] = trafficDay{"192.0.2.1": {strings.Repeat("u", 220): {Download: uint64(index)}}}
	}
	if err := collector.save(at); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > policy.maxBytes {
		t.Fatalf("文件写入突破上限：%d", len(content))
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("保存后应清理临时文件：%v", err)
	}
	reloaded, err := openTrafficCollector(path, time.UTC, policy)
	if err != nil {
		t.Fatal(err)
	}
	report, err := reloaded.report(at, "", true, false)
	if err != nil || !report.Period.Partial || len(report.Periods) != 1 || report.Totals.Download != 7 {
		t.Fatalf("超限后未优先保留最近的记录：%+v，%v", report, err)
	}
}

func TestTrafficOversizedBaselinesCannotRecountOldConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-history.json")
	policy := defaultTrafficPolicy(1)
	policy.maxBytes = 1000
	collector, err := openTrafficCollector(path, time.UTC, policy)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	collector.record(at.Add(-time.Minute), nil)
	connections := make([]trafficConnection, 15)
	for index := range connections {
		connections[index] = trafficConnection{
			ID: fmt.Sprintf("connection-%02d", index), Start: at.Add(-time.Second), TrafficBytes: model.TrafficBytes{Download: 10},
		}
	}
	collector.record(at, connections)
	if err := collector.save(at); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted trafficState
	if err := json.Unmarshal(content, &persisted); err != nil || len(persisted.Active) != 0 ||
		persisted.SampledAt == nil || !persisted.SampledAt.Equal(at) {
		t.Fatalf("大流量连接快照未降级为时间检查点：%+v，%v", persisted, err)
	}
	reloaded, err := openTrafficCollector(path, time.UTC, policy)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.record(at.Add(time.Second), connections)
	connections[0].Download = 20
	reloaded.record(at.Add(2*time.Second), connections)
	report, err := reloaded.report(at.Add(2*time.Second), "", false, true)
	if err != nil || report.Totals.Download != 160 {
		t.Fatalf("重启后已累计流量被重复计数：%+v，%v", report, err)
	}
}
