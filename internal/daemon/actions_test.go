package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"

	"bxw/internal/browser"
)

func TestActionsLiveEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live browser action test in short mode")
	}

	bs := browser.Installed()
	if len(bs) == 0 {
		t.Skip("no supported Chromium browser found on this machine")
	}

	// Run full live smoke on all installed browsers (Edge, Chrome)
	for _, targetBrowser := range bs {
		if targetBrowser.Name != "edge" && targetBrowser.Name != "chrome" {
			continue
		}
		t.Run(targetBrowser.Name, func(t *testing.T) {
			runLiveActionsOnBrowser(t, targetBrowser)
		})
	}
}

func runLiveActionsOnBrowser(t *testing.T, targetBrowser browser.Browser) {
	// Start local test server serving testdata/form.html
	testHTML, err := os.ReadFile(filepath.Join("..", "..", "testdata", "form.html"))
	if err != nil {
		t.Fatalf("read testdata/form.html: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(testHTML)
	}))
	defer ts.Close()

	d := &Daemon{
		br:        targetBrowser,
		stop:      make(chan struct{}),
		since:     time.Now(),
		sessions:  make(map[string]*tabSession),
		profileOf: make(map[cdp.BrowserContextID]string),
	}

	// 1. Launch headless agent browser pointed at test server
	if err := d.launch(true, ts.URL, false); err != nil {
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

	// 2. Find the tab
	var tabID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tabs, err := d.listTabs()
		if err == nil && len(tabs) > 0 {
			for _, tab := range tabs {
				if strings.HasPrefix(tab.URL, ts.URL) {
					tabID = tab.ID
					break
				}
			}
			if tabID == "" && tabs[0].URL != "about:blank" {
				tabID = tabs[0].ID
			}
			if tabID == "" && len(tabs) > 0 {
				tabID = tabs[0].ID
			}
		}
		if tabID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tabID == "" {
		t.Fatal("expected at least 1 tab open")
	}

	// Wait for test server page to load completely before taking initial snapshot
	if _, err := d.wait(tabID, waitCond{Selector: "input#username"}, 10*time.Second); err != nil {
		// If initial launch stayed on about:blank or was delayed, navigate explicitly
		if _, navErr := d.gotoURL(tabID, ts.URL, 10*time.Second); navErr != nil {
			t.Fatalf("goto %s: %v", ts.URL, navErr)
		}
		if _, err := d.wait(tabID, waitCond{Selector: "input#username"}, 10*time.Second); err != nil {
			t.Fatalf("wait for input#username on %s: %v", tabID, err)
		}
	}

	// 3. Take snapshot
	snap, err := d.snapshot(tabID, true, "", "", 15*time.Second)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	var inputRef, buttonRef string
	for _, n := range snap.Nodes {
		if n.Role == "textbox" && (strings.Contains(n.Name, "Username") || inputRef == "") {
			inputRef = n.Ref
		}
		if n.Role == "button" && strings.Contains(n.Name, "Submit") {
			buttonRef = n.Ref
		}
	}

	if inputRef == "" {
		t.Fatalf("failed to find textbox in snapshot:\n%s", snap.Text())
	}
	if buttonRef == "" {
		t.Fatalf("failed to find submit button in snapshot:\n%s", snap.Text())
	}

	// 4. Fill textbox with "Alice"
	_, err = d.fill(tabID, [][2]string{{inputRef, "Alice"}}, 10*time.Second)
	if err != nil {
		t.Fatalf("fill %s: %v", inputRef, err)
	}

	// 5. Click submit button
	_, err = d.click(tabID, buttonRef, false, 10*time.Second)
	if err != nil {
		t.Fatalf("click %s: %v", buttonRef, err)
	}

	// 6. Verify result div in DOM
	sess, err := d.tabSession(tabID)
	if err != nil {
		t.Fatalf("tabSession: %v", err)
	}

	var resultText string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = sess.eval(ctx, `document.getElementById('result').textContent`, &resultText)
	if err != nil {
		t.Fatalf("eval result: %v", err)
	}

	if resultText != "Submitted: Alice" {
		t.Fatalf("expected 'Submitted: Alice', got %q", resultText)
	}
	t.Logf("Success! Live browser action verified. Result: %s", resultText)

	// Test L4: fill on date/time picker input
	_, err = d.fill(tabID, [][2]string{{"#time-input", "14:30"}}, 10*time.Second)
	if err != nil {
		t.Fatalf("fill #time-input: %v", err)
	}
	var timeVal string
	if err := sess.eval(ctx, `document.getElementById('time-input').value`, &timeVal); err != nil {
		t.Fatalf("eval time value: %v", err)
	}
	if timeVal != "14:30" {
		t.Fatalf("expected time value '14:30', got %q", timeVal)
	}
	t.Logf("Time input verified: %s", timeVal)

	// 7. Test generic CDP dispatch (W1)
	resp := d.dispatch(Request{
		Op: "cdp",
		Args: map[string]any{
			"tab":    tabID,
			"method": "Runtime.evaluate",
			"params": `{"expression":"1+1","returnByValue":true}`,
		},
	})
	if resp.Error != "" {
		t.Fatalf("dispatch cdp error: %s", resp.Error)
	}
	var evalOut struct {
		Result struct {
			Type  string `json:"type"`
			Value int    `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Data, &evalOut); err != nil {
		t.Fatalf("unmarshal cdp response %s: %v", string(resp.Data), err)
	}
	if evalOut.Result.Value != 2 {
		t.Fatalf("expected cdp eval result 2, got %v", evalOut.Result.Value)
	}
	t.Logf("CDP dispatch verified: %s", string(resp.Data))

	// Test W2 goto dispatch
	gotoResp := d.dispatch(Request{
		Op: "goto",
		Args: map[string]any{
			"tab": tabID,
			"url": ts.URL + "/late",
		},
	})
	if gotoResp.Error != "" {
		t.Fatalf("dispatch goto error: %s", gotoResp.Error)
	}
	var gotoRes ActResult
	if err := json.Unmarshal(gotoResp.Data, &gotoRes); err != nil {
		t.Fatalf("unmarshal goto result %s: %v", string(gotoResp.Data), err)
	}
	t.Logf("goto summary: %s", gotoRes.TerseSummary("goto"))

	// Test W3 wait dispatch (inject delayed element and wait for selector)
	_ = d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": `setTimeout(()=>{var el=document.createElement('div');el.id='delayed-el';el.innerText='Hello Late';document.body.appendChild(el);}, 250)`,
		},
	})

	waitResp := d.dispatch(Request{
		Op: "wait",
		Args: map[string]any{
			"tab":      tabID,
			"selector": "#delayed-el",
			"timeout":  "5s",
		},
	})
	if waitResp.Error != "" {
		t.Fatalf("dispatch wait error: %s", waitResp.Error)
	}
	var waitRes map[string]any
	if err := json.Unmarshal(waitResp.Data, &waitRes); err != nil {
		t.Fatalf("unmarshal wait result %s: %v", string(waitResp.Data), err)
	}
	if sat, ok := waitRes["satisfied"].(bool); !ok || !sat {
		t.Fatalf("expected wait satisfied=true, got %v", waitRes)
	}
	t.Logf("wait verified: %+v", waitRes)

	// Test W4 read dispatch
	readResp := d.dispatch(Request{
		Op: "read",
		Args: map[string]any{
			"tab": tabID,
		},
	})
	if readResp.Error != "" {
		t.Fatalf("dispatch read error: %s", readResp.Error)
	}
	var readRes struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(readResp.Data, &readRes); err != nil {
		t.Fatalf("unmarshal read result %s: %v", string(readResp.Data), err)
	}
	if !strings.Contains(readRes.Text, "Hello Late") {
		t.Fatalf("expected read text to contain 'Hello Late', got: %q", readRes.Text)
	}
	t.Logf("read verified: %q", readRes.Text)

	// Test W5 hover dispatch
	hoverResp := d.dispatch(Request{
		Op: "hover",
		Args: map[string]any{
			"tab":      tabID,
			"selector": "#submit",
		},
	})
	if hoverResp.Error != "" {
		t.Fatalf("dispatch hover error: %s", hoverResp.Error)
	}
	var hoverRes ActResult
	if err := json.Unmarshal(hoverResp.Data, &hoverRes); err != nil {
		t.Fatalf("unmarshal hover result %s: %v", string(hoverResp.Data), err)
	}
	t.Logf("hover verified: %s", hoverRes.TerseSummary("hover #submit"))

	// Test W6 drag dispatch
	dragResp := d.dispatch(Request{
		Op: "drag",
		Args: map[string]any{
			"tab":  tabID,
			"from": "#username",
			"to":   "#submit",
		},
	})
	if dragResp.Error != "" {
		t.Fatalf("dispatch drag error: %s", dragResp.Error)
	}
	var dragRes ActResult
	if err := json.Unmarshal(dragResp.Data, &dragRes); err != nil {
		t.Fatalf("unmarshal drag result %s: %v", string(dragResp.Data), err)
	}
	t.Logf("drag verified: %s", dragRes.TerseSummary("drag #username -> #submit"))

	// Test W7 upload dispatch
	// Inject a file input
	_ = d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": `(function(){ var fi=document.createElement('input'); fi.type='file'; fi.id='file-input'; document.body.appendChild(fi); })()`,
		},
	})
	// Create a temp file to upload
	tmpUpload, err := os.CreateTemp("", "bxw_upload_test_*.txt")
	if err != nil {
		t.Fatalf("create temp upload file: %v", err)
	}
	tmpUploadPath := tmpUpload.Name()
	_ = tmpUpload.Close()
	defer os.Remove(tmpUploadPath)

	uploadResp := d.dispatch(Request{
		Op: "upload",
		Args: map[string]any{
			"tab":      tabID,
			"selector": "#file-input",
			"paths":    []any{tmpUploadPath},
		},
	})
	if uploadResp.Error != "" {
		t.Fatalf("dispatch upload error: %s", uploadResp.Error)
	}
	var uploadRes ActResult
	if err := json.Unmarshal(uploadResp.Data, &uploadRes); err != nil {
		t.Fatalf("unmarshal upload result %s: %v", string(uploadResp.Data), err)
	}
	t.Logf("upload verified: %s", uploadRes.TerseSummary("upload #file-input"))

	// Verify file was set on input
	var fileName string
	fileEvalResp := d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": "document.getElementById('file-input').files[0].name",
		},
	})
	_ = json.Unmarshal(fileEvalResp.Data, &fileName)
	if fileName != filepath.Base(tmpUploadPath) {
		t.Fatalf("expected uploaded file name %q, got %q", filepath.Base(tmpUploadPath), fileName)
	}

	// Test W8 dialog dispatch
	// Trigger confirm() asynchronously via setTimeout
	_ = d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": `setTimeout(() => { window._confirmResult = confirm('Proceed?'); }, 50)`,
		},
	})
	time.Sleep(200 * time.Millisecond)

	// Dispatch dialog accept
	dialogResp := d.dispatch(Request{
		Op: "dialog",
		Args: map[string]any{
			"tab":    tabID,
			"action": "accept",
		},
	})
	if dialogResp.Error != "" {
		t.Fatalf("dispatch dialog error: %s", dialogResp.Error)
	}
	t.Logf("dialog accept verified: %s", string(dialogResp.Data))

	// Verify confirm returned true
	var confirmVal bool
	confirmEval := d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": "window._confirmResult",
		},
	})
	_ = json.Unmarshal(confirmEval.Data, &confirmVal)
	if !confirmVal {
		t.Fatalf("expected confirm result true, got %v", confirmVal)
	}

	// Test W9 close_tab dispatch
	// Open a second tab first
	openResp := d.dispatch(Request{
		Op: "open",
		Args: map[string]any{
			"url": ts.URL + "/second",
		},
	})
	if openResp.Error != "" {
		t.Fatalf("dispatch open second tab error: %s", openResp.Error)
	}
	var secondTab Tab
	if err := json.Unmarshal(openResp.Data, &secondTab); err != nil {
		t.Fatalf("unmarshal second tab: %v", err)
	}

	// Close the second tab
	closeResp := d.dispatch(Request{
		Op: "close_tab",
		Args: map[string]any{
			"tab": secondTab.ID,
		},
	})
	if closeResp.Error != "" {
		t.Fatalf("dispatch close_tab error: %s", closeResp.Error)
	}
	t.Logf("close_tab verified: %s", string(closeResp.Data))

	// Test W10 console dispatch
	_ = d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": `console.error("test error message from page")`,
		},
	})
	time.Sleep(150 * time.Millisecond)

	consoleResp := d.dispatch(Request{
		Op: "console",
		Args: map[string]any{
			"tab": tabID,
		},
	})
	if consoleResp.Error != "" {
		t.Fatalf("dispatch console error: %s", consoleResp.Error)
	}
	var consoleLogs []map[string]any
	if err := json.Unmarshal(consoleResp.Data, &consoleLogs); err != nil {
		t.Fatalf("unmarshal console logs: %v", err)
	}
	foundError := false
	for _, l := range consoleLogs {
		if txt, ok := l["text"].(string); ok && strings.Contains(txt, "test error message") {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Fatalf("expected to find test error message in console logs, got: %+v", consoleLogs)
	}
	t.Logf("console verified: found error log in %d entries", len(consoleLogs))

	// Test W11 network dispatch
	_ = d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": `fetch(window.location.href).then(r => r.text())`,
		},
	})
	time.Sleep(200 * time.Millisecond)

	netResp := d.dispatch(Request{
		Op: "network",
		Args: map[string]any{
			"tab": tabID,
		},
	})
	if netResp.Error != "" {
		t.Fatalf("dispatch network error: %s", netResp.Error)
	}
	var netRequests []map[string]any
	if err := json.Unmarshal(netResp.Data, &netRequests); err != nil {
		t.Fatalf("unmarshal network list: %v", err)
	}
	if len(netRequests) == 0 {
		t.Fatal("expected at least one captured network request")
	}
	t.Logf("network verified: captured %d requests", len(netRequests))

	// Test W12 storage dispatch
	_ = d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": `localStorage.setItem('bxw_test_key', 'bxw_test_val'); document.cookie = 'bxw_test_cookie=abc; path=/';`,
		},
	})
	storageResp := d.dispatch(Request{
		Op: "storage",
		Args: map[string]any{
			"tab": tabID,
		},
	})
	if storageResp.Error != "" {
		t.Fatalf("dispatch storage error: %s", storageResp.Error)
	}
	var storageData struct {
		Cookies []map[string]any `json:"cookies"`
		Local   map[string]any   `json:"local"`
		Session map[string]any   `json:"session"`
	}
	if err := json.Unmarshal(storageResp.Data, &storageData); err != nil {
		t.Fatalf("unmarshal storage: %v", err)
	}
	if storageData.Local["bxw_test_key"] != "bxw_test_val" {
		t.Fatalf("expected localStorage['bxw_test_key'] == 'bxw_test_val', got: %v", storageData.Local)
	}
	t.Logf("storage verified: local keys=%d, cookies=%d", len(storageData.Local), len(storageData.Cookies))

	// Test W13 performance dispatch
	perfResp := d.dispatch(Request{
		Op: "performance",
		Args: map[string]any{
			"tab": tabID,
		},
	})
	if perfResp.Error != "" {
		t.Fatalf("dispatch performance error: %s", perfResp.Error)
	}
	var perfData map[string]any
	if err := json.Unmarshal(perfResp.Data, &perfData); err != nil {
		t.Fatalf("unmarshal performance data: %v", err)
	}
	if _, ok := perfData["dom_size"]; !ok {
		t.Fatalf("expected dom_size in performance data, got: %+v", perfData)
	}
	t.Logf("performance verified: dom_size=%v, ttfb=%v", perfData["dom_size"], perfData["ttfb"])

	// Test W14 emulate dispatch
	emulateResp := d.dispatch(Request{
		Op: "emulate",
		Args: map[string]any{
			"tab":    tabID,
			"device": "mobile",
			"media":  "dark",
		},
	})
	if emulateResp.Error != "" {
		t.Fatalf("dispatch emulate error: %s", emulateResp.Error)
	}
	// Check window.outerWidth or screen.width is 390
	var outerWidth float64
	wResp := d.dispatch(Request{
		Op: "eval",
		Args: map[string]any{
			"tab":    tabID,
			"script": "window.outerWidth",
		},
	})
	if wResp.Error != "" {
		t.Fatalf("eval outerWidth error: %s", wResp.Error)
	}
	_ = json.Unmarshal(wResp.Data, &outerWidth)
	if outerWidth != 390 {
		t.Fatalf("expected outerWidth 390 after mobile emulation, got: %v", outerWidth)
	}
	// Reset emulation
	resetResp := d.dispatch(Request{
		Op: "emulate",
		Args: map[string]any{
			"tab":    tabID,
			"device": "reset",
			"media":  "reset",
		},
	})
	if resetResp.Error != "" {
		t.Fatalf("dispatch reset emulate error: %s", resetResp.Error)
	}
	t.Logf("emulate verified: mobile innerWidth=390, reset success")

	// Test W16 follow mode dispatch
	gotoFollowResp := d.dispatch(Request{
		Op: "goto",
		Args: map[string]any{
			"tab":    tabID,
			"url":    ts.URL + "/form",
			"follow": true,
		},
	})
	if gotoFollowResp.Error != "" {
		t.Fatalf("dispatch goto with follow error: %s", gotoFollowResp.Error)
	}
	t.Logf("follow verified: goto with follow=true succeeded")

	// Test L6: close_tab on last tab refuses
	lastCloseResp := d.dispatch(Request{
		Op: "close_tab",
		Args: map[string]any{
			"tab": tabID,
		},
	})
	if lastCloseResp.Error == "" || !strings.Contains(lastCloseResp.Error, "refusing to close the last tab") {
		t.Fatalf("expected last-tab refusal error, got: %s (data: %s)", lastCloseResp.Error, string(lastCloseResp.Data))
	}
	t.Logf("last-tab refusal verified: %s", lastCloseResp.Error)
}
