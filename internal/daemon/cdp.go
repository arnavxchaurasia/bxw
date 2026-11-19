package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bxw/internal/exit"
	"bxw/internal/osport"
)

// launch starts an isolated or persistent profile browser instance with remote
// debugging. It uses --remote-debugging-port=0 to let Chromium atomically allocate
// and bind a free port, reading the assigned port from DevToolsActivePort to avoid
// port-binding race conditions (Pillar A).
func (d *Daemon) launch(headless bool, targetURL string, realProfile bool) error {
	p := osport.Current()
	if targetURL == "" {
		targetURL = "about:blank"
	}

	var profileDir string
	if realProfile {
		if d.br.Name != "brave" {
			return exit.New(exit.User, "real-profile remote debugging is blocked on %s by Chromium 136; use persistent profile instead (default)", d.br.Label)
		}
		profileDir = d.br.DataDir
	} else {
		profileDir = p.ProfileDir(d.br.Name)
	}
	_ = os.MkdirAll(profileDir, 0o755)

	// Reuse an already-running instance for this profile instead of spawning a
	// colliding one. Chromium's single-instance lock routes a second process
	// with the same --user-data-dir to the existing one and silently ignores the
	// new --remote-debugging-port, so the fresh port would never bind ("CDP did
	// not come up"). If CDP is already answering for this profile, attach to it.
	if port, ok := cdpFromProfile(profileDir); ok {
		d.mu.Lock()
		d.cdpPort = port
		d.mu.Unlock()
		_ = os.WriteFile(portFile(p, d.br.Name), []byte(fmt.Sprint(port)), 0o644)
		return nil
	}

	// Remove stale DevToolsActivePort before spawning so we don't read an old port.
	devToolsPortPath := filepath.Join(profileDir, "DevToolsActivePort")
	_ = os.Remove(devToolsPortPath)

	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	args = append(args, "--new-window", targetURL)

	pid, err := p.SpawnDetached(d.br.Exe, args)
	if err != nil {
		return err
	}

	port, err := pollDevToolsActivePort(devToolsPortPath, 15*time.Second)
	if err != nil {
		return err
	}

	if err := waitCDP(port, 30*time.Second); err != nil {
		return err
	}
	d.mu.Lock()
	d.cdpPort = port
	d.agentPID = pid
	d.mu.Unlock()
	_ = os.WriteFile(portFile(p, d.br.Name), []byte(fmt.Sprint(port)), 0o644)
	return nil
}

// listTabs returns the browser's page targets via the DevTools /json endpoint.
func (d *Daemon) listTabs() ([]Tab, error) {
	return d.listTabsFiltered("")
}

// listTabsFiltered returns page targets, optionally filtered by profile.
func (d *Daemon) listTabsFiltered(profileRef string) ([]Tab, error) {
	d.mu.Lock()
	port := d.cdpPort
	d.mu.Unlock()
	if port == 0 {
		discovered, err := d.discoverPortWithRetry(4, 250*time.Millisecond)
		if err != nil {
			return nil, err
		}
		port = discovered
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).
		Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return nil, fmt.Errorf("CDP not reachable on 127.0.0.1:%d: %w (try: bxw launch %s)", port, err, d.br.Name)
	}
	defer resp.Body.Close()
	var all []Tab
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}

	pNames := d.profileNames()

	// Try getting browser target info for browserContextId mapping
	ctxMap := make(map[string]string) // targetID -> profileDir
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if bconn, err := d.browserConn(ctx); err == nil {
		if btgt, err := d.browserTargets(ctx, bconn); err == nil {
			d.mu.Lock()
			for _, t := range btgt {
				if dir, ok := d.profileOf[t.BrowserContextID]; ok {
					ctxMap[string(t.TargetID)] = dir
				}
			}
			d.mu.Unlock()
		}
		_ = bconn.Close()
	}

	var targetDir string
	if profileRef != "" {
		if p, err := d.resolveProfile(profileRef); err == nil {
			targetDir = p.Dir
		}
	}

	pages := make([]Tab, 0, len(all))
	for _, t := range all {
		if t.Type != "page" || strings.Contains(t.URL, "bxprobe-") || strings.Contains(t.URL, "bxwprobe-") {
			continue
		}
		if dir, ok := ctxMap[t.ID]; ok {
			t.ProfileDir = dir
			t.Profile = pNames[dir]
		}
		if targetDir != "" && t.ProfileDir != "" && t.ProfileDir != targetDir {
			continue
		}
		pages = append(pages, t)
	}
	return pages, nil
}

