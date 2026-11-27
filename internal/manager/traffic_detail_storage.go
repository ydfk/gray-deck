package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mihomo-manager/internal/model"
)

type trafficObservation struct {
	detail  model.TrafficConnectionDetail
	minutes map[int64]model.TrafficBytes
}

func (d *trafficDetailStore) observe(at time.Time, conn trafficConnection, ip string, latest, delta model.TrafficBytes) {
	if len(conn.ID) > 128 {
		return
	}
	observation := d.pending[conn.ID]
	if observation == nil {
		observation = &trafficObservation{}
		d.pending[conn.ID] = observation
	}
	start := conn.Start
	if start.IsZero() {
		start = at
	}
	detail := model.TrafficConnectionDetail{
		ID: conn.ID, StartedAt: start.UTC(), LastSeenAt: at.UTC(),
		SourceIP: ip, InboundUser: detailText(conn.Metadata.InboundUser),
		Host: detailText(conn.Metadata.Host), SniffHost: detailText(conn.Metadata.SniffHost),
		DestinationIP: trafficIP(conn.Metadata.DestinationIP), DestinationPort: detailPort(conn.Metadata.DestinationPort),
		Network: detailText(conn.Metadata.Network), InboundType: detailText(conn.Metadata.Type), InboundName: detailText(conn.Metadata.InboundName),
		Rule: detailText(conn.Rule), RulePayload: detailText(conn.RulePayload), TrafficBytes: latest,
	}
	if len(conn.Chains) > 0 {
		// Mihomo 从节点向外记录策略组，展示时按策略组到节点排列。
		detail.Node = detailText(conn.Chains[0])
		for index := min(len(conn.Chains), 24) - 1; index >= 0; index-- {
			if name := detailText(conn.Chains[index]); name != "" {
				detail.Route = append(detail.Route, name)
			}
		}
	}
	observation.detail = detail
	if delta == (model.TrafficBytes{}) {
		return
	}
	if observation.minutes == nil {
		observation.minutes = make(map[int64]model.TrafficBytes)
	}
	minute := at.Truncate(time.Minute).Unix()
	usage := observation.minutes[minute]
	usage.Upload += delta.Upload
	usage.Download += delta.Download
	observation.minutes[minute] = usage
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

func (c *trafficCollector) openDetails() (*trafficDetailStore, error) {
	dir := filepath.Dir(c.path)
	store, err := openTrafficDetailDB(c.store, c.policy.maxBytes)
	if err != nil {
		return nil, err
	}
	_, imported, err := c.store.get(stateTrafficImport)
	if err != nil {
		return nil, err
	}
	if !imported {
		if err := store.migrateJournal(filepath.Join(dir, "traffic-connections.jsonl")); err != nil {
			return nil, err
		}
		if err := c.store.put(stateTrafficImport, []byte("1")); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (d *trafficDetailStore) migrateJournal(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 128*1024)
	scanner.Split(completeTrafficLine)
	for scanner.Scan() {
		var detail model.TrafficConnectionDetail
		if err := json.Unmarshal(scanner.Bytes(), &detail); err != nil || detail.ID == "" || detail.StartedAt.IsZero() || detail.LastSeenAt.IsZero() {
			return errors.New("旧连接明细包含无效记录，原文件已保留")
		}
		d.pending[detail.ID] = &trafficObservation{detail: detail}
		if len(d.pending) >= trafficDetailBatch {
			if err := d.persist(); err != nil {
				return fmt.Errorf("迁移旧连接明细：%w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := d.persist(); err != nil {
		return err
	}
	return nil
}

func completeTrafficLine(data []byte, atEOF bool) (int, []byte, error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index], nil
	}
	// 旧日志突然断电时可能留下未写完的一行。
	if atEOF {
		return len(data), nil, nil
	}
	return 0, nil, nil
}

func (c *trafficCollector) saveDetails(at time.Time) error {
	if c.details == nil {
		return nil
	}
	first := c.policy.currentMonth(at.In(c.location)).AddDate(0, 1-c.policy.retentionMonths, 0)
	err := c.details.flush(c.policy.start.at(first), c.policy.maxBytes)
	c.details.storageError = err != nil
	return err
}
