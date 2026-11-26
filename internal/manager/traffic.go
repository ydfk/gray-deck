package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mihomo-manager/internal/model"
)

const (
	trafficSampleInterval = time.Second
	trafficSaveInterval   = 15 * time.Second
)

var (
	ErrTrafficUnavailable = errors.New("traffic history unavailable")
	ErrTrafficPeriod      = errors.New("traffic period not found")
)

type trafficConnection struct {
	ID       string    `json:"id"`
	Start    time.Time `json:"start"`
	Metadata struct {
		SourceIP        string          `json:"sourceIP"`
		InboundUser     string          `json:"inboundUser"`
		Type            string          `json:"type"`
		InboundName     string          `json:"inboundName"`
		Network         string          `json:"network"`
		Host            string          `json:"host"`
		SniffHost       string          `json:"sniffHost"`
		DestinationIP   string          `json:"destinationIP"`
		DestinationPort json.RawMessage `json:"destinationPort"`
	} `json:"metadata"`
	Chains      []string `json:"chains"`
	Rule        string   `json:"rule"`
	RulePayload string   `json:"rulePayload"`
	model.TrafficBytes
}

// 按本地日期分段、来源 IP、入站用户名记录用量，空用户名表示未认证的连接。
type trafficDay map[string]map[string]model.TrafficBytes

type trafficState struct {
	StartedAt     time.Time                     `json:"startedAt"`
	SampledAt     *time.Time                    `json:"sampledAt,omitempty"`
	PrunedThrough string                        `json:"prunedThrough,omitempty"`
	Days          map[string]trafficDay         `json:"days"`
	Active        map[string]model.TrafficBytes `json:"active,omitempty"`
}

type trafficCollector struct {
	sampleMu     sync.Mutex
	mu           sync.RWMutex
	path         string
	location     *time.Location
	policy       trafficPolicy
	client       *http.Client
	state        trafficState
	details      *trafficDetailStore
	lastSampleAt time.Time
	newSince     time.Time
	sampleFailed bool
	dirty        bool
	storageError bool
}

func trafficCycleDay(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 1, nil
	}
	day, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || day < 1 || day > 31 {
		return 1, errors.New("GRAYDECK_TRAFFIC_CYCLE_DAY 必须是 1 到 31 的整数，已使用 1 日")
	}
	return day, nil
}

func openTrafficCollector(path string, location *time.Location, policy trafficPolicy) (*trafficCollector, error) {
	collector := &trafficCollector{
		path:     path,
		location: location,
		policy:   policy,
		client: &http.Client{
			Timeout: 2 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	content, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(content, &collector.state); err != nil {
			return nil, err
		}
		if collector.state.StartedAt.IsZero() {
			return nil, errors.New("历史文件缺少采集起始时间")
		}
		collector.newSince = collector.state.StartedAt
		if collector.state.SampledAt != nil {
			collector.newSince = *collector.state.SampledAt
		}
		for key, bucket := range collector.state.Days {
			if len(key) == len(time.DateOnly) {
				collector.state.Days[key+"T00:00"] = bucket
				delete(collector.state.Days, key)
			}
		}
		collector.dirty = len(content) > policy.maxBytes
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if collector.state.Days == nil {
		collector.state.Days = make(map[string]trafficDay)
	}
	if collector.state.Active == nil {
		collector.state.Active = make(map[string]model.TrafficBytes)
	}
	return collector, nil
}

func (s *Service) initTrafficCollector() {
	day, err := trafficCycleDay(os.Getenv("GRAYDECK_TRAFFIC_CYCLE_DAY"))
	if err != nil {
		s.appendLog(err.Error())
	}
	policy, policyErr := loadTrafficPolicy(s.cfg.AppConfigPath, defaultTrafficPolicy(day))
	if policyErr != nil {
		s.appendLogf("流量策略配置无效，流量采集已暂停：%v", policyErr)
		return
	}
	s.traffic, err = openTrafficCollector(filepath.Join(s.cfg.DataDir, "traffic-history.json"), localTimeLocation(), policy)
	if err != nil {
		s.appendLogf("无法读取流量历史：%v", err)
		return
	}
	s.traffic.details, err = openTrafficDetails(filepath.Join(s.cfg.DataDir, "traffic-connections.jsonl"), policy, time.Now())
	if err != nil {
		s.appendLogf("无法读取连接明细：%v", err)
	}
}

func (s *Service) trafficLoop() {
	if s.traffic == nil {
		return
	}
	sampleTicker := time.NewTicker(trafficSampleInterval)
	defer sampleTicker.Stop()
	saveTicker := time.NewTicker(trafficSaveInterval)
	defer saveTicker.Stop()

	for {
		select {
		case <-s.syncStopCh:
			s.saveTraffic()
			return
		case <-sampleTicker.C:
			s.sampleTraffic()
		case <-saveTicker.C:
			s.saveTraffic()
		}
	}
}

func (s *Service) sampleTraffic() {
	if s.traffic == nil {
		return
	}
	s.traffic.sampleMu.Lock()
	defer s.traffic.sampleMu.Unlock()
	if !s.trafficCoreRunning() {
		return
	}
	values := s.loadManagedRuntimeValues()
	s.traffic.sample(controllerURL(values.controller), values.secret)
}

func (s *Service) trafficCoreRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.coreCmd != nil
}

func (c *trafficCollector) sample(controller, secret string) {
	at := time.Now()
	snapshot, err := c.fetchSnapshot(controller, secret)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sampleFailed = err != nil
	if err != nil {
		return
	}
	c.record(at, snapshot)
}

func (c *trafficCollector) fetchSnapshot(controller, secret string) ([]trafficConnection, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(controller, "/")+"/connections", nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("连接统计请求失败：HTTP %d", response.StatusCode)
	}
	var snapshot struct {
		Connections json.RawMessage `json:"connections"`
	}
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		return nil, err
	}
	var connections []trafficConnection
	if err := json.Unmarshal(snapshot.Connections, &connections); err != nil {
		return nil, err
	}
	return connections, nil
}

