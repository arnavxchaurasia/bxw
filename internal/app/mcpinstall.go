package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"bxw/internal/exit"
)

type clientTarget struct {
	Name   string
	Dir    string
	Config string
}

func clientTargets() []clientTarget {
	appData := os.Getenv("APPDATA")
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile, _ = os.UserHomeDir()
	}

	return []clientTarget{
		{
			Name:   "Claude Desktop",
			Dir:    filepath.Join(appData, "Claude"),
			Config: filepath.Join(appData, "Claude", "claude_desktop_config.json"),
		},
		{
			Name:   "Cursor",
			Dir:    filepath.Join(userProfile, ".cursor"),
			Config: filepath.Join(userProfile, ".cursor", "mcp.json"),
		},
		{
			Name:   "Antigravity",
			Dir:    filepath.Join(userProfile, ".gemini", "antigravity"),
			Config: filepath.Join(userProfile, ".gemini", "antigravity", "mcp_config.json"),
		},
	}
}

func runMCPInstall(args []string) error {
	self, err := os.Executable()
	if err != nil {
		return exit.New(exit.Internal, "locate bxw executable: %v", err)
	}
	self, _ = filepath.Abs(self)

	entry := map[string]any{
		"command": self,
		"args":    []string{"mcp"},
	}

	targets := clientTargets()
	var installed []string

	for _, t := range targets {
		// If specific client was passed, only target that one
		if len(args) > 0 && !stringsEqual(args[0], t.Name) {
			continue
		}

		// Only install into existing client directories unless specifically targeted
		if len(args) == 0 {
			if _, err := os.Stat(t.Dir); os.IsNotExist(err) {
				continue
			}
		}

		if err := addServerToConfig(t.Config, "bxw", entry); err != nil {
			fmt.Printf("warning: failed to configure %s at %s: %v\n", t.Name, t.Config, err)
			continue
		}
		installed = append(installed, fmt.Sprintf("%s (%s)", t.Name, t.Config))
	}

	if len(installed) == 0 {
		fmt.Println("No supported MCP client configurations found (Claude Desktop, Cursor, Antigravity).")
		fmt.Printf("To configure manually, add this server to your client's MCP settings:\n\n")
		manual, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"bxw": entry}}, "", "  ")
		fmt.Println(string(manual))
		return nil
	}

	fmt.Println("Successfully installed bxw MCP server to:")
	for _, inst := range installed {
		fmt.Printf("  ✓ %s\n", inst)
	}
	fmt.Println("\nRestart your AI client to load the bxw tools.")
	return nil
}

func stringsEqual(a, b string) bool {
	return bytes.EqualFold([]byte(a), []byte(b))
}

func addServerToConfig(configPath, serverName string, serverDef map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}

	cfg := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil && len(bytes.TrimSpace(data)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&cfg); err != nil {
			return fmt.Errorf("parse existing config: %w", err)
		}
	}

	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		servers = make(map[string]any)
		cfg["mcpServers"] = servers
	}
	servers[serverName] = serverDef

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}
