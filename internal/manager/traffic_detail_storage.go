package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mihomo-manager/internal/model"
)

const (
	maxTrafficDetails        = 10000
	trafficDetailPrunePeriod = 15 * time.Minute
)

type trafficDetailStore struct {
	path         string
	days         int
	maxBytes     int
	writtenBytes int64
	records      map[string]model.TrafficConnectionDetail
	pending      map[string]struct{}
	rewrite      bool
	removed      bool
	nextPrune    time.Time
	storageError bool
}

func openTrafficDetails(path string, policy trafficPolicy, now time.Time) (*trafficDetailStore, error) {
	if policy.detailDays == 0 {
		return nil, nil
	}
	store := &trafficDetailStore{
		path: path, days: policy.detailDays, maxBytes: policy.detailMaxBytes,
		records: make(map[string]model.TrafficConnectionDetail),
		pending: make(map[string]struct{}),
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	store.writtenBytes = info.Size()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 128*1024)
	scanner.Split(completeTrafficLine)
	var read int64
	for scanner.Scan() {
		line := scanner.Bytes()
		read += int64(len(line) + 1)
		var record model.TrafficConnectionDetail
		if err := json.Unmarshal(line, &record); err != nil || record.ID == "" || record.StartedAt.IsZero() || record.LastSeenAt.IsZero() {
			return nil, fmt.Errorf("连接明细文件在字节 %d 附近存在无效记录", read)
		}
		if record.LastSeenAt.Before(store.cutoff(now)) {
			store.rewrite = true
			continue
		}
		store.records[record.ID] = record
		if len(store.records) > 2*maxTrafficDetails {
			store.prune(now)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	store.rewrite = store.rewrite || read != info.Size() || info.Size() > int64(store.maxBytes)
	store.prune(now)
	return store, nil
}

// 崩溃时可能留下半条记录，只重放带换行符的完整记录。
func completeTrafficLine(data []byte, atEOF bool) (int, []byte, error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index], nil
	}
	if atEOF {
		return len(data), nil, nil
	}
	return 0, nil, nil
}

func (d *trafficDetailStore) observe(at time.Time, conn trafficConnection, ip string) {
	if len(conn.ID) > 128 {
		return
	}
	previous, exists := d.records[conn.ID]
	start := conn.Start
	if exists {
		start = previous.StartedAt
	} else if start.IsZero() {
		start = at
	}
	if start.After(at) {
		at = start
	}
	record := model.TrafficConnectionDetail{
		ID: conn.ID, StartedAt: start.UTC(), LastSeenAt: at.UTC(),
		SourceIP: ip, InboundUser: detailText(conn.Metadata.InboundUser),
		Host: detailText(conn.Metadata.Host), SniffHost: detailText(conn.Metadata.SniffHost),
		DestinationIP: trafficIP(conn.Metadata.DestinationIP), DestinationPort: detailPort(conn.Metadata.DestinationPort),
		Network: detailText(conn.Metadata.Network), InboundType: detailText(conn.Metadata.Type), InboundName: detailText(conn.Metadata.InboundName),
		Rule: detailText(conn.Rule), RulePayload: detailText(conn.RulePayload),
		TrafficBytes: model.TrafficBytes{
			Upload: max(previous.Upload, conn.Upload), Download: max(previous.Download, conn.Download),
		},
	}
	if len(conn.Chains) > 0 {
		// Mihomo 从节点向外记录策略组，展示时按策略组到节点排列。
		record.Node = detailText(conn.Chains[0])
		record.Route = make([]string, 0, min(len(conn.Chains), 24))
		for index := min(len(conn.Chains), 24) - 1; index >= 0; index-- {
			if name := detailText(conn.Chains[index]); name != "" {
				record.Route = append(record.Route, name)
			}
		}
	}
	d.records[conn.ID] = record
	d.pending[conn.ID] = struct{}{}
	if len(d.records) > 2*maxTrafficDetails {
		d.prune(at)
	}
}

func detailText(value string) string {
	const maxFieldBytes = 512
	if len(value) <= maxFieldBytes {
		return value
	}
	end := maxFieldBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func detailPort(raw json.RawMessage) string {
	value, err := strconv.ParseUint(strings.Trim(string(raw), `"`), 10, 16)
	if err != nil || value == 0 {
		return ""
	}
	return strconv.FormatUint(value, 10)
}

func (d *trafficDetailStore) cutoff(now time.Time) time.Time {
	return now.Add(-time.Duration(d.days) * 24 * time.Hour)
}

func (d *trafficDetailStore) prune(now time.Time) {
	cutoff := d.cutoff(now)
	for id, record := range d.records {
		if record.LastSeenAt.Before(cutoff) {
			d.remove(id)
		}
	}
	if len(d.records) > maxTrafficDetails {
		for _, record := range d.latest()[maxTrafficDetails:] {
			d.remove(record.ID)
		}
	}
}

func (d *trafficDetailStore) remove(id string) {
	delete(d.records, id)
	delete(d.pending, id)
	d.removed = true
}

func (d *trafficDetailStore) latest() []model.TrafficConnectionDetail {
	records := make([]model.TrafficConnectionDetail, 0, len(d.records))
	for _, item := range d.records {
		records = append(records, item)
	}
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.LastSeenAt.Equal(b.LastSeenAt) {
			return a.ID > b.ID
		}
		return a.LastSeenAt.After(b.LastSeenAt)
	})
	return records
}

func (c *trafficCollector) saveDetails(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.details == nil {
		return nil
	}
	err := c.details.flush(at)
	c.details.storageError = err != nil
	return err
}

func (d *trafficDetailStore) flush(at time.Time) error {
	d.prune(at)
	// 批量清理过期记录，避免有持续流量时每隔几秒重写整个文件。
	if d.rewrite || (d.removed && !at.Before(d.nextPrune)) {
		return d.compact(at)
	}
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	for id := range d.pending {
		if err := encoder.Encode(d.records[id]); err != nil {
			return err
		}
		if int64(payload.Len())+d.writtenBytes > int64(d.maxBytes) {
			return d.compact(at)
		}
	}
	if payload.Len() == 0 {
		return nil
	}
	if err := appendTrafficJournal(d.path, payload.Bytes()); err != nil {
		d.rewrite = true
		return err
	}
	d.writtenBytes += int64(payload.Len())
	clear(d.pending)
	return nil
}

func (d *trafficDetailStore) compact(at time.Time) error {
	var payload []byte
	latest := d.latest()
	kept := 0
	for _, record := range latest {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if len(payload)+len(line)+1 > d.maxBytes {
			break
		}
		payload = append(payload, line...)
		payload = append(payload, '\n')
		kept++
	}
	if err := writeTrafficState(d.path, payload); err != nil {
		return err
	}
	for _, record := range latest[kept:] {
		delete(d.records, record.ID)
	}
	clear(d.pending)
	d.writtenBytes = int64(len(payload))
	d.rewrite = false
	d.removed = false
	d.nextPrune = at.Add(trafficDetailPrunePeriod)
	return nil
}

func appendTrafficJournal(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(payload)
	var syncErr error
	if writeErr == nil {
		syncErr = file.Sync()
	}
	return errors.Join(writeErr, syncErr, file.Close())
}
