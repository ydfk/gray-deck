package manager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func (s *Service) cacheLatestVersionLocked(key, before, version string) {
	if s.store != nil && version != "" && version != before {
		if err := s.store.put(key, []byte(version)); err != nil {
			s.appendLogLocked(fmt.Sprintf("保存最新版本记录失败：%v", err))
		}
	}
}

var zashboardAssetVersionPattern = regexp.MustCompile("=L\\((?:\"|'|`)(v?\\d+\\.\\d+\\.\\d+(?:[-+][0-9A-Za-z.-]+)?)(?:\"|'|`)\\)")
var semanticVersionPattern = regexp.MustCompile(`v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?`)

func (s *Service) detectInstalledZashboardVersion() string {
	if !fileExists(filepath.Join(s.zashboardRoot(), "index.html")) {
		return ""
	}
	if version := detectZashboardVersionFromAssets(s.zashboardRoot()); version != "" {
		return version
	}
	if version := s.cachedArtifactVersion(statePanelVersion, filepath.Join(s.zashboardDir(), "version.txt")); version != "" {
		return version
	}
	return readTrimmedFile(filepath.Join(s.zashboardRoot(), "version.txt"))
}

func (s *Service) cachedArtifactVersion(key, legacyPath string) string {
	if s.store != nil {
		data, found, err := s.store.get(key)
		if err != nil {
			s.appendLogf("读取版本记录失败：%v", err)
		} else if found {
			return string(data)
		}
	}
	if legacyPath == "" {
		return ""
	}
	version := readTrimmedFile(legacyPath)
	if version != "" && s.store != nil {
		if err := s.store.put(key, []byte(version)); err != nil {
			s.appendLogf("迁移版本记录失败：%v", err)
		}
	}
	return version
}

func detectZashboardVersionFromAssets(root string) string {
	files, err := filepath.Glob(filepath.Join(root, "assets", "*.js"))
	if err != nil {
		return ""
	}

	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		match := zashboardAssetVersionPattern.FindSubmatch(content)
		if len(match) == 2 {
			return strings.TrimSpace(string(match[1]))
		}
	}

	return ""
}

func readTrimmedFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(content))
}

func sameVersion(current, latest string) bool {
	normalizedCurrent := normalizeVersion(current)
	if normalizedCurrent == "" {
		return false
	}

	return normalizedCurrent == normalizeVersion(latest)
}

func normalizeVersion(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	return strings.TrimPrefix(trimmed, "v")
}

func detectVersionFromText(value string) string {
	match := semanticVersionPattern.FindString(strings.TrimSpace(value))
	return strings.TrimSpace(match)
}

func detectVersionFromName(value string) string {
	return detectVersionFromText(filepath.Base(value))
}

func (s *Service) detectInstalledCoreVersion() string {
	if version := s.detectCoreVersionFromBinary(); version != "" {
		return version
	}

	if fileExists(s.coreExecutablePath()) {
		return s.cachedArtifactVersion(stateCoreVersion, filepath.Join(s.coreDir(), "version.txt"))
	}

	return ""
}

func (s *Service) detectCoreVersionFromBinary() string {
	if !fileExists(s.coreExecutablePath()) {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, s.coreExecutablePath(), "-v")
	output, err := command.CombinedOutput()
	if err != nil && len(output) == 0 {
		return ""
	}

	return detectVersionFromText(string(output))
}
