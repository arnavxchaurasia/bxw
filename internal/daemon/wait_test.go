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

	"bxw/internal/browser"
)

// TestR1SmartWaitConditions verifies smart wait predicates:
// --selector (appears), --gone (disappears), --stable (DOM quiet),
// --network-idle, and timeout error naming the unmet condition.
func TestR1SmartWaitConditions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live browser smart wait test in short mode")
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

	// 1. Setup a test page fixture with dynamic DOM updates
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>R1 Smart Wait Test</title></head>
<body>
  <h1>Smart Wait Test Page</h1>
  <div id="target-gone">Disappearing element</div>
  <button id="action-btn" disabled style="display:none">Hidden Disabled Button</button>
  <script>
    // Delay element creation
    setTimeout(() => {
      const el = document.createElement('div');
      el.id = 'delayed-box';
      el.innerText = 'Delayed Content Ready';
      document.body.appendChild(el);
    }, 200);

    // Delay element removal
    setTimeout(() => {
      const g = document.getElementById('target-gone');
      if (g) g.remove();
    }, 300);

    // Make button actionable after 250ms
    setTimeout(() => {
      const btn = document.getElementById('action-btn');
      btn.style.display = 'block';
      btn.disabled = false;
      btn.innerText = 'Click Me Now';
    }, 250);
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

	// Launch headless agent browser pointed at test server
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

	// Find the tab
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
		t.Fatalf("timed out waiting for tab to appear in %s", targetBrowser.Name)
	}

	// 2. Test --selector waiting for delayed element
	res, err := d.wait(tabID, waitCond{Selector: "#delayed-box"}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait for #delayed-box failed: %v", err)
	}
	if sat, ok := res["satisfied"].(bool); !ok || !sat {
		t.Fatalf("expected satisfied=true, got %v", res)
	}

	// 3. Test --text waiting for text
	res, err = d.wait(tabID, waitCond{Text: "Delayed Content Ready"}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait for text failed: %v", err)
	}
	if sat, ok := res["satisfied"].(bool); !ok || !sat {
		t.Fatalf("expected satisfied=true for text, got %v", res)
	}

	// 4. Test --gone waiting for element removal
	res, err = d.wait(tabID, waitCond{Gone: "#target-gone"}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait for gone #target-gone failed: %v", err)
	}
	if sat, ok := res["satisfied"].(bool); !ok || !sat {
		t.Fatalf("expected satisfied=true for gone, got %v", res)
	}

	// 5. Test --stable DOM stability
	res, err = d.wait(tabID, waitCond{Stable: true}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait for stable failed: %v", err)
	}
	if sat, ok := res["satisfied"].(bool); !ok || !sat {
		t.Fatalf("expected satisfied=true for stable, got %v", res)
	}

	// 6. Test --network-idle
	res, err = d.wait(tabID, waitCond{NetworkIdle: true}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait for network-idle failed: %v", err)
	}
	if sat, ok := res["satisfied"].(bool); !ok || !sat {
		t.Fatalf("expected satisfied=true for network-idle, got %v", res)
	}

	// 7. Test timeout returning error naming the unmet condition
	_, err = d.wait(tabID, waitCond{Selector: "#nonexistent-element"}, 200*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout error for nonexistent selector, got nil")
	}
	if !strings.Contains(err.Error(), "timed out after") || !strings.Contains(err.Error(), `selector "#nonexistent-element"`) {
		t.Fatalf("expected error naming condition, got: %v", err)
	}

	_, err = d.wait(tabID, waitCond{Gone: "h1"}, 200*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout error for gone h1, got nil")
	}
	if !strings.Contains(err.Error(), `removal of "h1"`) {
		t.Fatalf("expected error naming condition 'removal of \"h1\"', got: %v", err)
	}

	// 8. Test auto-wait on click
	actRes, err := d.click(tabID, "#action-btn", false, true, 5*time.Second)
	if err != nil {
		t.Fatalf("click with auto-wait failed: %v", err)
	}
	t.Logf("click with auto-wait succeeded: %+v", actRes)

	// Verify button was indeed clicked
	sess, err := d.tabSession(tabID)
	if err != nil {
		t.Fatalf("tabSession: %v", err)
	}
	var btnText string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sess.eval(ctx, `document.getElementById('action-btn').innerText`, &btnText); err != nil {
		t.Fatalf("eval button text: %v", err)
	}
	if btnText != "Click Me Now" {
		t.Fatalf("expected button text 'Click Me Now', got %q", btnText)
	}
}
