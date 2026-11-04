package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

const (
	// opencodeGoDefaultModel is used when the config names no model. Any paid
	// Go model starts a window; this one has a large monthly allowance and a
	// low price, so a ping costs little and does not eat a scarce model's cap.
	opencodeGoDefaultModel  = "opencode-go/deepseek-v4.1-flash"
	opencodeGoDefaultPrompt = "Do not perform any work. Reply with exactly: OK"
	// opencodeGoPingTitle labels the synthetic session, and keeps the CLI from
	// spending a request generating a title of its own.
	opencodeGoPingTitle = "limitping-ping"
	// opencodeGoCleanupTimeout bounds the cleanup `opencode session delete` after
	// the ping itself has already finished.
	opencodeGoCleanupTimeout = 10 * time.Second
	// opencodeGoAnchorSkew is how much clock disagreement opencodeGoWindow
	// tolerates when deciding whether a rolling window has actually started.
	opencodeGoAnchorSkew = 5 * time.Second
)

// OpenCodeGo reads the Go allowance through OpenCode's own local API and
// triggers windows with a headless `opencode run`. Both paths therefore reuse
// the credentials the opencode CLI already holds, including the OAuth refresh
// it performs, and no separate login or key is needed.
type OpenCodeGo struct {
	cfg config.ProviderConfig
}

func NewOpenCodeGo(cfg config.ProviderConfig) *OpenCodeGo {
	return &OpenCodeGo{cfg: cfg}
}

func (o *OpenCodeGo) Name() string { return "opencode-go" }

