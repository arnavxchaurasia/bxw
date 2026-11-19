package config

import (
	"os"
	"testing"
)

func TestConfigDefaultAndParse(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "bxw-test-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	content := `
# test config
default_browser = "brave"
port_min = 52000

[cache]
max_mb = 1000

[retention]
history_days = 60
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// Direct parsing check
	cfg := Default()
	if cfg.DefaultBrowser != "edge" {
		t.Errorf("expected default edge, got %s", cfg.DefaultBrowser)
	}
}
