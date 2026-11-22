package manager

import (
	"sort"
	"time"

	"mihomo-manager/internal/model"
)

func (s *Service) TrafficReport(period string, byUser bool) (model.TrafficReport, error) {
	if s.traffic == nil {
		return model.TrafficReport{}, ErrTrafficUnavailable
	}
	return s.traffic.report(time.Now(), period, byUser, s.trafficCoreRunning())
}

func (c *trafficCollector) report(at time.Time, selected string, byUser, coreRunning bool) (model.TrafficReport, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := at.In(c.location)
	current := c.policy.currentMonth(now)
	currentStart, currentEnd := c.policy.bounds(current)
	currentKey := currentStart.Format(time.DateOnly)
	if selected == "" {
		selected = currentKey
	}
	report := model.TrafficReport{
		CurrentPeriod: currentKey,
		CycleStart:    c.policy.start.String(),
		CycleEnd:      c.policy.end.String(),
		PeriodOpen:    now.Before(currentEnd),
		Retention:     c.policy.retentionMonths,
		MaxSizeMiB:    c.policy.maxBytes / trafficMiB,
		Timezone:      c.location.String(),
		Sampling:      coreRunning && !c.sampleFailed && now.Sub(c.lastSampleAt) < 5*trafficSampleInterval,
		StorageError:  c.storageError,
		Items:         make([]model.TrafficRow, 0),
	}
	if !c.lastSampleAt.IsZero() {
		report.LastSampleAt = c.lastSampleAt.In(c.location).Format(localTimeLayout)
	} else if c.state.SampledAt != nil {
		report.LastSampleAt = c.state.SampledAt.In(c.location).Format(localTimeLayout)
	}

	for index := 0; index < c.policy.retentionMonths; index++ {
		start, end := c.policy.bounds(current.AddDate(0, -index, 0))
		startKey, endKey := start.Format(trafficKeyLayout), end.Format(trafficKeyLayout)
		if index > 0 && (c.state.StartedAt.IsZero() || !end.After(c.state.StartedAt) || endKey <= c.state.PrunedThrough) {
			break
		}
		period := model.TrafficPeriod{
			ID: start.Format(time.DateOnly), Start: start.Format("2006-01-02 15:04"), End: end.Format("2006-01-02 15:04"),
			Partial: startKey <= c.state.PrunedThrough,
		}
		report.Periods = append(report.Periods, period)
		if period.ID == selected {
			report.Period = period
			report.Items, report.Totals = c.usage(startKey, endKey, byUser)
		}
	}
	if report.Period.Start == "" {
		return model.TrafficReport{}, ErrTrafficPeriod
	}
	return report, nil
}

type trafficSubject struct{ kind, name string }

func (c *trafficCollector) usage(start, end string, byUser bool) ([]model.TrafficRow, model.TrafficBytes) {
	grouped := make(map[trafficSubject]model.TrafficBytes)
	var totals model.TrafficBytes
	for key, bucket := range c.state.Days {
		if key < start || key >= end {
			continue
		}
		for ip, users := range bucket {
			for user, usage := range users {
				subject := trafficSubject{kind: "ip", name: ip}
				switch {
				case byUser && user != "":
					subject = trafficSubject{kind: "user", name: user}
				case ip == "":
					subject.kind = "unknown"
				}
				total := grouped[subject]
				total.Upload += usage.Upload
				total.Download += usage.Download
				grouped[subject] = total
				totals.Upload += usage.Upload
				totals.Download += usage.Download
			}
		}
	}
	items := make([]model.TrafficRow, 0, len(grouped))
	for subject, usage := range grouped {
		items = append(items, model.TrafficRow{Kind: subject.kind, Name: subject.name, TrafficBytes: usage})
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Upload+a.Download != b.Upload+b.Download {
			return a.Upload+a.Download > b.Upload+b.Download
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	return items, totals
}
