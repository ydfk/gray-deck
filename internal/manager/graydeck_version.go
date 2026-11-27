package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"mihomo-manager/internal/buildinfo"
	"mihomo-manager/internal/model"
)

func (s *Service) fetchLatestGraydeckRelease(ctx context.Context) (releaseInfo, bool, error) {
	rawURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", buildinfo.RepositoryOwner, buildinfo.RepositoryName)
	candidates := s.preferredUpdateURLs(rawURL, true)
	// 镜像可能返回缓存的旧版本，先以官方接口为准。
	slices.Reverse(candidates)
	var lastErr error
	for _, candidate := range candidates {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		release, err := s.fetchGraydeckReleaseAt(attempt, candidate)
		cancel()
		if err == nil {
			return release, candidate == rawURL, nil
		}
		lastErr = err
	}
	return releaseInfo{}, false, lastErr
}

func (s *Service) fetchGraydeckReleaseAt(ctx context.Context, url string) (releaseInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	req.Header.Set("User-Agent", "Graydeck")
	req.Header.Set("Cache-Control", "no-cache")
	res, err := s.httpClient.Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return releaseInfo{}, fmt.Errorf("获取 Graydeck 发布信息失败：%s", res.Status)
	}
	var release releaseInfo
	if err := json.NewDecoder(res.Body).Decode(&release); err != nil {
		return releaseInfo{}, err
	}
	if _, prerelease, valid := parseGraydeckVersion(release.TagName); !valid || prerelease {
		return releaseInfo{}, errors.New("Graydeck 发布版本号无效")
	}
	return release, nil
}

func (s *Service) refreshGraydeckMetadata(ctx context.Context) error {
	s.graydeckVersionMu.Lock()
	defer s.graydeckVersionMu.Unlock()
	release, verified, err := s.fetchLatestGraydeckRelease(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && (verified || compareGraydeckVersions(release.TagName, s.status.GraydeckLatestVersion) > 0) {
		s.cacheLatestVersionLocked(stateLatestVersion, s.status.GraydeckLatestVersion, release.TagName)
		s.status.GraydeckLatestVersion = release.TagName
	}
	s.status.GraydeckVersionVerified = verified
	s.syncInstallStateLocked()
	return err
}

func (s *Service) CheckGraydeckVersion(ctx context.Context) model.SystemStatus {
	if err := s.refreshGraydeckMetadata(ctx); err != nil {
		s.appendLogf("获取 Graydeck 版本失败：%v", err)
	}
	return s.Status()
}

func (s *Service) graydeckVersionLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.syncStopCh:
			return
		case <-ticker.C:
			if err := s.refreshGraydeckMetadata(context.Background()); err != nil {
				s.appendLogf("获取 Graydeck 版本失败：%v", err)
			}
		}
	}
}

func parseGraydeckVersion(raw string) ([3]uint64, bool, bool) {
	var version [3]uint64
	base, _, prerelease := strings.Cut(normalizeVersion(raw), "-")
	parts := strings.Split(base, ".")
	if len(parts) != len(version) {
		return version, false, false
	}
	for index, part := range parts {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version, false, false
		}
		version[index] = value
	}
	return version, prerelease, true
}

func compareGraydeckVersions(a, b string) int {
	left, leftPre, leftValid := parseGraydeckVersion(a)
	right, rightPre, rightValid := parseGraydeckVersion(b)
	switch {
	case !leftValid && !rightValid:
		return 0
	case !leftValid:
		return -1
	case !rightValid:
		return 1
	}
	if value := slices.Compare(left[:], right[:]); value != 0 || leftPre == rightPre {
		return value
	}
	if leftPre {
		return -1
	}
	return 1
}
