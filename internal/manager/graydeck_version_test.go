package manager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type graydeckTestTransport func(*http.Request) (*http.Response, error)

func (f graydeckTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGraydeckChecksOfficialReleaseBeforeCacheAndKeepsKnownNewer(t *testing.T) {
	config := filepath.Join(t.TempDir(), "graydeck.yaml")
	if err := os.WriteFile(config, []byte("update:\n  prefer-proxy: true\n  proxy-url: https://mirror.example/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{cfg: Config{AppConfigPath: config}}
	service.status.GraydeckVersion = "1.1.0"
	officialAvailable := true
	var requests []string
	service.httpClient = &http.Client{Transport: graydeckTestTransport(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.Host)
		if req.Header.Get("Cache-Control") != "no-cache" {
			t.Fatal("版本 API 未请求刷新缓存")
		}
		tag := "v1.1.0"
		if req.URL.Host == "api.github.com" {
			if req.URL.Path != "/repos/ydfk/gray-deck/releases/latest" {
				t.Fatalf("请求了错误的发布仓库：%s", req.URL.Path)
			}
			if !officialAvailable {
				return nil, errors.New("GitHub unavailable")
			}
			tag = "v1.2.0"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"` + tag + `"}`))}, nil
	})}
	if err := service.refreshGraydeckMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != "api.github.com" || !service.status.GraydeckUpdateAvailable || service.status.GraydeckLatestVersion != "v1.2.0" {
		t.Fatalf("面板没有采用 GitHub 的更新版本：%+v，%+v", requests, service.status)
	}
	officialAvailable = false
	if err := service.refreshGraydeckMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.status.GraydeckLatestVersion != "v1.2.0" || service.status.GraydeckVersionVerified || service.status.GraydeckIsLatest {
		t.Fatalf("代理缓存覆盖了更高版本：%+v", service.status)
	}
	service.status.GraydeckLatestVersion = ""
	if err := service.refreshGraydeckMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.status.GraydeckIsLatest || service.status.GraydeckUpdateAvailable {
		t.Fatalf("镜像只返回已安装版本时不能断言最新：%+v", service.status)
	}
}