func (c *trafficCollector) record(at time.Time, connections []trafficConnection) {
	c.lastSampleAt = at
	if c.state.StartedAt.IsZero() {
		c.state.StartedAt = at.UTC()
		c.dirty = true
	}
	bucket := c.policy.bucket(at.In(c.location))
	seen := make(map[string]struct{}, len(connections))
	for _, conn := range connections {
		if _, duplicate := seen[conn.ID]; duplicate || conn.ID == "" || conn.Metadata.Type == "Inner" {
			continue
		}
		seen[conn.ID] = struct{}{}
		ip := trafficIP(conn.Metadata.SourceIP)
		if c.details != nil {
			c.details.observe(at, conn, ip)
		}
		last, known := c.state.Active[conn.ID]
		// 已经在运行且没有检查点的连接先记录基线，避免重复累计。
		latest := model.TrafficBytes{
			Upload:   max(last.Upload, conn.Upload),
			Download: max(last.Download, conn.Download),
		}
		if !known || latest != last {
			c.state.Active[conn.ID] = latest
			c.dirty = true
		}
		if !known && (conn.Start.Before(c.state.StartedAt) || conn.Start.Before(c.newSince)) {
			continue
		}
		delta := model.TrafficBytes{Upload: latest.Upload - last.Upload, Download: latest.Download - last.Download}
		if delta != (model.TrafficBytes{}) {
			c.addUsage(bucket, ip, conn.Metadata.InboundUser, delta)
		}
	}
	for id := range c.state.Active {
		if _, active := seen[id]; !active {
			delete(c.state.Active, id)
			c.dirty = true
		}
	}
}

func trafficIP(raw string) string {
	if addr, err := netip.ParseAddr(raw); err == nil {
		addr = addr.Unmap()
		if !addr.IsUnspecified() {
			return addr.String()
		}
	}
	return ""
}

func (c *trafficCollector) addUsage(day, ip, user string, delta model.TrafficBytes) {
	if c.state.Days[day] == nil {
		c.state.Days[day] = make(trafficDay)
	}
	bucket := c.state.Days[day]
	if bucket[ip] == nil {
		bucket[ip] = make(map[string]model.TrafficBytes)
	}
	total := bucket[ip][user]
	total.Upload += delta.Upload
	total.Download += delta.Download
	bucket[ip][user] = total
}
