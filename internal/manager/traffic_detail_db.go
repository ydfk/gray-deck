package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	trafficDetailBatch = 128
	trafficDBReserve   = 256 * 1024
)

type trafficDetailStore struct {
	db           *sql.DB
	pending      map[string]*trafficObservation
	pageSize     int
	budget       int
	prunedBefore time.Time
	storageError bool
}

const trafficDetailSchema = `
CREATE TABLE IF NOT EXISTS connections (
	id TEXT PRIMARY KEY,
	started INTEGER NOT NULL,
	last_seen INTEGER NOT NULL,
	source_ip TEXT NOT NULL,
	inbound_user TEXT NOT NULL,
	target TEXT NOT NULL,
	protocol TEXT NOT NULL,
	search_text TEXT NOT NULL,
	detail TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS connections_started ON connections(started DESC,id DESC);
CREATE INDEX IF NOT EXISTS connections_last_seen ON connections(last_seen,id);
CREATE INDEX IF NOT EXISTS connections_source ON connections(source_ip);
CREATE INDEX IF NOT EXISTS connections_user ON connections(inbound_user);
CREATE TABLE IF NOT EXISTS activity (
	id TEXT NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
	minute INTEGER NOT NULL,
	upload INTEGER NOT NULL,
	download INTEGER NOT NULL,
	PRIMARY KEY(id,minute)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS activity_minute ON activity(minute,id);
`

func openTrafficDetailDB(store *stateStore, budget int) (*trafficDetailStore, error) {
	if budget < trafficDBReserve {
		return nil, errors.New("流量历史剩余空间不足以打开连接数据库")
	}
	details := &trafficDetailStore{db: store.db, pending: make(map[string]*trafficObservation)}
	if err := details.init(budget); err != nil {
		return nil, err
	}
	return details, nil
}

func (d *trafficDetailStore) init(budget int) error {
	if err := d.db.QueryRow("PRAGMA page_size").Scan(&d.pageSize); err != nil {
		return err
	}
	return d.setBudget(budget)
}

func (d *trafficDetailStore) setBudget(bytes int) error {
	if bytes == d.budget {
		return nil
	}
	if bytes < trafficDBReserve {
		return errors.New("流量历史超出共享空间")
	}
	maxPages := bytes / d.pageSize
	for {
		var pages int
		if err := d.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
			return err
		}
		if pages <= maxPages {
			break
		}
		removed, err := d.evictOldest()
		if err != nil {
			return err
		}
		if !removed {
			return errors.New("无法将连接数据库缩小到共享上限")
		}
	}
	if _, err := d.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
		return err
	}
	d.budget = bytes
	return nil
}

func (d *trafficDetailStore) flush(first time.Time, budget int) error {
	if !first.Equal(d.prunedBefore) {
		if _, err := d.db.Exec("DELETE FROM activity WHERE minute < ?", first.Unix()); err != nil {
			return err
		}
		if _, err := d.db.Exec("DELETE FROM connections WHERE last_seen < ?", first.UnixNano()); err != nil {
			return err
		}
		d.prunedBefore = first
	}
	if err := d.setBudget(budget); err != nil {
		return err
	}
	return d.persist()
}

func (d *trafficDetailStore) evictOldest() (bool, error) {
	return evictOldestTraffic(d.db)
}

func evictOldestTraffic(db *sql.DB) (bool, error) {
	result, err := db.Exec(`
		DELETE FROM activity WHERE (minute,id) IN (SELECT minute,id FROM activity ORDER BY minute,id LIMIT 1024)`)
	if err != nil {
		return false, err
	}
	activity, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	result, err = db.Exec(`
		DELETE FROM connections WHERE id IN (
			SELECT c.id FROM connections c WHERE NOT EXISTS(SELECT 1 FROM activity a WHERE a.id=c.id)
			ORDER BY c.last_seen,c.id LIMIT 128
		)`)
	if err != nil {
		return false, err
	}
	connections, err := result.RowsAffected()
	return activity > 0 || connections > 0, err
}

func (d *trafficDetailStore) persist() error {
	observations := make([]*trafficObservation, 0, len(d.pending))
	for _, observation := range d.pending {
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(i, j int) bool {
		a, b := observations[i].detail, observations[j].detail
		if a.LastSeenAt.Equal(b.LastSeenAt) {
			return a.ID < b.ID
		}
		return a.LastSeenAt.Before(b.LastSeenAt)
	})
	batchSize := trafficDetailBatch
	for len(observations) > 0 {
		count := min(len(observations), batchSize)
		batch := observations[:count]
		if err := d.writeBatch(batch); err != nil {
			if !sqliteFull(err) {
				return err
			}
			// SQLITE_FULL 会回滚本批。释放最旧空间后再写，避免重复累计。
			removed, evictErr := d.evictOldest()
			if evictErr == nil && !removed && count > 1 {
				batchSize = count / 2
				continue
			}
			if evictErr != nil || !removed {
				return errors.Join(err, evictErr)
			}
			continue
		}
		for _, observation := range batch {
			delete(d.pending, observation.detail.ID)
		}
		observations = observations[count:]
	}
	return nil
}

func (d *trafficDetailStore) writeBatch(batch []*trafficObservation) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range batch {
		item := record.detail
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		target := item.Host
		if target == "" {
			target = item.SniffHost
		}
		if target == "" {
			target = item.DestinationIP
		}
		search := strings.ToLower(strings.Join([]string{
			item.Host, item.SniffHost, item.DestinationIP, item.DestinationPort, item.SourceIP, item.InboundUser,
			item.Rule, item.RulePayload, item.Network, item.InboundType, item.InboundName, strings.Join(item.Route, "\n"),
		}, "\n"))
		_, err = tx.Exec(`
			INSERT INTO connections(id,started,last_seen,source_ip,inbound_user,target,protocol,search_text,detail)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET last_seen=excluded.last_seen,source_ip=excluded.source_ip,inbound_user=excluded.inbound_user,
				target=excluded.target,protocol=excluded.protocol,search_text=excluded.search_text,detail=excluded.detail
			WHERE excluded.last_seen>=connections.last_seen`,
			item.ID, item.StartedAt.UnixNano(), item.LastSeenAt.UnixNano(), item.SourceIP, item.InboundUser,
			strings.ToLower(strings.TrimSuffix(target, ".")), item.Network, search, string(payload))
		if err != nil {
			return err
		}
		for minute, usage := range record.minutes {
			_, err := tx.Exec(`
				INSERT INTO activity(id,minute,upload,download) VALUES(?,?,?,?)
				ON CONFLICT(id,minute) DO UPDATE SET upload=upload+excluded.upload,download=download+excluded.download`,
				item.ID, minute, usage.Upload, usage.Download)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
