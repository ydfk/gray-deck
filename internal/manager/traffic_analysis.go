package manager

import (
	"encoding/json"
	"strings"
	"time"

	"mihomo-manager/internal/model"
)

type TrafficAnalysisQuery struct {
	TrafficConnectionQuery
	Day      string
	Hour     string
	Protocol string
	Target   string
}

func (s *Service) TrafficAnalysis(query TrafficAnalysisQuery) (model.TrafficAnalysisReport, error) {
	if s.traffic == nil {
		return model.TrafficAnalysisReport{}, ErrTrafficUnavailable
	}
	return s.traffic.analysis(time.Now(), query)
}

func (c *trafficCollector) analysis(now time.Time, query TrafficAnalysisQuery) (model.TrafficAnalysisReport, error) {
	filters, args, err := connectionFilters(query.TrafficConnectionQuery)
	if err != nil {
		return model.TrafficAnalysisReport{}, err
	}
	if len(query.Protocol) > 128 || len(query.Target) > 512 {
		return model.TrafficAnalysisReport{}, ErrTrafficDetailQuery
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.details == nil {
		return model.TrafficAnalysisReport{}, ErrTrafficUnavailable
	}
	window, found := c.connectionWindow(now, query.Period)
	if !found {
		return model.TrafficAnalysisReport{}, ErrTrafficPeriod
	}
	window, err = c.analysisWindow(window, query.Day, query.Hour)
	if err != nil {
		return model.TrafficAnalysisReport{}, err
	}
	if query.Protocol != "" {
		filters, args = append(filters, "c.protocol=?"), append(args, query.Protocol)
	}
	if query.Target != "" {
		filters, args = append(filters, "c.target=?"), append(args, query.Target)
	}
	filters = append(filters, "a.minute>=?", "a.minute<?")
	args = append(args, window.start.Unix(), window.end.Unix())
	scope := " FROM activity a JOIN connections c ON c.id=a.id WHERE " + strings.Join(filters, " AND ")
	var report model.TrafficAnalysisReport
	report.Timeline, report.Totals, err = c.analysisTimeline(scope, args, query)
	if err != nil {
		return report, err
	}
	report.Protocols, err = c.analysisRanking("c.protocol", scope, args)
	if err != nil {
		return report, err
	}
	report.Targets, err = c.analysisRanking("c.target", scope, args)
	if err != nil {
		return report, err
	}
	report.Accesses, err = c.analysisAccesses(scope, args)
	return report, err
}

func (c *trafficCollector) analysisWindow(window trafficWindow, day, hour string) (trafficWindow, error) {
	if day == "" {
		if hour != "" {
			return window, ErrTrafficDetailQuery
		}
		return window, nil
	}
	at, err := time.ParseInLocation(time.DateOnly, day, c.location)
	if err != nil {
		return window, ErrTrafficDetailQuery
	}
	start, end := at, at.AddDate(0, 0, 1)
	if hour != "" {
		at, err = time.Parse(time.RFC3339, hour)
		local := at.In(c.location)
		if err != nil || local.Format(time.DateOnly) != day || local.Minute() != 0 || local.Second() != 0 || local.Nanosecond() != 0 {
			return window, ErrTrafficDetailQuery
		}
		start, end = at, at.Add(time.Hour)
	}
	if start.After(window.start) {
		window.start = start
	}
	if end.Before(window.end) {
		window.end = end
	}
	return window, nil
}

func (c *trafficCollector) analysisTimeline(scope string, args []any, query TrafficAnalysisQuery) ([]model.TrafficAnalysisRow, model.TrafficBytes, error) {
	rows, err := c.details.db.Query("SELECT a.minute,sum(a.upload),sum(a.download)"+scope+" GROUP BY a.minute ORDER BY a.minute", args...)
	if err != nil {
		return nil, model.TrafficBytes{}, err
	}
	defer rows.Close()
	items := make([]model.TrafficAnalysisRow, 0)
	var totals model.TrafficBytes
	for rows.Next() {
		var minute int64
		var size model.TrafficBytes
		if err := rows.Scan(&minute, &size.Upload, &size.Download); err != nil {
			return nil, totals, err
		}
		at := time.Unix(minute, 0).In(c.location)
		key, label := at.Format(time.DateOnly), at.Format(time.DateOnly)
		if query.Hour != "" {
			key, label = at.Format(time.RFC3339), at.Format("15:04")
		} else if query.Day != "" {
			hour := at.Add(-time.Duration(at.Minute()) * time.Minute)
			key, label = hour.Format(time.RFC3339), hour.Format("15:04 -07:00")
		}
		if len(items) == 0 || items[len(items)-1].Key != key {
			items = append(items, model.TrafficAnalysisRow{Key: key, Label: label})
		}
		items[len(items)-1].Upload += size.Upload
		items[len(items)-1].Download += size.Download
		totals.Upload += size.Upload
		totals.Download += size.Download
	}
	return items, totals, rows.Err()
}

func (c *trafficCollector) analysisRanking(column, scope string, args []any) ([]model.TrafficAnalysisRow, error) {
	rows, err := c.details.db.Query("SELECT "+column+",sum(a.upload),sum(a.download)"+scope+
		" GROUP BY "+column+" ORDER BY sum(a.upload)+sum(a.download) DESC,"+column+" LIMIT 12", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.TrafficAnalysisRow, 0)
	for rows.Next() {
		var item model.TrafficAnalysisRow
		if err := rows.Scan(&item.Key, &item.Upload, &item.Download); err != nil {
			return nil, err
		}
		item.Label = item.Key
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *trafficCollector) analysisAccesses(scope string, args []any) ([]model.TrafficAccessRow, error) {
	rows, err := c.details.db.Query(`
		SELECT c.detail,min(a.minute),max(a.minute),sum(a.upload),sum(a.download)`+scope+`
		GROUP BY c.id ORDER BY sum(a.upload)+sum(a.download) DESC,c.id LIMIT 20`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.TrafficAccessRow, 0)
	for rows.Next() {
		var item model.TrafficAccessRow
		var payload []byte
		var first, last int64
		if err := rows.Scan(&payload, &first, &last, &item.Upload, &item.Download); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Connection); err != nil {
			return nil, err
		}
		item.First = time.Unix(first, 0).In(c.location)
		item.Last = time.Unix(last, 0).In(c.location)
		items = append(items, item)
	}
	return items, rows.Err()
}
