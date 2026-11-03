package cli

import (
	"strings"
	"testing"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/scheduler"
)

func TestDefaultConfigSelectsBothProviders(t *testing.T) {
	cfg := config.Default()

	providers := enabledProviders(cfg)
	if got, want := providerNames(providers), []string{"claude", "codex"}; !sameStrings(got, want) {
		t.Fatalf("enabled providers = %#v, want %#v", got, want)
	}

	targets, err := buildTargets(cfg)
	if err != nil {
		t.Fatalf("buildTargets: %v", err)
	}
	if got, want := targetNames(targets), []string{"claude", "codex"}; !sameStrings(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestDisabledProviderIsStillSelectableByName(t *testing.T) {
	cfg := config.Default()
	cfg.Codex.Enabled = false

	providers, err := selectProviders(cfg, "codex")
	if err != nil {
		t.Fatalf("selectProviders: %v", err)
	}
	if got, want := providerNames(providers), []string{"codex"}; !sameStrings(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}

	targets, err := selectTargets(cfg, "codex")
	if err != nil {
		t.Fatalf("selectTargets: %v", err)
	}
	if got, want := targetNames(targets), []string{"codex"}; !sameStrings(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

// A name limitping does not serve — a typo, or a provider that has since been
// retired — must be rejected with the list of valid ones, not resolved into a
// silently empty selection.
func TestUnknownProviderIsRejected(t *testing.T) {
	cfg := config.Default()

	if _, err := selectProviders(cfg, "nonesuch"); err == nil {
		t.Fatal("selectProviders succeeded on an unknown provider, want an error")
	} else if !strings.Contains(err.Error(), "claude, codex, opencode-go, or all") {
		t.Fatalf("selectProviders error = %v, want the valid provider list", err)
	}
	if _, err := selectTargets(cfg, "nonesuch"); err == nil {
		t.Fatal("selectTargets succeeded on an unknown provider, want an error")
	}
}

func TestOpenCodeGoIsDisabledByDefaultButSelectable(t *testing.T) {
	cfg := config.Default()
	if cfg.OpenCodeGo.Enabled {
		t.Fatal("opencode_go enabled in defaults")
	}
	providers, err := selectProviders(cfg, "opencode-go")
	if err != nil {
		t.Fatalf("selectProviders(opencode-go): %v", err)
	}
	if got, want := providerNames(providers), []string{"opencode-go"}; !sameStrings(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	targets, err := selectTargets(cfg, "opencode-go")
	if err != nil {
		t.Fatalf("selectTargets(opencode-go): %v", err)
	}
	if got, want := targetNames(targets), []string{"opencode-go"}; !sameStrings(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func providerNames(ps []provider.Provider) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name()
	}
	return names
}

func targetNames(targets []scheduler.Target) []string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Provider.Name()
	}
	return names
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
