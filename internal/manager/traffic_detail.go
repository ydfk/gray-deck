package manager

import (
	"encoding/base64"
	"errors"
	"sort"
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
	if len(query.Subject) > 512 || len(query.Search) > 512 {
		return model.TrafficConnectionReport{}, ErrTrafficDetailQuery
	}
	switch query.Kind {
	case "", "unknown":
		if query.Subject != "" {
			return model.TrafficConnectionReport{}, ErrTrafficDetailQuery
		}
	case "ip", "user":
		if query.Subject == "" || (query.Kind == "user" && !query.ByUser) {
			return model.TrafficConnectionReport{}, ErrTrafficDetailQuery
		}
	default:
		return model.TrafficConnectionReport{}, ErrTrafficDetailQuery
	}
	cursor, err := parseTrafficDetailCursor(query.Cursor)
	if err != nil {
		return model.TrafficConnectionReport{}, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	report := model.TrafficConnectionReport{
		RetentionDays: c.policy.detailDays, MaxSizeMiB: c.policy.detailMaxBytes / trafficMiB,
		MaxRecords: maxTrafficDetails, Items: make([]model.TrafficConnectionDetail, 0),
	}
	if c.policy.detailDays == 0 {
		return report, nil
	}
	if c.details == nil {
		return model.TrafficConnectionReport{}, ErrTrafficUnavailable
	}
	report.StorageError = c.details.storageError
	if query.Period == "all" {
		report.Items, report.NextCursor = c.findConnections(now, trafficWindow{}, query, cursor)
		return report, nil
	}
	windows := c.trafficWindows(c.policy.currentMonth(now.In(c.location)))
	for _, window := range windows {
		if query.Period == "" || window.start.Format(time.DateOnly) == query.Period {
			report.Items, report.NextCursor = c.findConnections(now, window, query, cursor)
			return report, nil
		}
	}
	return model.TrafficConnectionReport{}, ErrTrafficPeriod
}

func (c *trafficCollector) findConnections(now time.Time, window trafficWindow, query TrafficConnectionQuery, cursor trafficDetailCursor) ([]model.TrafficConnectionDetail, string) {
	matches := make([]model.TrafficConnectionDetail, 0)
	cutoff := c.details.cutoff(now)
	term := strings.ToLower(strings.TrimSpace(query.Search))
	subject := trafficSubject{kind: query.Kind, name: query.Subject}
	for _, item := range c.details.records {
		at := item.StartedAt
		if (!window.end.IsZero() && (at.Before(window.start) || !at.Before(window.end))) || item.LastSeenAt.Before(cutoff) {
			continue
		}
		if !cursor.at.IsZero() && (at.After(cursor.at) || (at.Equal(cursor.at) && item.ID >= cursor.id)) {
			continue
		}
		if subject.kind != "" && subjectForTraffic(item.SourceIP, item.InboundUser, query.ByUser) != subject {
			continue
		}
		if term != "" && !trafficDetailMatches(item, term) {
			continue
		}
		matches = append(matches, item)
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.StartedAt.Equal(b.StartedAt) {
			return a.ID > b.ID
		}
		return a.StartedAt.After(b.StartedAt)
	})

	next := ""
	if len(matches) > trafficDetailPageSize {
		matches = matches[:trafficDetailPageSize]
		last := matches[len(matches)-1]
		next = base64.RawURLEncoding.EncodeToString([]byte(last.StartedAt.UTC().Format(time.RFC3339Nano) + "\n" + last.ID))
	}
	for index := range matches {
		item := &matches[index]
		item.StartedAt = item.StartedAt.In(c.location)
		item.LastSeenAt = item.LastSeenAt.In(c.location)
	}
	return matches, next
}

func trafficDetailMatches(item model.TrafficConnectionDetail, term string) bool {
	for _, field := range []string{item.Host, item.SniffHost, item.DestinationIP, item.DestinationPort, item.SourceIP, item.InboundUser, item.Rule, item.RulePayload, item.Network, item.InboundType, item.InboundName} {
		if strings.Contains(strings.ToLower(field), term) {
			return true
		}
	}
	for _, name := range item.Route {
		if strings.Contains(strings.ToLower(name), term) {
			return true
		}
	}
	return false
}
