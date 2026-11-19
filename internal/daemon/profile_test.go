package daemon

import (
	"testing"

	"github.com/chromedp/cdproto/cdp"

	"bxw/internal/browser"
)

func TestCheckURL(t *testing.T) {
	valid := []string{
		"http://example.com",
		"https://example.com/path?arg=1",
		"about:blank",
		"data:text/html,<h1>hi</h1>",
		"file:///C:/path/to/file.html",
	}
	for _, u := range valid {
		if err := browser.CheckURL(u); err != nil {
			t.Errorf("CheckURL(%q) unexpected error: %v", u, err)
		}
	}

	invalid := []string{
		"--flag",
		"-switch",
		"ftp://example.com",
		"javascript:alert(1)",
		"",
	}
	for _, u := range invalid {
		if err := browser.CheckURL(u); err == nil {
			t.Errorf("CheckURL(%q) expected error, got nil", u)
		}
	}
}

func TestProbeURL(t *testing.T) {
	nonce := "1234abcd"
	u := browser.ProbeURL(nonce)
	if err := browser.CheckURL(u); err != nil {
		t.Fatalf("ProbeURL generated invalid URL %q: %v", u, err)
	}
	if want := browser.ProbeMarker + nonce; !contains(u, want) {
		t.Errorf("ProbeURL %q missing %q", u, want)
	}
}

func TestFindProfile(t *testing.T) {
	ps := []browser.Profile{
		{Dir: "Default", Name: "Personal"},
		{Dir: "Profile 1", Name: "Work"},
		{Dir: "Profile 2", Name: "Testing"},
	}

	// Match by name
	p, err := browser.FindProfile(ps, "work")
	if err != nil || p.Dir != "Profile 1" {
		t.Errorf("expected Profile 1 for 'work', got %+v, err: %v", p, err)
	}

	// Match by directory
	p, err = browser.FindProfile(ps, "Default")
	if err != nil || p.Name != "Personal" {
		t.Errorf("expected Personal for 'Default', got %+v, err: %v", p, err)
	}

	// Missing profile
	_, err = browser.FindProfile(ps, "NonExistent")
	if err == nil {
		t.Error("expected error for NonExistent profile, got nil")
	}
}

func TestLearnContext(t *testing.T) {
	d := &Daemon{
		profileOf: make(map[cdp.BrowserContextID]string),
	}

	d.learn("ctx-1", "Default")
	if d.profileOf["ctx-1"] != "Default" {
		t.Errorf("expected Default, got %s", d.profileOf["ctx-1"])
	}

	// Relearning "Default" on a new context should evict the old context
	d.learn("ctx-2", "Default")
	if d.profileOf["ctx-2"] != "Default" {
		t.Errorf("expected Default on ctx-2, got %s", d.profileOf["ctx-2"])
	}
	if _, exists := d.profileOf["ctx-1"]; exists {
		t.Errorf("expected ctx-1 to be evicted when Default re-associated with ctx-2")
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
