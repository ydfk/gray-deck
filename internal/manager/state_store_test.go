package manager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStateStore(t *testing.T, dir string) *stateStore {
	t.Helper()
	store, err := openStateStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Close() })
	return store
}

func openTrafficCollector(t *testing.T, path string, location *time.Location, policy trafficPolicy) (*trafficCollector, error) {
	t.Helper()
	return loadTrafficCollector(testStateStore(t, filepath.Dir(path)), path, location, policy)
}

func TestBackendDatabaseMigratesSubscriptionAndVersionWithoutOverwritingYAML(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir}
	source := filepath.Join(dir, "subscriptions.json")
	legacy := `[{"id":"config-old","name":"旧订阅","url":"https://example.test/sub","enabled":true,"syncInterval":"30m"}]`
	if err := os.WriteFile(source, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{cfg: cfg, store: testStateStore(t, dir)}
	if err := service.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	preview := service.subscriptionPreviewPath("config-old")
	if err := os.WriteFile(preview, []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service.coreDir(), "version.txt"), []byte("v9.4.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.loadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	if got := service.cachedArtifactVersion(stateCoreVersion, filepath.Join(service.coreDir(), "version.txt")); got != "v9.4.1" {
		t.Fatalf("版本迁移错误：%s", got)
	}
	if _, err := service.UpdateSubscription(context.Background(), "config-old", "新名称", "https://example.test/sub", "2h"); err != nil {
		t.Fatal(err)
	}
	reopened := &Service{cfg: cfg, store: testStateStore(t, dir)}
	if err := reopened.loadSubscriptions(); err != nil {
		t.Fatal(err)
	}
	items := reopened.Subscriptions()
	if len(items) != 1 || items[0].Name != "新名称" || items[0].SyncInterval != "2h" || !items[0].Enabled || !items[0].AutoSync {
		t.Fatalf("没有从数据库恢复更新后的订阅：%+v", items)
	}
	if version := reopened.cachedArtifactVersion(stateCoreVersion, ""); version != "v9.4.1" {
		t.Fatalf("数据库版本缓存丢失：%s", version)
	}
	oldContents, err := os.ReadFile(source)
	if err != nil || string(oldContents) != legacy {
		t.Fatalf("迁移覆盖了旧订阅备份：%v", err)
	}
	yaml, err := os.ReadFile(preview)
	if err != nil || string(yaml) != "proxies: []\n" {
		t.Fatalf("订阅 YAML 被改变：%v", err)
	}
}

func TestBackendDatabaseRestoresBoundedLogsAndHashedSessions(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir, AppConfigPath: filepath.Join(dir, "graydeck.yaml")}
	config := "auth:\n  enabled: true\n  username: admin\n  password: test-value\n"
	if err := os.WriteFile(cfg.AppConfigPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Service{cfg: cfg, store: testStateStore(t, dir)}
	for i := 0; i < maxLogEntries+5; i++ {
		s.appendLog("购买的订阅 https://example.test/token-in-path?credential=private")
	}
	if err := s.flushLogs(); err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateSession("admin", "test-value")
	if err != nil {
		t.Fatal(err)
	}
	reopened := &Service{cfg: cfg, store: testStateStore(t, dir)}
	if err := reopened.loadLogs(); err != nil {
		t.Fatal(err)
	}
	logs := reopened.Logs()
	if len(logs) != maxLogEntries || strings.Contains(logs[0].Message, "token-in-path") || strings.Contains(logs[0].Message, "private") {
		t.Fatalf("持久日志超限或暴露了订阅密钥：%d", len(logs))
	}
	if user, ok := reopened.SessionUser(token); !ok || user != "admin" {
		t.Fatal("登录会话未随数据库恢复")
	}
	wantHash := sha256.Sum256([]byte(token))
	var hash []byte
	if err := reopened.store.db.QueryRow("SELECT token_hash FROM sessions").Scan(&hash); err != nil || string(hash) != string(wantHash[:]) {
		t.Fatalf("会话没有以哈希形式保存：%v", err)
	}
	if err := os.WriteFile(cfg.AppConfigPath, []byte(strings.Replace(config, "test-value", "rotated", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.SessionUser(token); ok {
		t.Fatal("更改密码后旧会话仍可用")
	}
	newToken, err := reopened.CreateSession("admin", "rotated")
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteSession(newToken); err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.SessionUser(newToken); ok {
		t.Fatal("已注销会话仍可用")
	}
}

func TestUnifiedDatabaseCopiesLegacySQLiteAndKeepsItForRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, legacyTrafficDB)
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("PRAGMA auto_vacuum=FULL;" + trafficDetailSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO connections VALUES('old',100,100,'','','','','','{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO activity VALUES('old',100,20,30)`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	store := testStateStore(t, dir)
	var down uint64
	if err := store.db.QueryRow("SELECT download FROM activity WHERE id='old'").Scan(&down); err != nil || down != 30 {
		t.Fatalf("从旧 SQLite 迁移失败：%d，%v", down, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("旧 SQLite 备份不可用：%v", err)
	}
}
