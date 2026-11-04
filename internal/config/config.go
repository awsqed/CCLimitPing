// Package config loads and validates limitping's TOML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration that (un)marshals from TOML as a string like
// "10s" so the config file stays human-friendly.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// ProviderConfig holds the per-provider knobs. ReasoningEffort applies to
// Codex-backed providers and is ignored by Claude.
type ProviderConfig struct {
	Enabled         bool     `toml:"enabled"`
	Prompt          string   `toml:"prompt"`
	ExtraArgs       []string `toml:"extra_args"`
	Model           string   `toml:"model"`
	ReasoningEffort string   `toml:"reasoning_effort"`
	AlignStart      string   `toml:"align_start"`
	// ContinuePrompt is the message injected into a proxied session
	// (`limitping continue <provider>`) when the 5h limit recovers.
	// Empty falls back to "continue".
	ContinuePrompt string `toml:"continue_prompt"`
	// AutoRedeem lets `watch` and `continue` spend a banked reset credit that is
	// about to lapse (a Codex reset credit or a Claude reset card). Off by
	// default: redeeming is irreversible.
	AutoRedeem bool `toml:"auto_redeem"`
	// ActiveHours restricts pings to a local-time range, e.g. "07:00-22:00".
	// Outside it, watch waits for the range to open again instead of starting
	// a window. Empty means no restriction.
	ActiveHours Hours `toml:"active_hours"`
}

// Config is the full configuration.
type Config struct {
	// WeeklyThreshold: skip pinging when the weekly window's utilization
	// (0..1) is at or above this value, until the weekly window resets.
	WeeklyThreshold float64 `toml:"weekly_threshold"`
	// ResetBuffer: wait this long after a window's reset time before pinging,
	// to be sure the window has actually rolled over.
	ResetBuffer Duration `toml:"reset_buffer"`
	// Notify: emit macOS notifications on ping success/failure/skip.
	Notify bool `toml:"notify"`
	// UsageDisplay controls the text status percentage: "used" preserves the raw
	// provider usage value; "remaining" shows the complement users see in some UIs.
	UsageDisplay string `toml:"usage_display"`
	// AlignStart is the global RFC3339 anchor for the first window, applied to
	// every provider that leaves its own align_start empty. Empty = start ASAP.
	AlignStart string `toml:"align_start"`
	// ActiveHours is the global local-time range (e.g. "07:00-22:00") applied
	// to every provider that leaves its own active_hours empty. The zero value
	// means no restriction.
	ActiveHours Hours `toml:"active_hours"`

	Claude     ProviderConfig `toml:"claude"`
	Codex      ProviderConfig `toml:"codex"`
	OpenCodeGo ProviderConfig `toml:"opencode_go"`
}

// Default returns the built-in defaults used when no config file exists.
func Default() Config {
	return Config{
		WeeklyThreshold: 0.99,
		ResetBuffer:     Duration{10 * time.Second},
		Notify:          true,
		UsageDisplay:    "used",
		Claude: ProviderConfig{
			Enabled:        true,
			Prompt:         ".",
			Model:          "haiku",
			ExtraArgs:      []string{},
			ContinuePrompt: "continue",
		},
		Codex: ProviderConfig{
			Enabled: true,
			Prompt:  "ok",
			// Empty means limitping picks the cheapest model in the Codex
			// catalog at ping time. Naming one here is what went stale when
			// OpenAI retired gpt-5.4-mini.
			Model:           "",
			ReasoningEffort: "low",
			ContinuePrompt:  "continue",
		},
		OpenCodeGo: ProviderConfig{
			// Opt-in: an existing install may have no opencode CLI or Go
			// subscription, and status must not fail for it.
			Enabled: false,
			Prompt:  "Do not perform any work. Reply with exactly: OK",
			// A large monthly allowance and a low price: a ping only has to be
			// billable, and it should not burn a scarce model's allowance.
			Model:          "opencode-go/deepseek-v4.1-flash",
			ExtraArgs:      []string{},
			ContinuePrompt: "continue",
		},
	}
}

// Dir returns limitping's config directory, honoring $XDG_CONFIG_HOME and
// falling back to ~/.config/limitping.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "limitping"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "limitping"), nil
}

