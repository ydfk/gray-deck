package manager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"mihomo-manager/internal/model"
)

const trafficDetailPageSize = 50

var ErrTrafficDetailQuery = errors.New("invalid traffic details query")

type TrafficConnectionQuery struct {
	Period  string
	ByUser  bool
	Kind    string
	Subject string
	Search  string
	Cursor  string
}

type trafficDetailCursor struct {
	at time.Time
	id string
}

func parseTrafficDetailCursor(raw string) (trafficDetailCursor, error) {
	if raw == "" {
		return trafficDetailCursor{}, nil
	}
	if len(raw) > 256 {
		return trafficDetailCursor{}, ErrTrafficDetailQuery
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return trafficDetailCursor{}, ErrTrafficDetailQuery
	}
	at, id, ok := strings.Cut(string(decoded), "\n")
	when, err := time.Parse(time.RFC3339Nano, at)
	if !ok || err != nil || id == "" || len(id) > 128 {
		return trafficDetailCursor{}, ErrTrafficDetailQuery
	}
	return trafficDetailCursor{when, id}, nil
}

func (s *Service) TrafficConnections(query TrafficConnectionQuery) (model.TrafficConnectionReport, error) {
	if s.traffic == nil {
		return model.TrafficConnectionReport{}, ErrTrafficUnavailable
	}
	return s.traffic.connectionReport(time.Now(), query)
}

func (c *trafficCollector) connectionReport(now time.Time, query TrafficConnectionQuery) (model.TrafficConnectionReport, error) {
	cursor, err := parseTrafficDetailCursor(query.Cursor)
	if err != nil {
		return model.TrafficConnectionReport{}, err
	}

	filters, args, err := connectionFilters(query)
	if err != nil {
		return model.TrafficConnectionReport{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	report := model.TrafficConnectionReport{
		Disabled: c.policy.detailsDisabled, MaxSizeMiB: c.policy.maxBytes / trafficMiB,
		Items: make([]model.TrafficConnectionDetail, 0),
	}
	if report.Disabled {
		return report, nil
	}
	if c.details == nil {
		return model.TrafficConnectionReport{}, ErrTrafficUnavailable
	}
	report.StorageError = c.details.storageError
	if query.Period != "all" {
		window, found := c.connectionWindow(now, query.Period)
		if !found {
			return model.TrafficConnectionReport{}, ErrTrafficPeriod
		}
		filters = append(filters, "c.started>=?", "c.started<?")
		args = append(args, window.start.UnixNano(), window.end.UnixNano())
	}
	if !cursor.at.IsZero() {
		filters = append(filters, "(c.started,c.id)<(?,?)")
		args = append(args, cursor.at.UnixNano(), cursor.id)
	}
	report.Items, report.NextCursor, err = c.findConnections(filters, args)
	return report, err
}

func (c *trafficCollector) connectionWindow(now time.Time, period string) (trafficWindow, bool) {
	windows := c.trafficWindows(c.policy.currentMonth(now.In(c.location)))
	for _, window := range windows {
		if period == "" || window.start.Format(time.DateOnly) == period {
			return window, true
		}
	}
	return trafficWindow{}, false
}

func connectionFilters(query TrafficConnectionQuery) ([]string, []any, error) {
	if len(query.Subject) > 512 || len(query.Search) > 512 {
		return nil, nil, ErrTrafficDetailQuery
	}
	var filters []string
	var args []any
	switch query.Kind {
	case "", "unknown":
		if query.Subject != "" {
			return nil, nil, ErrTrafficDetailQuery
		}
		if query.Kind == "unknown" {
			filters = append(filters, "c.source_ip=''")
		}
	case "ip", "user":
		if query.Subject == "" || (query.Kind == "user" && !query.ByUser) {
			return nil, nil, ErrTrafficDetailQuery
		}
		column := "source_ip"
		if query.Kind == "user" {
			column = "inbound_user"
		}
		filters = append(filters, "c."+column+"=?")
		args = append(args, query.Subject)
	default:
		return nil, nil, ErrTrafficDetailQuery
	}
	if query.ByUser && (query.Kind == "ip" || query.Kind == "unknown") {
		filters = append(filters, "c.inbound_user=''")
	}
	if term := strings.ToLower(strings.TrimSpace(query.Search)); term != "" {
		filters = append(filters, "instr(c.search_text,?)>0")
		args = append(args, term)
	}
	return filters, args, nil
}

func (c *trafficCollector) findConnections(filters []string, args []any) ([]model.TrafficConnectionDetail, string, error) {
	sql := "SELECT c.started,c.detail FROM connections c"
	if len(filters) != 0 {
		sql += " WHERE " + strings.Join(filters, " AND ")
	}
	rows, err := c.details.db.Query(sql+" ORDER BY c.started DESC,c.id DESC LIMIT ?", append(args, trafficDetailPageSize+1)...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	matches := make([]model.TrafficConnectionDetail, 0, trafficDetailPageSize+1)
	for rows.Next() {
		var started int64
		var payload []byte
		if err := rows.Scan(&started, &payload); err != nil {
			return nil, "", err
		}
		var item model.TrafficConnectionDetail
		if err := json.Unmarshal(payload, &item); err != nil {
			return nil, "", err
		}
		item.StartedAt = time.Unix(0, started).In(c.location)
		item.LastSeenAt = item.LastSeenAt.In(c.location)
		matches = append(matches, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(matches) > trafficDetailPageSize {
		matches = matches[:trafficDetailPageSize]
		last := matches[len(matches)-1]
		next = base64.RawURLEncoding.EncodeToString([]byte(last.StartedAt.UTC().Format(time.RFC3339Nano) + "\n" + last.ID))
	}
	return matches, next, nil
}
