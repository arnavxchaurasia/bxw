package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/coder/websocket"

	"bxw/internal/browser"
	"bxw/internal/exit"
)

// TestR2SelfHealReconnect simulates a dropped TabConn (ws connection closed mid-session)
// and asserts that supervised reconnect restores a working session.
// Also asserts that permanent failure after retries returns typed exit.Upstream.
func TestR2SelfHealReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live browser reconnect test in short mode")
	}

	bs := browser.Installed()
	if len(bs) == 0 {
		t.Skip("no supported Chromium browser found on this machine")
	}

	targetBrowser := bs[0]
	for _, b := range bs {
		if b.Name == "edge" || b.Name == "chrome" {
			targetBrowser = b
			break
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>R2 Reconnect Test</title></head>
<body>
  <h1>Self Heal Reconnect Page</h1>
  <div id="counter">0</div>
  <script>
    window.incrementCounter = () => {
      const el = document.getElementById('counter');
      el.innerText = String(Number(el.innerText) + 1);
      return Number(el.innerText);
    };
  </script>
</body>
</html>`)
	}))
	defer server.Close()

	d := &Daemon{
		br:        targetBrowser,
		stop:      make(chan struct{}),
		since:     time.Now(),
		sessions:  make(map[string]*tabSession),
		profileOf: make(map[cdp.BrowserContextID]string),
	}

	if err := d.launch(true, server.URL, false); err != nil {
		t.Fatalf("launch headless %s: %v", targetBrowser.Name, err)
	}
	defer func() {
		if d.agentPID != 0 {
			if proc, err := os.FindProcess(d.agentPID); err == nil {
				_ = proc.Kill()
				_, _ = proc.Wait()
			}
		}
	}()

	var tabID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tabs, err := d.listTabs()
		if err == nil && len(tabs) > 0 {
			for _, tab := range tabs {
				if strings.HasPrefix(tab.URL, server.URL) {
					tabID = tab.ID
					break
				}
			}
			if tabID == "" && tabs[0].URL != "about:blank" {
				tabID = tabs[0].ID
			}
		}
		if tabID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tabID == "" {
		t.Fatalf("timed out waiting for tab in %s", targetBrowser.Name)
	}

	sess, err := d.tabSession(tabID)
	if err != nil {
		t.Fatalf("tabSession: %v", err)
	}

	// 1. First eval works normally
	var count1 int
	if err := sess.eval(context.Background(), `window.incrementCounter()`, &count1); err != nil {
		t.Fatalf("eval 1 failed: %v", err)
	}
	if count1 != 1 {
		t.Fatalf("expected count 1, got %d", count1)
	}

	// 2. Simulate dropped connection by intentionally closing the underlying websocket mid-session
	sess.mu.Lock()
	originalConn := sess.conn
	sess.mu.Unlock()

	_ = originalConn.ws.Close(websocket.StatusNormalClosure, "simulated mid-session drop")

	// Wait briefly for readLoop to detect close
	time.Sleep(100 * time.Millisecond)

	// Verify original connection is closed
	originalConn.mu.Lock()
	droppedErr := originalConn.closeErr
	originalConn.mu.Unlock()
	if droppedErr == nil {
		t.Logf("note: closeErr was not immediately set, continuing to eval")
	}

	// 3. Perform eval: supervised reconnect should trigger, restore connection, and succeed
	var count2 int
	if err := sess.eval(context.Background(), `window.incrementCounter()`, &count2); err != nil {
		t.Fatalf("eval 2 after dropped connection failed: %v", err)
	}
	if count2 != 2 {
		t.Fatalf("expected count 2 after reconnect, got %d", count2)
	}
	t.Logf("Supervised reconnect succeeded! Evaluated after drop: count=%d", count2)

	// 4. Test permanent failure: test a detached session with invalid daemon port
	failingDaemon := &Daemon{
		br:      browser.Browser{Name: "invalid-browser"},
		cdpPort: 59999, // unreachable port
	}
	failingSess := &tabSession{
		id:    "fake-tab-id",
		d:     failingDaemon,
		conn:  &TabConn{closeErr: fmt.Errorf("underlying network dead")},
		wsURL: "ws://127.0.0.1:59999/devtools/page/fake",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	permErr := failingSess.eval(ctx, `1 + 1`, nil)
	if permErr == nil {
		t.Fatalf("expected permanent failure error on unreachable daemon, got nil")
	}

	// Must be typed exit.Upstream
	var exitErr *exit.Error
	if !errorsAs(permErr, &exitErr) || exitErr.Class != exit.Upstream {
		t.Fatalf("expected typed exit.Upstream error, got: %T %v", permErr, permErr)
	}
	t.Logf("Permanent failure returned typed exit.Upstream: %v", permErr)
}
