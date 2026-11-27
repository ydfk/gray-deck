package manager

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

const (
	stateDBName        = "graydeck.sqlite"
	legacyTrafficDB    = "traffic-connections.sqlite"
	stateSubscriptions = "subscriptions"
	stateTraffic       = "traffic-summary"
	stateTrafficImport = "traffic-jsonl-imported"
	stateCoreVersion   = "version-mihomo"
	stateCoreLatest    = "version-mihomo-latest"
	statePanelVersion  = "version-zashboard"
	statePanelLatest   = "version-zashboard-latest"
	stateLatestVersion = "version-graydeck-latest"
)

type stateStore struct{ db *sql.DB }

const stateSchema = `
CREATE TABLE IF NOT EXISTS app_state (
	key TEXT PRIMARY KEY,
	value BLOB NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS logs (
	id INTEGER PRIMARY KEY,
	at TEXT NOT NULL,
	message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash BLOB PRIMARY KEY,
	proof BLOB NOT NULL,
	username TEXT NOT NULL,
	expires INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires);
`

func openStateStore(dir string) (*stateStore, error) {
	path := filepath.Join(dir, stateDBName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		legacy := filepath.Join(dir, legacyTrafficDB)
		// 通过 SQLite 复制旧数据库的一致快照，包含崩溃前已提交的事务。
		if _, err := os.Stat(legacy); err == nil {
			if err := copyLegacyState(legacy, path); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=DELETE; PRAGMA auto_vacuum=FULL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=2000;"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(stateSchema + trafficDetailSchema); err != nil {
		db.Close()
		return nil, err
	}
	return &stateStore{db}, nil
}

func copyLegacyState(source, target string) error {
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return err
	}
	defer db.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".graydeck-migration-*.sqlite")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := db.Exec("VACUUM INTO ?", tmp.Name()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

func (s *stateStore) get(key string) ([]byte, bool, error) {
	var value []byte
	err := s.db.QueryRow("SELECT value FROM app_state WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return value, err == nil, err
}

func (s *stateStore) put(key string, value []byte) error {
	return s.write(func() error {
		_, err := s.db.Exec("INSERT INTO app_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
		return err
	})
}

// 订阅、会话和日志写入优先于可淘汰的历史明细。
func (s *stateStore) write(operation func() error) error {
	for {
		err := operation()
		if !sqliteFull(err) {
			return err
		}
		removed, trimErr := evictOldestTraffic(s.db)
		if trimErr != nil || !removed {
			return errors.Join(err, trimErr)
		}
	}
}

func sqliteFull(err error) bool {
	var databaseErr *sqlite.Error
	return errors.As(err, &databaseErr) && databaseErr.Code()&255 == 13
}
