package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultMaxTargets  = 10
	ExtendedMaxTargets = 100
)

func DefaultLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, "pinglog-"+time.Now().Format("20060102-150405")+".jsonl")
}

func ValidateTargets(targets []string, allowExtended bool) error {
	limit := DefaultMaxTargets
	if allowExtended {
		limit = ExtendedMaxTargets
	}
	if len(targets) == 0 {
		return fmt.Errorf("no targets specified")
	}
	if len(targets) > limit {
		return fmt.Errorf("too many targets: %d (limit is %d, or %d with the extended-limit option)",
			len(targets), DefaultMaxTargets, ExtendedMaxTargets)
	}
	return nil
}

func ParseConfigFile(path string) ([]string, time.Duration, time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	return ParseConfig(data)
}

func ParseConfig(data []byte) ([]string, time.Duration, time.Duration, error) {
	var targets []string
	var duration, interval time.Duration
	haveDuration, haveInterval := false, false

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isSettingLine(line) {
			for _, part := range strings.Split(line, ",") {
				part = strings.TrimSpace(part)
				key, value, ok := strings.Cut(part, "=")
				if !ok {
					return nil, 0, 0, fmt.Errorf("invalid setting %q", part)
				}
				key = strings.TrimSpace(key)
				value = strings.TrimSpace(value)
				d, err := time.ParseDuration(value)
				if err != nil {
					return nil, 0, 0, fmt.Errorf("invalid %s %q: %w", key, value, err)
				}
				switch key {
				case "duration":
					duration, haveDuration = d, true
				case "interval":
					interval, haveInterval = d, true
				default:
					return nil, 0, 0, fmt.Errorf("unknown setting %q", key)
				}
			}
			continue
		}
		targets = append(targets, line)
	}

	if len(targets) == 0 {
		return nil, 0, 0, fmt.Errorf("config contains no targets")
	}
	if !haveDuration {
		return nil, 0, 0, fmt.Errorf("config is missing duration (last line: duration=60s, interval=1s)")
	}
	if !haveInterval {
		return nil, 0, 0, fmt.Errorf("config is missing interval (last line: duration=60s, interval=1s)")
	}
	if duration <= 0 || interval <= 0 {
		return nil, 0, 0, fmt.Errorf("duration and interval must be positive")
	}
	return targets, duration, interval, nil
}

func isSettingLine(line string) bool {
	return strings.HasPrefix(line, "duration=") || strings.HasPrefix(line, "interval=")
}