// opencodeGoUsageEnvelope is the shape of
// `opencode api experimental.usage.limits`.
type opencodeGoUsageEnvelope struct {
	Data *struct {
		Workspace struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"workspace"`
		Go *struct {
			Rolling opencodeGoMeter `json:"rolling"`
			Weekly  opencodeGoMeter `json:"weekly"`
			Monthly opencodeGoMeter `json:"monthly"`
		} `json:"go"`
	} `json:"data"`
}

// opencodeGoMeter is one of Go's allowance meters. ResetsAt is milliseconds
// since the Unix epoch, and Limited is the backend's own verdict that the
// meter is at its limit.
type opencodeGoMeter struct {
	Percent  float64 `json:"percent"`
	Limited  bool    `json:"limited"`
	ResetsAt int64   `json:"resetsAt"`
}

func (o *OpenCodeGo) ReadUsage(ctx context.Context) (*usage.Usage, error) {
	// Stamp the read before the command runs: while no window is running, the
	// API reports one ending a full 5h from the moment it handles the request.
	// Read after the command, that hypothetical reset is a few seconds short of
	// 5h and looks anchored; read before it, it is at least 5h away.
	readAt := time.Now()
	raw, err := opencodeGoUsageJSON(ctx)
	if err != nil {
		return nil, err
	}
	return opencodeGoUsageFromJSON(raw, readAt)
}

// opencodeGoUsageJSON runs the local OpenCode API. --standalone keeps the read
// working whether or not the background server happens to be running; the
// server it starts handles Console OAuth refresh and org selection, which a
// direct HTTP call would have to reproduce.
func opencodeGoUsageJSON(ctx context.Context) ([]byte, error) {
	dir, configDir, err := opencodeGoHome()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "opencode", "--standalone", "api", "experimental.usage.limits")
	cmd.Dir = dir
	cmd.Env = opencodeGoEnv(configDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Left nil so the child reads /dev/null rather than an open stdin.
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opencode usage read failed: %w: %s", err, opencodeGoTail(stderr.Bytes(), stdout.Bytes()))
	}
	return stdout.Bytes(), nil
}

// opencodeGoUsageFromJSON normalizes the API response into the shared model. A
// nil data is an account that is not signed in; a missing go block is a
// workspace without a Go subscription.
func opencodeGoUsageFromJSON(raw []byte, now time.Time) (*usage.Usage, error) {
	var env opencodeGoUsageEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("opencode usage response: %w", err)
	}
	if env.Data == nil {
		return nil, errors.New("opencode-go: not signed in to OpenCode Console (run `opencode auth login`)")
	}
	if env.Data.Go == nil {
		return nil, fmt.Errorf("opencode-go: workspace %q has no OpenCode Go subscription", env.Data.Workspace.Name)
	}
	g := env.Data.Go
	u := &usage.Usage{
		Provider:  "opencode-go",
		Plan:      "Go",
		FiveHour:  opencodeGoWindow(g.Rolling, 5*time.Hour, now),
		Weekly:    opencodeGoWindow(g.Weekly, 7*24*time.Hour, now),
		Monthly:   opencodeGoWindow(g.Monthly, 0, now),
		FetchedAt: now,
		Raw:       append([]byte(nil), raw...),
	}
	return u, nil
}

func opencodeGoWindow(m opencodeGoMeter, length time.Duration, now time.Time) usage.Window {
	w := usage.Window{
		UsedPercent:   m.Percent,
		WindowSeconds: int(length / time.Second),
	}
	if m.Limited {
		w.UsedPercent = 100
	}
	if m.ResetsAt > 0 {
		w.ResetsAt = time.UnixMilli(m.ResetsAt)
		// A rolling window that ended in the past is not running: the next
		// request starts a new one. Leaving the stale reset on would make
		// watch wait out a window that is already free.
		//
		// A rolling window with its whole length still ahead is not running
		// either. The API never says "no window is running": it answers with a
		// hypothetical one that resets five hours after the read, so its reset
		// time slides forward on every poll. Taking that at face value made
		// watch sit on a window it had never started and never ping. now is
		// stamped before the command runs (see ReadUsage), so an idle window is
		// at least its full length away while an anchored one is strictly
		// closer; the skew only absorbs clock disagreement.
		if length == 5*time.Hour {
			remaining := w.ResetsAt.Sub(now)
			if remaining <= 0 || (w.UsedPercent == 0 && remaining >= length-opencodeGoAnchorSkew) {
				w.ResetsAt = time.Time{}
			}
		}
	}
	return w
}

func (o *OpenCodeGo) Trigger(ctx context.Context, dryRun bool) (*TriggerResult, error) {
	prompt := o.cfg.Prompt
	if prompt == "" {
		prompt = opencodeGoDefaultPrompt
	}
	model := o.cfg.Model
	if model == "" {
		model = opencodeGoDefaultModel
	}
	// A session id chosen up front lets the synthetic session be deleted
	// afterwards; `opencode run` cannot skip persisting it. --title keeps the
	// CLI from spending a second request generating one.
	sessionID := opencodeGoSessionID()
	args := []string{
		// --standalone is what makes the isolation below authoritative: a
		// long-running background server would have been started with the
		// user's own global config, and would ignore this ping's.
		"--standalone", "run", "--format", "json",
		"--model", model,
		"--session", sessionID,
		"--title", opencodeGoPingTitle,
	}
	args = append(args, o.cfg.ExtraArgs...)
	args = append(args, prompt)
	res := &TriggerResult{
		Command: "opencode " + shellJoin(args),
		Model:   model,
	}
	if dryRun {
		return res, nil
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	// Run the ping in a private, empty project with no global instructions and
	// every tool denied. Without that isolation the model reads whatever
	// AGENTS.md the working directory carries, decides to do work, and turns a
	// one-request ping into a long tool loop — the very cost the ping exists to
	// avoid.
	dir, configDir, err := opencodeGoHome()
	if err != nil {
		return res, err
	}
	cmd.Dir = dir
	cmd.Env = opencodeGoEnv(configDir)
	err = cmd.Run()
	opencodeGoDeleteSession(sessionID, dir, cmd.Env)
	if err != nil {
		return res, fmt.Errorf("opencode run failed: %w: %s", err, opencodeGoTail(stderr.Bytes(), stdout.Bytes()))
	}
	if !opencodeGoRunUsage(stdout.Bytes(), res) {
		// A clean exit with no finished step means nothing reached the model,
		// so no window was started.
		return res, fmt.Errorf("opencode run finished no step, so no window was started: %s",
			opencodeGoTail(stderr.Bytes(), stdout.Bytes()))
	}
	return res, nil
}

// opencodeGoEvent is the part of one `opencode run --format json` line this
// package reads. The final step_finish carries the turn's tokens and cost.
type opencodeGoEvent struct {
	Type string `json:"type"`
	Part struct {
		Type   string  `json:"type"`
		Cost   float64 `json:"cost"`
		Tokens struct {
			Input     int `json:"input"`
			Output    int `json:"output"`
			Reasoning int `json:"reasoning"`
			Cache     struct {
				Read  int `json:"read"`
				Write int `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
	} `json:"part"`
}

