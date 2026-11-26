package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mihomo-manager/internal/model"
)

func newDetailTestCollector(t *testing.T, at time.Time, policy trafficPolicy) *trafficCollector {
	t.Helper()
	root := t.TempDir()
	collector, err := openTrafficCollector(filepath.Join(root, "traffic-history.json"), time.FixedZone("UTC+8", 8*3600), policy)
	if err != nil {
		t.Fatal(err)
	}
	collector.details, err = openTrafficDetails(filepath.Join(root, "traffic-connections.jsonl"), policy, at)
	if err != nil {
		t.Fatal(err)
	}
	return collector
}

func TestTrafficConnectionsRecordMihomoDestinationAndRoute(t *testing.T) {
	at := time.Date(2026, 11, 27, 2, 0, 0, 0, time.UTC)
	policy := defaultTrafficPolicy(1)
	collector := newDetailTestCollector(t, at, policy)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/connections" || req.Header.Get("Authorization") != "Bearer test-controller" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"connections":[
			{"id":"detail-id","start":"2026-11-27T01:59:59Z","metadata":{
				"sourceIP":"::ffff:192.0.2.8","inboundUser":"viewer","type":"Tun","network":"tcp","inboundName":"office",
				"host":"video.example.test","sniffHost":"cdn.example.test","destinationIP":"2001:db8::4","destinationPort":"443"
			},"chains":["Edge-1","Video","AUTO"],"rule":"RuleSet","rulePayload":"streaming","upload":18,"download":400},
			{"id":"direct-id","start":"2026-11-27T01:59:57Z","metadata":{"destinationPort":53,"network":"udp"},"chains":["DIRECT"],"rule":"Match"}
		]}`)
	}))
	defer server.Close()
	snapshot, err := collector.fetchSnapshot(server.URL, "test-controller")
	if err != nil {
		t.Fatal(err)
	}
	collector.record(at, snapshot)
	snapshot[0].Download = 600
	collector.record(at.Add(time.Second), snapshot[:1])
	collector.record(at.Add(2*time.Second), nil)
	if err := collector.saveDetails(at.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	collector.details, err = openTrafficDetails(collector.details.path, policy, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report, err := collector.connectionReport(at, TrafficConnectionQuery{ByUser: true, Kind: "user", Subject: "viewer", Search: "CDN.EXAMPLE"})
	if err != nil || len(report.Items) != 1 {
		t.Fatalf("连接明细查找失败：%+v，%v", report, err)
	}
	item := report.Items[0]
	if item.Host != "video.example.test" || item.DestinationIP != "2001:db8::4" || item.DestinationPort != "443" ||
		item.SourceIP != "192.0.2.8" || item.Network != "tcp" || item.InboundType != "Tun" || item.InboundName != "office" ||
		item.Node != "Edge-1" || !reflect.DeepEqual(item.Route, []string{"AUTO", "Video", "Edge-1"}) ||
		item.Rule != "RuleSet" || item.RulePayload != "streaming" || item.Upload != 18 || item.Download != 600 ||
		item.StartedAt.Format(time.RFC3339) != "2026-11-27T09:59:59+08:00" || !item.LastSeenAt.Equal(at.Add(time.Second)) {
		t.Fatalf("连接信息不正确：%+v", item)
	}
	direct, err := collector.connectionReport(at, TrafficConnectionQuery{ByUser: true, Search: "direct"})
	if err != nil || len(direct.Items) != 1 || direct.Items[0].DestinationPort != "53" {
		t.Fatalf("直连或数字端口不正确：%+v，%v", direct, err)
	}
}

func TestTrafficConnectionsCanBrowseAllRetainedAndUseStableCursor(t *testing.T) {
	at := time.Date(2026, 11, 30, 18, 0, 0, 0, time.UTC)
	collector := newDetailTestCollector(t, at, defaultTrafficPolicy(1))
	for index := 0; index < 55; index++ {
		conn := trafficConnection{ID: fmt.Sprintf("connection-%03d", index), Start: at.Add(-time.Second)}
		conn.Metadata.SourceIP = "192.0.2.8"
		collector.record(at, []trafficConnection{conn})
	}
	collector.record(at, []trafficConnection{{ID: "previous-cycle", Start: at.Add(-3 * time.Hour)}})
	query := TrafficConnectionQuery{ByUser: true, Kind: "ip", Subject: "192.0.2.8"}
	first, err := collector.connectionReport(at, query)
	if err != nil || len(first.Items) != 50 || first.Items[0].ID != "connection-054" || first.NextCursor == "" {
		t.Fatalf("第一页错误：%+v，%v", first, err)
	}
	newer := trafficConnection{ID: "new-connection", Start: at}
	newer.Metadata.SourceIP = "192.0.2.8"
	collector.record(at.Add(time.Second), []trafficConnection{newer})
	query.Cursor = first.NextCursor
	second, err := collector.connectionReport(at.Add(time.Second), query)
	if err != nil || len(second.Items) != 5 || second.Items[0].ID != "connection-004" || second.NextCursor != "" {
		t.Fatalf("新增连接导致分页重叠或漏记：%+v，%v", second, err)
	}
	all, err := collector.connectionReport(at, TrafficConnectionQuery{Period: "all", Kind: "unknown"})
	if err != nil || len(all.Items) != 1 || all.Items[0].ID != "previous-cycle" {
		t.Fatalf("无法浏览当前周期外的保留记录：%+v，%v", all, err)
	}
	if _, err := collector.connectionReport(at, TrafficConnectionQuery{Cursor: "bad cursor"}); !errors.Is(err, ErrTrafficDetailQuery) {
		t.Fatalf("无效游标应被拒绝：%v", err)
	}
}

func TestTrafficDetailJournalCompactsAndRecoversIncompleteTail(t *testing.T) {
	at := time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)
	policy := defaultTrafficPolicy(1)
	policy.detailDays = 1
	policy.detailMaxBytes = 750
	collector := newDetailTestCollector(t, at, policy)
	store := collector.details
	for index := 1; index <= 4; index++ {
		conn := trafficConnection{ID: fmt.Sprintf("record-%d", index), Start: at.Add(-time.Hour)}
		conn.Metadata.Host = strings.Repeat("x", 155)
		store.observe(at.Add(time.Duration(index)*time.Minute), conn, "192.0.2.1")
	}
	if err := store.flush(at.Add(5 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture) > policy.detailMaxBytes || len(store.records) != 2 || store.records["record-4"].ID == "" || store.records["record-3"].ID == "" {
		t.Fatalf("文件未优先保留最新记录，文件大小 %d，记录数 %d", len(fixture), len(store.records))
	}
	if err := appendTrafficJournal(store.path, []byte(`{"id":"unfinished"`)); err != nil {
		t.Fatal(err)
	}
	recovered, err := openTrafficDetails(store.path, policy, at.Add(5*time.Minute))
	if err != nil || len(recovered.records) != 2 {
		t.Fatalf("重启后没有恢复完整记录：%v", err)
	}
	if err := recovered.flush(at.Add(5 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := recovered.flush(at.Add(25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.path)
	if err != nil || info.Size() != 0 || len(recovered.records) != 0 {
		t.Fatalf("没有清理到期明细：%v", err)
	}
}

func TestTrafficDetailsDoNotAffectSummaryWhenDisabledOrOnSaveError(t *testing.T) {
	at := time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)
	policy := defaultTrafficPolicy(1)
	collector := newDetailTestCollector(t, at, policy)
	collector.details.path = filepath.Join(t.TempDir(), "missing", "details.jsonl")
	collector.record(at.Add(-time.Second), nil)
	conn := trafficConnection{ID: "detail-id", Start: at, TrafficBytes: model.TrafficBytes{Download: 150}}
	conn.Metadata.SourceIP = "192.0.2.8"
	collector.record(at, []trafficConnection{conn})
	if err := collector.saveDetails(at); err == nil {
		t.Fatal("明细目录不可写时应报告失败")
	}
	if err := collector.save(at); err != nil {
		t.Fatalf("明细保存失败影响了月度汇总：%v", err)
	}
	summary, err := collector.report(at, "", false, false)
	if err != nil || summary.Totals.Download != 150 {
		t.Fatalf("原有流量汇总错误：%+v，%v", summary, err)
	}
	file := filepath.Join(t.TempDir(), "detail.jsonl")
	content, err := json.Marshal(collector.details.records[conn.ID])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(content, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	policy.detailDays = 0
	disabled, err := openTrafficDetails(file, policy, at)
	if err != nil || disabled != nil {
		t.Fatalf("停用时不应加载明细：%v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("停用不应移除已有文件：%v", err)
	}
}
