package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
)

func TestOpenCodeGoUsageFromJSON(t *testing.T) {
	now := time.Unix(1793673762, 0)
	raw := []byte(`{
		"location":{"directory":"/home/me"},
		"data":{
			"workspace":{"id":"org_1","name":"me"},
			"go":{
				"rolling":{"percent":0,"limited":false,"resetsAt":1793691593000},
				"weekly":{"percent":9,"limited":false,"resetsAt":1794182400000},
				"monthly":{"percent":4,"limited":false,"resetsAt":1796120382000}
			},
			"budgets":[]
		}
	}`)
	u, err := opencodeGoUsageFromJSON(raw, now)
	if err != nil {
		t.Fatalf("opencodeGoUsageFromJSON: %v", err)
	}
	if u.Provider != "opencode-go" || u.Plan != "Go" {
		t.Fatalf("provider/plan = %q/%q", u.Provider, u.Plan)
	}
	if !u.FiveHour.Active() {
		t.Errorf("rolling window not active: %+v", u.FiveHour)
	}
	if u.FiveHour.WindowSeconds != 5*60*60 {
		t.Errorf("rolling window seconds = %d, want 5h", u.FiveHour.WindowSeconds)
	}
	if !u.Weekly.Active() || u.Weekly.UsedPercent != 9 {
		t.Errorf("weekly = %+v", u.Weekly)
	}
	if u.Monthly.Missing() || u.Monthly.UsedPercent != 4 || !u.Monthly.Active() {
		t.Errorf("monthly = %+v", u.Monthly)
	}
	if len(u.Raw) == 0 {
		t.Error("raw response not kept")
	}
}

func TestOpenCodeGoLimitedMeterReadsFull(t *testing.T) {
	now := time.Now()
	raw := []byte(`{"data":{"workspace":{"name":"me"},"go":{
		"rolling":{"percent":42,"limited":true,"resetsAt":1793691593000},
		"weekly":{"percent":0,"limited":false,"resetsAt":1794182400000},
		"monthly":{"percent":0,"limited":false,"resetsAt":1796120382000}}}}`)
	u, err := opencodeGoUsageFromJSON(raw, now)
	if err != nil {
		t.Fatalf("opencodeGoUsageFromJSON: %v", err)
	}
	if u.FiveHour.UsedPercent != 100 {
		t.Fatalf("limited rolling window = %.0f%%, want 100%%", u.FiveHour.UsedPercent)
	}
}

func TestOpenCodeGoUsageNeedsSignInAndSubscription(t *testing.T) {
	if _, err := opencodeGoUsageFromJSON([]byte(`{"data":null}`), time.Now()); err == nil ||
		!strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("data:null error = %v, want a sign-in error", err)
	}
	raw := []byte(`{"data":{"workspace":{"name":"me"}}}`)
	if _, err := opencodeGoUsageFromJSON(raw, time.Now()); err == nil ||
		!strings.Contains(err.Error(), "no OpenCode Go subscription") {
		t.Fatalf("missing go error = %v, want a subscription error", err)
	}
}

// A rolling window that ended in the past is free again: leaving its stale
// reset time on would make watch wait out a window that never began.
func TestOpenCodeGoPastRollingResetIsCleared(t *testing.T) {
	now := time.Unix(1793673762, 0)
	raw := []byte(`{"data":{"workspace":{"name":"me"},"go":{
		"rolling":{"percent":0,"limited":false,"resetsAt":1793600000000},
		"weekly":{"percent":9,"limited":false,"resetsAt":1794182400000},
		"monthly":{"percent":4,"limited":false,"resetsAt":1796120382000}}}}`)
	u, err := opencodeGoUsageFromJSON(raw, now)
	if err != nil {
		t.Fatalf("opencodeGoUsageFromJSON: %v", err)
	}
	if u.FiveHour.Active() {
		t.Fatalf("past rolling window still active: %+v", u.FiveHour)
	}
	if !u.Weekly.ResetsAt.Equal(time.UnixMilli(1794182400000)) {
		t.Errorf("weekly reset = %v, want it kept", u.Weekly.ResetsAt)
	}
}

func TestOpenCodeGoRunUsage(t *testing.T) {
	out := []byte(`{"type":"step_start","sessionID":"ses_1"}
{"type":"text","sessionID":"ses_1","part":{"type":"text","text":"ok"}}
{"type":"step_finish","sessionID":"ses_1","part":{"type":"step-finish","reason":"stop","cost":0,"tokens":{"input":3234,"output":16,"reasoning":47,"cache":{"read":1792,"write":0}}}}
{"type":"step_finish","sessionID":"ses_1","part":{"type":"step-finish","cost":0.002,"tokens":{"input":10,"output":2,"reasoning":0,"cache":{"read":0,"write":0}}}}
`)
	var res TriggerResult
	if !opencodeGoRunUsage(out, &res) {
		t.Fatal("opencodeGoRunUsage reported no finished step")
	}
	if !res.HasUsage {
		t.Error("HasUsage = false")
	}
	if res.InputTokens != 3234+1792+10 || res.OutputTokens != 16+47+2 {
		t.Errorf("tokens = in %d / out %d, want in %d / out %d",
			res.InputTokens, res.OutputTokens, 3234+1792+10, 16+47+2)
	}
	if res.TotalTokens != res.InputTokens+res.OutputTokens {
		t.Errorf("total = %d, want in+out", res.TotalTokens)
	}
	if res.CostUSD != 0.002 {
		t.Errorf("cost = %v, want 0.002", res.CostUSD)
	}
}

func TestOpenCodeGoRunUsageWithoutStep(t *testing.T) {
	var res TriggerResult
	if opencodeGoRunUsage([]byte(`{"type":"step_start"}`+"\n"), &res) {
		t.Fatal("opencodeGoRunUsage reported a finished step for a start-only stream")
	}
}

func TestOpenCodeGoTriggerDryRun(t *testing.T) {
	p := NewOpenCodeGo(config.ProviderConfig{
		Model:  "opencode-go/glm-5.3-flash",
		Prompt: "ok",
	})
	res, err := p.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("Trigger dry run: %v", err)
	}
	for _, want := range []string{"opencode --standalone run", "--format json", "--model opencode-go/glm-5.3-flash", "--session ses_lp", "limitping-ping", "ok"} {
		if !strings.Contains(res.Command, want) {
			t.Errorf("command %q does not contain %q", res.Command, want)
		}
	}
	if res.Model != "opencode-go/glm-5.3-flash" {
		t.Errorf("model = %q", res.Model)
	}
}