// Path returns the absolute path of the config file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads the config file, applying defaults for any missing fields. If the
// file does not exist, the full default config is returned.
func Load() (Config, error) {
	cfg := Default()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	} else if err != nil {
		return cfg, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.applyGlobal()
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// applyGlobal fills each provider's unset scheduling options from the
// top-level values. A provider that sets its own value keeps it.
func (c *Config) applyGlobal() {
	apply := func(pc *ProviderConfig) {
		if pc.AlignStart == "" {
			pc.AlignStart = c.AlignStart
		}
		if pc.ActiveHours.IsZero() {
			pc.ActiveHours = c.ActiveHours
		}
	}
	apply(&c.Claude)
	apply(&c.Codex)
	apply(&c.OpenCodeGo)
}

func (c Config) validate() error {
	if c.WeeklyThreshold < 0 || c.WeeklyThreshold > 1 {
		return fmt.Errorf("weekly_threshold must be between 0 and 1, got %v", c.WeeklyThreshold)
	}
	if c.ResetBuffer.Duration < 0 {
		return errors.New("reset_buffer must not be negative")
	}
	if c.UsageDisplay != "" && c.UsageDisplay != "used" && c.UsageDisplay != "remaining" {
		return fmt.Errorf("usage_display must be \"used\" or \"remaining\", got %q", c.UsageDisplay)
	}
	if c.AlignStart != "" {
		if _, err := time.Parse(time.RFC3339, c.AlignStart); err != nil {
			return fmt.Errorf("align_start: %w", err)
		}
	}
	return nil
}

// WriteDefault writes a commented default config to Path(). It refuses to
// overwrite an existing file unless force is true.
func WriteDefault(force bool) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.toml")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("config already exists at %s (use --force to overwrite)", path)
		}
	}
	if err := os.WriteFile(path, []byte(defaultTOML), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

const defaultTOML = `# limitping configuration

# Skip pinging when the weekly window utilization (0..1) is at/above this,
# until the weekly window resets.
weekly_threshold = 0.99

# Wait this long after a window's reset before pinging (ensures rollover).
reset_buffer = "10s"

# Emit macOS notifications on ping success/failure/skip.
notify = true

# Text status percentage: "used" or "remaining".
usage_display = "used"

# Optional RFC3339 anchor for the first window, applied to every provider that
# leaves its own align_start empty; empty = start ASAP.
align_start = ""

# Global local-time range applied to every provider that leaves its own
# active_hours empty, e.g. "07:00-22:00"; empty = around the clock.
active_hours = ""

[claude]
enabled = true
prompt = "."
# Cheapest tier; triggering doesn't need a SOTA model and this avoids burning
# Sonnet/Opus budget (incl. the separate weekly Opus bucket). Alias or full id.
model = "haiku"
# Extra Claude CLI args. Headless/print-only flags such as -p, --print,
# --output-format, and --max-turns are ignored.
extra_args = []
# Optional RFC3339 anchor for the first window's phase; empty = inherit the
# global align_start (or start ASAP if that is empty too).
align_start = ""
# Only start windows inside this local-time range (start inclusive, end
# exclusive), e.g. "07:00-22:00"; empty = inherit the global active_hours (or
# around the clock if that is empty too). Outside it, watch waits for the next
# start instead of pinging.
active_hours = ""
# Message injected to resume a proxied session when the 5h limit recovers
# (limitping continue claude). Empty = "continue".
continue_prompt = "continue"
# Let watch/continue spend a Claude reset card that is about to lapse (within
# 24h with usage to reclaim, or in its final hour). Redeeming is irreversible;
# "limitping redeem claude" does it manually. See "limitping help redeem".
auto_redeem = false

[codex]
enabled = true
prompt = "ok"
# Model used to trigger a window. Empty = pick the cheapest one your plan
# offers, read from the Codex CLI's own catalog at ping time — a ping only has
# to be billable, so this stays correct as OpenAI retires and adds models.
# Name a model here to pin it (see ~/.codex/models_cache.json for the list).
model = ""
# "low" keeps the ping cheap; "minimal" is rejected when web_search/image_gen
# tools are enabled in your Codex config.
reasoning_effort = "low"
extra_args = []
# Optional RFC3339 anchor for the first window's phase; empty = inherit the
# global align_start (or start ASAP if that is empty too).
align_start = ""
# Only start windows inside this local-time range, e.g. "07:00-22:00"; empty =
# inherit the global active_hours (or around the clock if that is empty too).
active_hours = ""
# Message injected to resume a proxied session when the 5h limit recovers.
continue_prompt = "continue"
# Let watch/continue spend a banked reset credit that is about to lapse (within
# 24h with usage to reclaim, or in its final hour). Redeeming is irreversible;
# "limitping redeem" does it manually. See "limitping help redeem".
auto_redeem = false

[opencode_go]
# Opt-in: requires the opencode CLI signed in to an OpenCode Console workspace
# with a Go, Go Plus or Go Teams subscription.
enabled = false
prompt = "Do not perform any work. Reply with exactly: OK"
# Any paid OpenCode Go model starts a window. This one has a large monthly
# allowance; a ping only has to be billable.
model = "opencode-go/deepseek-v4.1-flash"
# Extra "opencode run" args.
extra_args = []
# Optional RFC3339 anchor for the first window's phase; empty = inherit the
# global align_start (or start ASAP if that is empty too).
align_start = ""
# Only start windows inside this local-time range, e.g. "07:00-22:00"; empty =
# inherit the global active_hours (or around the clock if that is empty too).
active_hours = ""
`
