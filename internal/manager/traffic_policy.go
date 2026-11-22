package manager

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	trafficKeyLayout       = "2006-01-02T15:04"
	defaultRetentionMonths = 12
	defaultTrafficSizeMiB  = 32
	trafficMiB             = 1024 * 1024
)

type trafficClock struct{ day, hour, minute int }

func (c trafficClock) String() string {
	return fmt.Sprintf("%d %02d:%02d", c.day, c.hour, c.minute)
}

type trafficPolicy struct {
	start, end      trafficClock
	retentionMonths int
	maxBytes        int
}

func defaultTrafficPolicy(day int) trafficPolicy {
	boundary := trafficClock{day: day}
	return trafficPolicy{start: boundary, end: boundary, retentionMonths: defaultRetentionMonths, maxBytes: defaultTrafficSizeMiB * trafficMiB}
}

func loadTrafficPolicy(path string, fallback trafficPolicy) (trafficPolicy, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	var config struct {
		Traffic struct {
			Start           *string `yaml:"start"`
			End             *string `yaml:"end"`
			RetentionMonths *int    `yaml:"retention-months"`
			MaxSizeMiB      *int    `yaml:"max-size-mib"`
		} `yaml:"traffic"`
	}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return fallback, err
	}

	policy := fallback
	if input := config.Traffic.Start; input != nil {
		value, err := parseTrafficClock(*input)
		if err != nil {
			return fallback, fmt.Errorf("traffic.start：%w", err)
		}
		policy.start, policy.end = value, value
	}
	if input := config.Traffic.End; input != nil {
		value, err := parseTrafficClock(*input)
		if err != nil {
			return fallback, fmt.Errorf("traffic.end：%w", err)
		}
		policy.end = value
	}
	if value := config.Traffic.RetentionMonths; value != nil {
		if *value < 1 || *value > 120 {
			return fallback, errors.New("traffic.retention-months 必须介于 1 和 120")
		}
		policy.retentionMonths = *value
	}
	if value := config.Traffic.MaxSizeMiB; value != nil {
		if *value < 1 || *value > 256 {
			return fallback, errors.New("traffic.max-size-mib 必须介于 1 和 256")
		}
		policy.maxBytes = *value * trafficMiB
	}
	return policy, nil
}

func parseTrafficClock(input string) (trafficClock, error) {
	const invalid = "请使用 日 HH:MM 格式，日期范围 1 到 31"
	parts := strings.Fields(input)
	if len(parts) != 2 || len(parts[1]) != len("15:04") {
		return trafficClock{}, errors.New(invalid)
	}
	day, dayErr := strconv.Atoi(parts[0])
	clock, clockErr := time.Parse("15:04", parts[1])
	if dayErr != nil || clockErr != nil || day < 1 || day > 31 {
		return trafficClock{}, errors.New(invalid)
	}
	return trafficClock{day, clock.Hour(), clock.Minute()}, nil
}

func (c trafficClock) at(month time.Time) time.Time {
	lastDay := month.AddDate(0, 1, -1).Day()
	return time.Date(month.Year(), month.Month(), min(c.day, lastDay), c.hour, c.minute, 0, 0, month.Location())
}

func (c trafficClock) order() int {
	return c.day*1440 + c.hour*60 + c.minute
}

func trafficMonth(at time.Time) time.Time {
	// 用月初正午作为锚点，月份运算不受午夜的夏令时跳变影响。
	return time.Date(at.Year(), at.Month(), 1, 12, 0, 0, 0, at.Location())
}

func (p trafficPolicy) currentMonth(at time.Time) time.Time {
	month := trafficMonth(at)
	if at.Before(p.start.at(month)) {
		return month.AddDate(0, -1, 0)
	}
	return month
}

func (p trafficPolicy) bounds(month time.Time) (start, end time.Time) {
	start = p.start.at(month)
	next := month.AddDate(0, 1, 0)
	endMonth := month
	if p.end.order() <= p.start.order() {
		endMonth = next
	}
	end = p.end.at(endMonth)
	// 短月截到月末时，两个界限可能交错。确保周期不重叠。
	if end.Before(start) {
		end = start
	}
	if nextStart := p.start.at(next); end.After(nextStart) {
		end = nextStart
	}
	return start, end
}

func (p trafficPolicy) bucket(at time.Time) string {
	day := at.Format(time.DateOnly)
	start, end := p.bounds(p.currentMonth(at))
	// 平时只存日合计，遇到周期起止才在当天增加一条分段边界。
	boundary := start
	if !at.Before(end) {
		boundary = end
	}
	if boundary.Format(time.DateOnly) == day {
		return boundary.Format(trafficKeyLayout)
	}
	return day + "T00:00"
}
