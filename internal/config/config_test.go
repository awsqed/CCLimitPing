package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useTempConfigDir points Dir() (via $XDG_CONFIG_HOME) at a fresh temp dir and
// returns the config file path inside it.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "limitping", "config.toml")
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReturnsDefaultsWithoutFile(t *testing.T) {
	useTempConfigDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	def := Default()
	if cfg.WeeklyThreshold != def.WeeklyThreshold || cfg.UsageDisplay != def.UsageDisplay ||
		cfg.ResetBuffer.Duration != def.ResetBuffer.Duration || cfg.Notify != def.Notify {
		t.Fatalf("Load() = %+v, want defaults %+v", cfg, def)
	}
	if !cfg.Claude.Enabled || !cfg.Codex.Enabled {
		t.Fatalf("default provider enablement = claude:%t codex:%t, want both true",
			cfg.Claude.Enabled, cfg.Codex.Enabled)
	}
}

func TestLoadMergesPartialFileOverDefaults(t *testing.T) {
	path := useTempConfigDir(t)
	writeConfig(t, path, `
weekly_threshold = 0.5
reset_buffer = "30s"

[claude]
enabled = false
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.WeeklyThreshold != 0.5 {
		t.Fatalf("weekly_threshold = %v, want 0.5", cfg.WeeklyThreshold)
	}
	if cfg.ResetBuffer.Duration != 30*time.Second {
		t.Fatalf("reset_buffer = %v, want 30s", cfg.ResetBuffer.Duration)
	}
	if cfg.Claude.Enabled {
		t.Fatal("claude.enabled = true, want the file's false to win")
	}
	// Untouched fields keep their defaults. Compared against Default() rather
	// than literals so retiring a provider model doesn't break this test.
	def := Default()
	if cfg.Claude.Model != def.Claude.Model || cfg.Codex.Model != def.Codex.Model ||
		cfg.UsageDisplay != def.UsageDisplay {
		t.Fatalf("defaults not preserved: claude.model=%q codex.model=%q usage_display=%q",
			cfg.Claude.Model, cfg.Codex.Model, cfg.UsageDisplay)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := map[string]string{
		"weekly_threshold out of range": "weekly_threshold = 1.5",
		"negative reset_buffer":         `reset_buffer = "-5s"`,
		"unknown usage_display":         `usage_display = "percent"`,
		"malformed TOML":                "weekly_threshold = =",
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := useTempConfigDir(t)
			writeConfig(t, path, contents)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted invalid config %q", contents)
			}
		})
	}
}

func TestDurationRoundTrip(t *testing.T) {
	var d Duration
	if err := d.UnmarshalText([]byte("90m")); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if d.Duration != 90*time.Minute {
		t.Fatalf("parsed %v, want 90m", d.Duration)
	}
	out, err := d.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(out) != "1h30m0s" {
		t.Fatalf("marshaled %q, want 1h30m0s", out)
	}
	if err := d.UnmarshalText([]byte("not-a-duration")); err == nil {
		t.Fatal("UnmarshalText accepted garbage")
	}
}

func TestLoadReadsActiveHours(t *testing.T) {
	path := useTempConfigDir(t)
	writeConfig(t, path, `
[opencode_go]
enabled = true
active_hours = "07:00-22:00"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.OpenCodeGo.ActiveHours.String(); got != "07:00-22:00" {
		t.Fatalf("opencode_go.active_hours = %q, want 07:00-22:00", got)
	}
	if !cfg.Claude.ActiveHours.IsZero() || !cfg.Codex.ActiveHours.IsZero() {
		t.Fatal("active_hours leaked into another provider")
	}
}

func TestLoadRejectsInvalidActiveHours(t *testing.T) {
	path := useTempConfigDir(t)
	writeConfig(t, path, `
[opencode_go]
active_hours = "22:00-07:00"
`)
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an overnight active_hours range")
	}
}

func TestOpenCodeGoIsOptIn(t *testing.T) {
	def := Default()
	if def.OpenCodeGo.Enabled {
		t.Fatal("opencode_go.enabled defaults to true; an install without the CLI would fail status")
	}
	if def.OpenCodeGo.Model == "" {
		t.Fatal("opencode_go.model default is empty")
	}

	path := useTempConfigDir(t)
	writeConfig(t, path, `
[opencode_go]
enabled = true
model = "opencode-go/glm-5.3-flash"
prompt = "hi"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.OpenCodeGo.Enabled || cfg.OpenCodeGo.Model != "opencode-go/glm-5.3-flash" || cfg.OpenCodeGo.Prompt != "hi" {
		t.Fatalf("opencode_go = %+v, want the file's values", cfg.OpenCodeGo)
	}
}

func TestDefaultConfigValidates(t *testing.T) {
	if err := Default().validate(); err != nil {
		t.Fatalf("Default() fails its own validation: %v", err)
	}
}

func TestWriteDefaultThenLoad(t *testing.T) {
	path := useTempConfigDir(t)

	got, err := WriteDefault(false)
	if err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	if got != path {
		t.Fatalf("WriteDefault path = %q, want %q", got, path)
	}

	// The written template must parse, validate, and match the built-in
	// defaults on the key knobs.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() of written default: %v", err)
	}
	def := Default()
	if cfg.WeeklyThreshold != def.WeeklyThreshold ||
		cfg.ResetBuffer.Duration != def.ResetBuffer.Duration ||
		cfg.Claude.Model != def.Claude.Model ||
		cfg.Codex.Model != def.Codex.Model ||
		cfg.Codex.AutoRedeem != def.Codex.AutoRedeem {
		t.Fatalf("written default drifted from Default(): %+v vs %+v", cfg, def)
	}
}

func TestWriteDefaultRefusesOverwrite(t *testing.T) {
	useTempConfigDir(t)

	if _, err := WriteDefault(false); err != nil {
		t.Fatalf("first WriteDefault: %v", err)
	}
	if _, err := WriteDefault(false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second WriteDefault error = %v, want refusal without --force", err)
	}
	if _, err := WriteDefault(true); err != nil {
		t.Fatalf("WriteDefault(force) error = %v, want overwrite to succeed", err)
	}
}