// discoverPort finds an active CDP port for this browser:
// 1. Checks everyday browser's DevToolsActivePort
// 2. Checks previously launched agent instance's .port file
func (d *Daemon) discoverPort() (int, error) {
	p := osport.Current()

	// 1. Everyday browser DevToolsActivePort
	portFileEveryday := filepath.Join(d.br.DataDir, "DevToolsActivePort")
	if data, err := os.ReadFile(portFileEveryday); err == nil {
		f := strings.Fields(string(data))
		if len(f) >= 1 {
			if port, err := strconv.Atoi(f[0]); err == nil && port > 0 {
				if err := waitCDP(port, time.Second); err == nil {
					d.mu.Lock()
					d.cdpPort = port
					d.mu.Unlock()
					return port, nil
				}
			}
		}
	}

	// 2. Agent instance .port file
	notRunning := fmt.Errorf("no %s is running with remote debugging (run: bxw launch %s or turn on debugging at %s)",
		d.br.Label, d.br.Name, d.br.Inspect)

	data, err := os.ReadFile(portFile(p, d.br.Name))
	if err != nil {
		return 0, notRunning
	}
	var port int
	if _, err := fmt.Sscan(string(data), &port); err != nil || port == 0 {
		return 0, notRunning
	}
	if err := waitCDP(port, time.Second); err != nil {
		_ = os.Remove(portFile(p, d.br.Name))
		return 0, notRunning
	}
	d.mu.Lock()
	d.cdpPort = port
	d.mu.Unlock()
	return port, nil
}

// cdpFromProfile returns the active CDP port for a launched profile if an
// instance is already running. Chromium writes the real debugging port to
// <user-data-dir>/DevToolsActivePort (first line). A stale file from a killed
// instance fails the waitCDP probe and is treated as "not running".
func cdpFromProfile(profileDir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(profileDir, "DevToolsActivePort"))
	if err != nil {
		return 0, false
	}
	line := strings.TrimSpace(string(data))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	port, err := strconv.Atoi(line)
	if err != nil || port == 0 {
		return 0, false
	}
	if waitCDP(port, time.Second) != nil {
		return 0, false
	}
	return port, true
}

// pollDevToolsActivePort polls for DevToolsActivePort to be written by Chromium
// and returns the parsed TCP port.
func pollDevToolsActivePort(filePath string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	delay := 50 * time.Millisecond
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filePath)
		if err == nil && len(data) > 0 {
			line := strings.TrimSpace(string(data))
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			port, err := strconv.Atoi(line)
			if err == nil && port > 0 {
				return port, nil
			}
		}
		time.Sleep(delay)
		if delay < 200*time.Millisecond {
			delay = delay * 3 / 2
		}
	}
	return 0, fmt.Errorf("DevToolsActivePort not created within %v at %s", timeout, filePath)
}

func portFile(p osport.Platform, browser string) string {
	newPath := filepath.Join(p.RunDir(), browser+".port")
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	legacyPath := filepath.Join(p.LocalAppData(), "bxw", browser+"-agent.port")
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}
	return newPath
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitCDP(port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	cli := &http.Client{Timeout: 2 * time.Second}
	delay := 100 * time.Millisecond
	for time.Now().Before(deadline) {
		if r, err := cli.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port)); err == nil {
			r.Body.Close()
			return nil
		}
		time.Sleep(delay)
		if delay < 500*time.Millisecond {
			delay = delay * 3 / 2
		}
	}
	return fmt.Errorf("CDP did not come up on 127.0.0.1:%d", port)
}

// discoverPortWithRetry retries discoverPort with backoff
func (d *Daemon) discoverPortWithRetry(attempts int, initialDelay time.Duration) (int, error) {
	var lastErr error
	delay := initialDelay
	for i := 0; i < attempts; i++ {
		port, err := d.discoverPort()
		if err == nil && port > 0 {
			return port, nil
		}
		lastErr = err
		time.Sleep(delay)
		delay = delay * 2
	}
	return 0, lastErr
}