// opencodeGoRunUsage reads the tokens and cost out of the JSONL events and
// reports whether a finished step was seen at all.
func opencodeGoRunUsage(out []byte, res *TriggerResult) bool {
	var input, output int
	finished := false
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev opencodeGoEvent
		if json.Unmarshal(line, &ev) != nil || ev.Type != "step_finish" {
			continue
		}
		finished = true
		t := ev.Part.Tokens
		// input excludes cache reads and writes; output excludes reasoning.
		// Fold both back in so "in / out" matches what the request processed.
		input += t.Input + t.Cache.Read + t.Cache.Write
		output += t.Output + t.Reasoning
		res.CostUSD += ev.Part.Cost
	}
	if !finished {
		return false
	}
	res.HasUsage = true
	res.InputTokens = input
	res.OutputTokens = output
	res.TotalTokens = input + output
	return true
}

func opencodeGoSessionID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ses_lp%d", time.Now().UnixNano())
	}
	return "ses_lp" + hex.EncodeToString(b[:])
}

// opencodeGoConfig is the entire config of limitping's private OpenCode
// instance: deny every tool. OpenCode commands only have to be billable
// requests, and a tool call would turn one into a longer, more expensive turn.
const opencodeGoConfig = `{
  "$schema": "https://opencode.ai/config.json",
  "permissions": [
    { "action": "*", "resource": "*", "effect": "deny" }
  ]
}
`

// opencodeGoHome returns the private project directory limitping's OpenCode
// commands run in and the private config directory they load. Both live under
// the user cache dir and are reused, so pings do not accumulate projects in
// OpenCode's session list.
func opencodeGoHome() (dir, configDir string, err error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	root := filepath.Join(base, "limitping")
	dir = filepath.Join(root, "opencode-ping")
	configDir = filepath.Join(root, "opencode-ping-config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create opencode ping dir: %w", err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create opencode ping config dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "opencode.json"), []byte(opencodeGoConfig), 0o600); err != nil {
		return "", "", fmt.Errorf("write opencode ping config: %w", err)
	}
	return dir, configDir, nil
}

// opencodeGoEnv is the current environment with OpenCode's config knobs
// replaced by limitping's own, so the user's global AGENTS.md, plugins and
// providers cannot enlarge the request.
func opencodeGoEnv(configDir string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "OPENCODE_CONFIG_DIR=") ||
			strings.HasPrefix(kv, "OPENCODE_CONFIG_CONTENT=") ||
			strings.HasPrefix(kv, "OPENCODE_CONFIG_PROJECT_DISABLE=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "OPENCODE_CONFIG_DIR="+configDir)
}

// opencodeGoDeleteSession removes the ping's synthetic session, best effort:
// the ping already succeeded, and a leftover session is not worth reporting as
// a failed ping.
func opencodeGoDeleteSession(id, dir string, env []string) {
	ctx, cancel := context.WithTimeout(context.Background(), opencodeGoCleanupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "opencode", "session", "delete", id)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = nil
	_ = cmd.Run()
}

func opencodeGoTail(stdout, stderr []byte) string {
	tail := strings.TrimSpace(string(stderr))
	if tail == "" {
		tail = strings.TrimSpace(string(stdout))
	}
	const max = 500
	if len(tail) > max {
		tail = "..." + tail[len(tail)-max:]
	}
	return tail
}
