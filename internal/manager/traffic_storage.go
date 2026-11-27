package manager

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

func (s *Service) saveTraffic() {
	if s.traffic == nil {
		return
	}
	if err := s.traffic.save(time.Now()); err != nil {
		s.appendLogf("保存流量历史失败：%v", err)
	}
}

func (c *trafficCollector) save(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 即使没有新流量，也按保留期限清理历史。
	c.pruneExpired(at.In(c.location))
	if !c.dirty {
		return c.saveDetails(at)
	}
	snapshot, payload, err := c.compactSnapshot(at, c.policy.summaryBudget())
	if err == nil {
		err = c.store.put(stateTraffic, payload)
	}
	c.storageError = err != nil
	if err == nil {
		c.state.Days = snapshot.Days
		c.state.PrunedThrough = snapshot.PrunedThrough
		c.state.SampledAt = snapshot.SampledAt
		c.dirty = false
		return c.saveDetails(at)
	}
	return err
}

func (c *trafficCollector) pruneExpired(now time.Time) {
	firstMonth := c.policy.currentMonth(now).AddDate(0, 1-c.policy.retentionMonths, 0)
	cutoff := c.policy.start.at(firstMonth).Format(trafficKeyLayout)
	for key := range c.state.Days {
		if key < cutoff {
			delete(c.state.Days, key)
			c.state.PrunedThrough = max(c.state.PrunedThrough, key)
			c.dirty = true
		}
	}
}

func (c *trafficCollector) compactSnapshot(at time.Time, budget int) (trafficState, []byte, error) {
	snapshot := c.state
	if !c.lastSampleAt.IsZero() {
		checkpoint := c.lastSampleAt.UTC()
		snapshot.SampledAt = &checkpoint
	}
	baselines, err := json.Marshal(snapshot.Active)
	if err != nil {
		return snapshot, nil, err
	}
	if len(baselines) > budget/4 {
		// 高并发时限制磁盘上的连接基线，重启后从采样时间重新建立缺失的基线。
		snapshot.Active = nil
		if snapshot.SampledAt == nil {
			checkpoint := at.UTC()
			snapshot.SampledAt = &checkpoint
		}
	}
	payload, err := json.Marshal(snapshot)
	if err != nil || len(payload) <= budget {
		return snapshot, payload, err
	}

	keys := make([]string, 0, len(snapshot.Days))
	snapshot.Days = make(map[string]trafficDay, len(c.state.Days))
	for key, bucket := range c.state.Days {
		keys = append(keys, key)
		snapshot.Days[key] = bucket
	}
	sort.Strings(keys)
	// 为新的淘汰边界预留空间，避免每删一段就重编码整个文件。
	remaining := len(payload) + 1
	if snapshot.PrunedThrough == "" {
		remaining += len(`,"prunedThrough":"2006-01-02T15:04"`)
	}
	for _, key := range keys {
		bucket, err := json.Marshal(snapshot.Days[key])
		if err != nil {
			return snapshot, nil, err
		}
		delete(snapshot.Days, key)
		snapshot.PrunedThrough = max(snapshot.PrunedThrough, key)
		remaining -= len(bucket) + len(key) + 4
		if remaining <= budget {
			break
		}
	}
	payload, err = json.Marshal(snapshot)
	if err == nil && len(payload) > budget {
		err = errors.New("流量历史元数据超过文件大小上限")
	}
	return snapshot, payload, err
}
