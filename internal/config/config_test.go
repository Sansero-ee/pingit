package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseConfigValid(t *testing.T) {
	data := []byte("# comment\n8.8.8.8\n\nexample.com\n2001:db8::1\nduration=60s, interval=1s\n")
	targets, dur, iv, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	want := []string{"8.8.8.8", "example.com", "2001:db8::1"}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	if dur != 60*time.Second {
		t.Fatalf("duration = %v, want 60s", dur)
	}
	if iv != time.Second {
		t.Fatalf("interval = %v, want 1s", iv)
	}
}

func TestParseConfigSeparateSettingLines(t *testing.T) {
	data := []byte("host1\ninterval=500ms\nduration=2m\n")
	targets, dur, iv, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(targets) != 1 || targets[0] != "host1" {
		t.Fatalf("targets = %v, want [host1]", targets)
	}
	if dur != 2*time.Minute {
		t.Fatalf("duration = %v, want 2m", dur)
	}
	if iv != 500*time.Millisecond {
		t.Fatalf("interval = %v, want 500ms", iv)
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"no settings", "8.8.8.8\n"},
		{"missing interval", "8.8.8.8\nduration=60s\n"},
		{"missing duration", "8.8.8.8\ninterval=1s\n"},
		{"bad duration", "8.8.8.8\nduration=abc, interval=1s\n"},
		{"bad interval", "8.8.8.8\nduration=60s, interval=xyz\n"},
		{"no targets", "duration=60s, interval=1s\n"},
		{"unknown key in settings", "8.8.8.8\nduration=60s, foo=1s\n"},
		{"empty file", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := ParseConfig([]byte(tc.data)); err == nil {
				t.Fatalf("expected error for %q", tc.data)
			}
		})
	}
}

func TestParseConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.txt")
	content := "# targets\n8.8.8.8\nexample.com\nduration=30s, interval=2s\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	targets, dur, iv, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}
	if !reflect.DeepEqual(targets, []string{"8.8.8.8", "example.com"}) {
		t.Fatalf("targets = %v", targets)
	}
	if dur != 30*time.Second || iv != 2*time.Second {
		t.Fatalf("duration = %v, interval = %v", dur, iv)
	}
}

func TestParseConfigFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.txt")
	if _, _, _, err := ParseConfigFile(path); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidateTargetsDefaultLimit(t *testing.T) {
	targets := make([]string, DefaultMaxTargets)
	for i := range targets {
		targets[i] = fmt.Sprintf("10.0.0.%d", i+1)
	}
	if err := ValidateTargets(targets, false); err != nil {
		t.Fatalf("expected %d targets to pass: %v", len(targets), err)
	}
	if err := ValidateTargets(append(targets, "10.0.0.200"), false); err == nil {
		t.Fatalf("expected error above default limit %d", DefaultMaxTargets)
	}
}

func TestValidateTargetsExtendedLimit(t *testing.T) {
	targets := make([]string, ExtendedMaxTargets)
	for i := range targets {
		targets[i] = fmt.Sprintf("10.0.%d.%d", i/256, i%256+1)
	}
	if err := ValidateTargets(targets, true); err != nil {
		t.Fatalf("expected %d targets to pass with extended limit: %v", len(targets), err)
	}
	if err := ValidateTargets(append(targets, "10.1.0.1"), true); err == nil {
		t.Fatalf("expected error above extended limit %d", ExtendedMaxTargets)
	}
}

func TestValidateTargetsEmpty(t *testing.T) {
	if err := ValidateTargets(nil, false); err == nil {
		t.Fatal("expected error for empty target list")
	}
}
