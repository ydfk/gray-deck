package manager

import (
	"fmt"
	"log"
	"net/url"
	"regexp"
	"time"

	"mihomo-manager/internal/model"
)

const (
	maxLogEntries = 400
	maxLogRunes   = 1024
)

var logURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

func (s *Service) appendLogf(format string, args ...any) {
	s.appendLog(fmt.Sprintf(format, args...))
}

func (s *Service) appendLogLocked(message string) {
	// 订阅地址的路径和参数可能包含密钥，日志只记录来源主机。
	message = logURL.ReplaceAllStringFunc(message, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return "[URL]"
		}
		return parsed.Scheme + "://" + parsed.Host
	})
	runes := []rune(message)
	if len(runes) > maxLogRunes {
		message = string(runes[:maxLogRunes])
	}
	entry := model.LogEntry{At: formatLocalTime(localNow()), Message: message}
	s.logs = keepRecentLogs(append(s.logs, entry), maxLogEntries)
	if s.store != nil {
		s.pendingLogs = keepRecentLogs(append(s.pendingLogs, entry), maxLogEntries)
	}
}

func keepRecentLogs(items []model.LogEntry, limit int) []model.LogEntry {
	if len(items) > limit {
		return append([]model.LogEntry(nil), items[len(items)-limit:]...)
	}
	return items
}

func (s *Service) loadLogs() error {
	rows, err := s.store.db.Query(`SELECT at,message FROM (
		SELECT id,at,message FROM logs ORDER BY id DESC LIMIT ?
	) ORDER BY id`, maxLogEntries)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entry model.LogEntry
		if err := rows.Scan(&entry.At, &entry.Message); err != nil {
			return err
		}
		s.logs = append(s.logs, entry)
	}
	return rows.Err()
}

func (s *Service) logPersistenceLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.syncStopCh:
			return
		case <-ticker.C:
			if err := s.flushLogs(); err != nil {
				log.Printf("保存日志失败：%v", err)
			}
		}
	}
}

func (s *Service) flushLogs() error {
	if s.store == nil {
		return nil
	}
	s.logFlushMu.Lock()
	defer s.logFlushMu.Unlock()
	s.mu.Lock()
	items := s.pendingLogs
	s.pendingLogs = nil
	s.mu.Unlock()
	if len(items) == 0 {
		return nil
	}
	limit := maxLogEntries
	if s.traffic != nil {
		limit = s.traffic.policy.logLimit()
	}
	items = keepRecentLogs(items, limit)
	err := s.store.write(func() error {
		tx, err := s.store.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec("DELETE FROM logs WHERE id NOT IN (SELECT id FROM logs ORDER BY id DESC LIMIT ?)", limit-len(items)); err != nil {
			return err
		}
		for _, entry := range items {
			if _, err := tx.Exec("INSERT INTO logs(at,message) VALUES(?,?)", entry.At, entry.Message); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		s.mu.Lock()
		s.pendingLogs = keepRecentLogs(append(items, s.pendingLogs...), maxLogEntries)
		s.mu.Unlock()
	}
	return err
}

func (s *Service) trimLogs(limit int) error {
	if _, err := s.store.db.Exec("DELETE FROM logs WHERE id NOT IN (SELECT id FROM logs ORDER BY id DESC LIMIT ?)", limit); err != nil {
		return err
	}
	s.logs = keepRecentLogs(s.logs, limit)
	return nil
}
