package manager

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"mihomo-manager/internal/model"
)

func TestTrafficUsageSurvivesRestartAndRollsOverAtLocalMidnight(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	path := filepath.Join(t.TempDir(), "traffic-history.json")
	first := time.Date(2026, 11, 30, 23, 59, 50, 0, zone)
	before := first.Add(8 * time.Second).UTC()
	after := first.Add(12 * time.Second).UTC()
	collector, err := openTrafficCollector(t, path, zone, defaultTrafficPolicy(1))
	if err != nil {
		t.Fatal(err)
	}

	alice := trafficConnection{ID: "long-lived", Start: first.Add(-time.Hour)}
	alice.Metadata.SourceIP = "::ffff:192.0.2.1"
	alice.Metadata.InboundUser = "alice"
	alice.TrafficBytes = model.TrafficBytes{Upload: 1000, Download: 2000}
	collector.record(first, []trafficConnection{alice})

	alice.TrafficBytes = model.TrafficBytes{Upload: 1100, Download: 2200}
	second := trafficConnection{ID: "second", Start: first.Add(time.Second), TrafficBytes: model.TrafficBytes{Download: 30}}
	second.Metadata.SourceIP = "2001:db8::2"
	second.Metadata.InboundUser = "alice"
	guest := trafficConnection{ID: "guest", Start: first.Add(time.Second), TrafficBytes: model.TrafficBytes{Upload: 50, Download: 10}}
	guest.Metadata.SourceIP = "192.0.2.1"
	collector.record(before, []trafficConnection{alice, second, guest})
	collector.record(before, []trafficConnection{alice, second, guest})
	if err := collector.save(before); err != nil {
		t.Fatal(err)
	}

	collector, err = openTrafficCollector(t, path, zone, defaultTrafficPolicy(1))
	if err != nil {
		t.Fatal(err)
	}
	alice.TrafficBytes = model.TrafficBytes{Upload: 1500, Download: 3000}
	collector.record(after, []trafficConnection{alice})
	if err := collector.save(after); err != nil {
		t.Fatal(err)
	}
	collector, err = openTrafficCollector(t, path, zone, defaultTrafficPolicy(1))
	if err != nil {
		t.Fatal(err)
	}

	history, err := collector.report(after, "2026-11-01", true, false)
	if err != nil {
		t.Fatal(err)
	}
	expectedUsers := []model.TrafficRow{
		{Kind: "user", Name: "alice", TrafficBytes: model.TrafficBytes{Upload: 100, Download: 230}},
		{Kind: "ip", Name: "192.0.2.1", TrafficBytes: model.TrafficBytes{Upload: 50, Download: 10}},
	}
	if !reflect.DeepEqual(history.Items, expectedUsers) || history.CurrentPeriod != "2026-12-01" || len(history.Periods) != 2 {
		t.Fatalf("历史记录错误：%+v", history)
	}
	byIP, err := collector.report(after, "2026-11-01", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(byIP.Items) != 2 || byIP.Items[0] != (model.TrafficRow{
		Kind: "ip", Name: "192.0.2.1", TrafficBytes: model.TrafficBytes{Upload: 150, Download: 210},
	}) || byIP.Totals != history.Totals {
		t.Fatalf("按 IP 统计错误：%+v", byIP)
	}
	current, err := collector.report(after, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if current.Period.ID != "2026-12-01" || current.Totals != (model.TrafficBytes{Upload: 400, Download: 800}) {
		t.Fatalf("跨月增量错误：%+v", current)
	}

	collector.policy = defaultTrafficPolicy(15)
	regrouped, err := collector.report(after, "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if regrouped.Period.ID != "2026-11-15" || regrouped.Totals != (model.TrafficBytes{Upload: 550, Download: 1040}) {
		t.Fatalf("调整起算日后的汇总有误：%+v", regrouped)
	}
}

func TestTrafficCycleClampsToLastDayOfShortMonth(t *testing.T) {
	at := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	collector, err := openTrafficCollector(t, filepath.Join(t.TempDir(), "traffic-history.json"), time.UTC, defaultTrafficPolicy(31))
	if err != nil {
		t.Fatal(err)
	}
	collector.record(at.Add(-time.Second), nil)
	report, err := collector.report(at, "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.TrafficPeriod{
		{ID: "2024-02-29", Start: "2024-02-29 00:00", End: "2024-03-31 00:00"},
		{ID: "2024-01-31", Start: "2024-01-31 00:00", End: "2024-02-29 00:00"},
	}
	if !reflect.DeepEqual(report.Periods, want) {
		t.Fatalf("月底周期不正确：%+v", report.Periods)
	}
	if _, err := collector.report(at, "2024-02-01", true, false); !errors.Is(err, ErrTrafficPeriod) {
		t.Fatalf("无效周期应返回 ErrTrafficPeriod：%v", err)
	}
}

func TestTrafficSamplingReadsMihomoConnectionsAndRetainsBaselineOnError(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/connections" || req.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, body.Load())
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "traffic-history.json")
	collector, err := openTrafficCollector(t, path, time.UTC, defaultTrafficPolicy(1))
	if err != nil {
		t.Fatal(err)
	}
	// 模拟已采集一段时间，首次出现的当期连接可以计入已有字节。
	collector.state.StartedAt = start.Add(-time.Minute)
	snapshot := func(download int) string {
		return fmt.Sprintf(`{"connections":[{"id":"live","start":%q,"metadata":{"sourceIP":"2001:db8::1","inboundUser":"bob"},"upload":12,"download":%d}]}`,
			time.Now().Format(time.RFC3339Nano), download)
	}
	body.Store(snapshot(60))
	collector.sample(server.URL, "test-secret")
	body.Store(`{"unexpected":true}`)
	collector.sample(server.URL, "test-secret")
	if !collector.sampleFailed || collector.lastSampleAt.IsZero() {
		t.Fatal("采集失败应保留上次的采样时间")
	}
	body.Store(snapshot(100))
	collector.sample(server.URL, "test-secret")
	report, err := collector.report(time.Now(), "", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Sampling || report.Totals != (model.TrafficBytes{Upload: 12, Download: 100}) || len(report.Items) != 1 || report.Items[0].Name != "bob" {
		t.Fatalf("采样失败后累计错误：%+v", report)
	}
}

func TestTrafficHistoryReadErrorDoesNotOverwriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-history.json")
	const original = `{"days":{`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openTrafficCollector(t, path, time.UTC, defaultTrafficPolicy(1)); err == nil {
		t.Fatal("无效历史文件应报告错误")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatal("无效历史文件应保留原内容")
	}
}
