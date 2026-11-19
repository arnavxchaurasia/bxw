package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSetDebuggingAndPreserveKeys(t *testing.T) {
	tmpDir := t.TempDir()

	initialState := map[string]any{
		"browser": map[string]any{
			"custom_int":   int64(9876543210),
			"custom_str":   "preserve_me",
			"custom_array": []any{"a", "b", "c"},
		},
		"profile": map[string]any{
			"info_cache": map[string]any{
				"Default": map[string]any{
					"name": "Personal",
				},
			},
		},
	}

	initialBytes, err := json.MarshalIndent(initialState, "", "  ")
	if err != nil {
		t.Fatalf("marshal initial state: %v", err)
	}

	localStatePath := filepath.Join(tmpDir, "Local State")
	if err := os.WriteFile(localStatePath, initialBytes, 0o644); err != nil {
		t.Fatalf("write Local State: %v", err)
	}

	// Create test browser pointing to tmpDir
	b := Browser{
		Name:    "test-browser",
		Label:   "Test Browser",
		DataDir: tmpDir,
		Exe:     "nonexistent.exe", // Not running
	}

	// 1. Initial debugging state should be false
	enabled, err := b.DebuggingEnabled()
	if err != nil {
		t.Fatalf("DebuggingEnabled: %v", err)
	}
	if enabled {
		t.Errorf("expected debugging to be disabled initially")
	}

	// 2. Enable debugging
	if err := b.SetDebugging(true); err != nil {
		t.Fatalf("SetDebugging(true): %v", err)
	}

	// 3. Verify enabled
	enabled, err = b.DebuggingEnabled()
	if err != nil || !enabled {
		t.Errorf("expected debugging to be enabled after SetDebugging(true), got %v, err: %v", enabled, err)
	}

	// 4. Verify backup was created
	bakPath := localStatePath + ".bxw.bak"
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		t.Errorf("expected backup file %s to exist", bakPath)
	}

	// 5. Verify existing keys were preserved
	savedData, err := os.ReadFile(localStatePath)
	if err != nil {
		t.Fatalf("read saved state: %v", err)
	}
	savedState, err := decodeState(savedData)
	if err != nil {
		t.Fatalf("decode saved state: %v", err)
	}

	browserMap, ok := savedState["browser"].(map[string]any)
	if !ok || browserMap["custom_str"] != "preserve_me" {
		t.Errorf("custom_str not preserved in saved state: %+v", savedState)
	}

	// 6. Revert debugging
	if err := b.SetDebugging(false); err != nil {
		t.Fatalf("SetDebugging(false): %v", err)
	}
	enabled, err = b.DebuggingEnabled()
	if err != nil || enabled {
		t.Errorf("expected debugging to be disabled after SetDebugging(false), got %v", enabled)
	}
}
