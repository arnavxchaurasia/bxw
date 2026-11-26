package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	jsonv2 "github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"bxw/internal/contextmgr"
	"bxw/internal/exit"
	"bxw/internal/osport"
)

// TabConn is a WebSocket connection to a DevTools target tab, implementing cdp.Session.
type TabConn struct {
	ws       *websocket.Conn
	seq      atomic.Uint64
	mu       sync.Mutex
	pending  map[uint64]chan cdpResponse
	subsMu   sync.RWMutex
	subs     map[string][]chan jsonv2.Value
	closed   chan struct{}
	closeErr error
}

type cdpRequest struct {
	ID     uint64 `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     uint64       `json:"id,omitempty"`
	Method string       `json:"method,omitempty"`
	Params jsonv2.Value `json:"params,omitempty"`
	Result jsonv2.Value `json:"result,omitempty"`
	Error  *cdpError    `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *cdpError) Error() string {
	return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message)
}

func dialTab(ctx context.Context, wsURL string) (*TabConn, error) {
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, exit.New(exit.Upstream, "dial tab websocket %s: %v", wsURL, err)
	}
	c.SetReadLimit(32 * 1024 * 1024)

	tc := &TabConn{
		ws:      c,
		pending: make(map[uint64]chan cdpResponse),
		subs:    make(map[string][]chan jsonv2.Value),
		closed:  make(chan struct{}),
	}
	go tc.readLoop()
	return tc, nil
}

func (tc *TabConn) readLoop() {
	defer close(tc.closed)
	for {
		var resp cdpResponse
		err := wsjson.Read(context.Background(), tc.ws, &resp)
		if err != nil {
			tc.mu.Lock()
			tc.closeErr = err
			for _, ch := range tc.pending {
				ch <- cdpResponse{Error: &cdpError{Message: err.Error()}}
			}
			tc.pending = nil
			tc.mu.Unlock()
			return
		}
		if resp.ID != 0 {
			tc.mu.Lock()
			if ch, ok := tc.pending[resp.ID]; ok {
				delete(tc.pending, resp.ID)
				ch <- resp
			}
			tc.mu.Unlock()
		} else if resp.Method != "" {
			tc.subsMu.RLock()
			subList := tc.subs[resp.Method]
			for _, ch := range subList {
				select {
				case ch <- resp.Params:
				default:
				}
			}
			tc.subsMu.RUnlock()
		}
	}
}

// Call implements cdp.Session.
func (tc *TabConn) Call(ctx context.Context, method string, params, result any) error {
	id := tc.seq.Add(1)
	ch := make(chan cdpResponse, 1)

	tc.mu.Lock()
	if tc.closeErr != nil {
		tc.mu.Unlock()
		return tc.closeErr
	}
	tc.pending[id] = ch
	tc.mu.Unlock()

	req := cdpRequest{ID: id, Method: method, Params: params}
	if err := wsjson.Write(ctx, tc.ws, req); err != nil {
		tc.mu.Lock()
		delete(tc.pending, id)
		tc.mu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		tc.mu.Lock()
		delete(tc.pending, id)
		tc.mu.Unlock()
		return ctx.Err()
	case <-tc.closed:
		return tc.closeErr
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal([]byte(resp.Result), result)
		}
		return nil
	}
}

// Subscribe implements cdp.Session.
func (tc *TabConn) Subscribe(method string) (events <-chan jsonv2.Value, cancel func()) {
	ch := make(chan jsonv2.Value, 64)
	tc.subsMu.Lock()
	tc.subs[method] = append(tc.subs[method], ch)
	tc.subsMu.Unlock()

	cancel = func() {
		tc.subsMu.Lock()
		defer tc.subsMu.Unlock()
		list := tc.subs[method]
		for i, c := range list {
			if c == ch {
				tc.subs[method] = append(list[:i], list[i+1:]...)
				break
			}
		}
	}
	return ch, cancel
}

// Close closes the tab connection.
func (tc *TabConn) Close() error {
	return tc.ws.Close(websocket.StatusNormalClosure, "done")
}

// Dialog represents an alert, confirm, prompt or beforeunload dialog.
type Dialog struct {
	Type          string `json:"type"`
	Message       string `json:"message"`
	DefaultPrompt string `json:"default_prompt,omitempty"`
}

var errDialog = errors.New("a dialog opened")

// tabSession manages the connection and ref state for a single open tab.
type tabSession struct {
	id        string
	wsURL     string
	conn      *TabConn
	mu        sync.Mutex
	dmu       sync.Mutex
	dialog    *Dialog
	interrupt context.CancelCauseFunc

	refs      map[string]cdp.BackendNodeID
	refDescs  map[string]RefDescriptor
	refLoader cdp.LoaderID

	consoleBuf *ringBuffer
	netBuf     *ringBuffer

	// R1: Track in-flight network requests for network-idle condition
	netMu        sync.Mutex
	inFlightReqs map[network.RequestID]time.Time

	// R2: Supervised reconnect support
	d *Daemon
}

func (sess *tabSession) watchDialogs(ctx context.Context) {
	opened := cdp.Events(ctx, sess.conn, page.JavascriptDialogOpening)
	closed := cdp.Events(ctx, sess.conn, page.JavascriptDialogClosed)

	go func() {
		for ev, err := range opened {
			if err != nil {
				return
			}
			sess.dmu.Lock()
			sess.dialog = &Dialog{
				Type:          string(ev.Type),
				Message:       ev.Message,
				DefaultPrompt: ev.DefaultPrompt,
			}
			if sess.interrupt != nil {
				sess.interrupt(errDialog)
			}
			sess.dmu.Unlock()
		}
	}()

	go func() {
		for _, err := range closed {
			if err != nil {
				return
			}
			sess.dmu.Lock()
			sess.dialog = nil
			sess.dmu.Unlock()
		}
	}()
}

func (sess *tabSession) pendingDialog() *Dialog {
	sess.dmu.Lock()
	defer sess.dmu.Unlock()
	return sess.dialog
}

func (sess *tabSession) errIfDialog() error {
	if d := sess.pendingDialog(); d != nil {
		return exit.New(exit.User, "the page is showing a dialog (%s: %q): run bxw dialog %s accept|dismiss", d.Type, d.Message, sess.id)
	}
	return nil
}

func (sess *tabSession) loaderID(ctx context.Context) cdp.LoaderID {
	tree, err := cdp.Call(ctx, sess.conn, page.GetFrameTree, cdp.Empty{})
	if err != nil || tree.FrameTree == nil || tree.FrameTree.Frame == nil {
		return ""
	}
	return tree.FrameTree.Frame.LoaderID
}

func (sess *tabSession) location(ctx context.Context) (string, string) {
	var loc []string
	if err := sess.eval(ctx, `[location.href, document.title]`, &loc); err != nil || len(loc) != 2 {
		return "", ""
	}
	return loc[0], loc[1]
}

// logReconnect logs a reconnect attempt or outcome to diagnostics/logs.
func logReconnect(browserName, tabID, msg string) {
	log.Printf("[reconnect] browser=%s tab=%s: %s", browserName, tabID, msg)
	p := osport.Current()
	logsDir := p.LogsDir()
	if err := os.MkdirAll(logsDir, 0o755); err == nil {
		diagFile := filepath.Join(logsDir, "reconnect.log")
		if f, err := os.OpenFile(diagFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer f.Close()
			entry := fmt.Sprintf("[%s] browser=%s tab=%s: %s\n", time.Now().UTC().Format(time.RFC3339), browserName, tabID, msg)
			_, _ = f.WriteString(entry)
		}
	}
}

// ensureAlive verifies the session connection is alive and healthy.
// If the connection was dropped (CDP read-loop error), it performs supervised
// reconnect with exponential backoff + jitter, re-resolving the target via F2 SessionModel.
// If retries are exhausted, it returns a typed exit.Upstream error.
func (sess *tabSession) ensureAlive(ctx context.Context) error {
	if sess == nil {
		return exit.New(exit.NotFound, "nil tab session")
	}

	sess.conn.mu.Lock()
	dropped := sess.conn.closeErr != nil
	sess.conn.mu.Unlock()

	if !dropped {
		return nil
	}

	if sess.d == nil {
		return exit.New(exit.Upstream, "connection closed and daemon backpointer missing: %v", sess.conn.closeErr)
	}

	browserName := sess.d.br.Name
	tabID := sess.id
	logReconnect(browserName, tabID, fmt.Sprintf("CDP connection dropped (%v); starting supervised reconnect", sess.conn.closeErr))

	// Re-resolve target via F2 SessionModel if available
	lastTargetURL := ""
	if sm, err := contextmgr.LoadSession(sess.d.br.Name); err == nil {
		if sm.URL != "" {
			lastTargetURL = sm.URL
		}
	}

	maxRetries := 4
	baseBackoff := 100 * time.Millisecond

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Exponential backoff with jitter: backoff = base * 2^(attempt-1) + jitter(0..50ms)
		backoff := baseBackoff * time.Duration(1<<(attempt-1))
		jitter := time.Duration(rand.Intn(50)) * time.Millisecond
		sleepDur := backoff + jitter
		logReconnect(browserName, tabID, fmt.Sprintf("attempt %d/%d (waiting %s)", attempt, maxRetries, sleepDur))

		select {
		case <-ctx.Done():
			return exit.New(exit.Upstream, "reconnect aborted by context: %v", ctx.Err())
		case <-time.After(sleepDur):
		}

		// Re-discover port and live tabs
		_, _ = sess.d.discoverPort()
		tabs, err := sess.d.listTabs()
		if err != nil {
			logReconnect(browserName, tabID, fmt.Sprintf("attempt %d failed to list tabs: %v", attempt, err))
			continue
		}

		var targetTab Tab
		found := false

		// 1. Match by tab ID
		for _, t := range tabs {
			if strings.EqualFold(t.ID, tabID) || strings.HasPrefix(strings.ToLower(t.ID), strings.ToLower(tabID)) {
				targetTab = t
				found = true
				break
			}
		}

		// 2. If tab ID changed after restart, re-resolve by URL from F2 SessionModel
		if !found && lastTargetURL != "" {
			for _, t := range tabs {
				if strings.EqualFold(t.URL, lastTargetURL) || strings.HasPrefix(t.URL, lastTargetURL) {
					targetTab = t
					found = true
					logReconnect(browserName, tabID, fmt.Sprintf("re-resolved target by F2 URL %q -> new tab ID %s", lastTargetURL, t.ID))
					sess.id = t.ID
					break
				}
			}
		}

		// 3. Fallback to first non-empty tab
		if !found && len(tabs) > 0 {
			targetTab = tabs[0]
			found = true
			logReconnect(browserName, tabID, fmt.Sprintf("falling back to tab %s (%s)", targetTab.ID, targetTab.URL))
			sess.id = targetTab.ID
		}

		if !found {
			logReconnect(browserName, tabID, fmt.Sprintf("attempt %d: no matching tab found", attempt))
			continue
		}

		// Redial WebSocket connection
		newConn, err := dialTab(ctx, targetTab.WS)
		if err != nil {
			logReconnect(browserName, tabID, fmt.Sprintf("attempt %d failed to dial %s: %v", attempt, targetTab.WS, err))
			continue
		}

		// Re-enable domains and re-subscribe
		initCtx, initCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = cdp.Call(initCtx, newConn, page.Enable, page.EnableParams{})
		_, _ = cdp.Call(initCtx, newConn, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true})
		_, _ = cdp.Call(initCtx, newConn, dom.Enable, dom.EnableParams{})
		_, _ = cdp.Call(initCtx, newConn, accessibility.Enable, cdp.Empty{})
		_, _ = cdp.Call(initCtx, newConn, runtime.Enable, cdp.Empty{})
		_, _ = cdp.Call(initCtx, newConn, cdplog.Enable, cdp.Empty{})
		_, _ = cdp.Call(initCtx, newConn, network.Enable, network.EnableParams{})
		initCancel()

		sess.conn = newConn
		sess.wsURL = targetTab.WS
		sess.refs = make(map[string]cdp.BackendNodeID)
		sess.refDescs = make(map[string]RefDescriptor)

		// Restart watchers on new connection
		sess.watchDialogs(context.Background())
		sess.startDebugCapture(context.Background())

		// Update daemon sessions map
		sess.d.mu.Lock()
		sess.d.sessions[sess.id] = sess
		sess.d.mu.Unlock()

		logReconnect(browserName, tabID, fmt.Sprintf("reconnect successful on attempt %d; session restored", attempt))
		return nil
	}

	errMsg := fmt.Sprintf("CDP connection dropped and reconnect failed after %d attempts: %v", maxRetries, sess.conn.closeErr)
	logReconnect(browserName, tabID, errMsg)
	return exit.New(exit.Upstream, "%s", errMsg)
}

// eval evaluates JavaScript on the tabSession with automatic reconnect supervision.
func (sess *tabSession) eval(ctx context.Context, expr string, out any) error {
	if err := sess.ensureAlive(ctx); err != nil {
		return err
	}

	yes := true
	res, err := cdp.Call(ctx, sess.conn, runtime.Evaluate, runtime.EvaluateParams{
		Expression:    expr,
		ReturnByValue: &yes,
		AwaitPromise:  &yes,
	})
	if err != nil {
		// If call itself failed due to a sudden drop, try reconnect once more
		if sess.ensureAlive(ctx) == nil {
			res, err = cdp.Call(ctx, sess.conn, runtime.Evaluate, runtime.EvaluateParams{
				Expression:    expr,
				ReturnByValue: &yes,
				AwaitPromise:  &yes,
			})
		}
	}
	if err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		msg := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil && res.ExceptionDetails.Exception.Description != "" {
			msg = res.ExceptionDetails.Exception.Description
		}
		return exit.New(exit.User, "script threw: %s", msg)
	}
	if out != nil && res.Result != nil && len(res.Result.Value) > 0 {
		return json.Unmarshal([]byte(res.Result.Value), out)
	}
	return nil
}

var refPattern = regexp.MustCompile(`^e[0-9]+$`)

// selector maps a snapshot ref (e.g. "e12") to "[data-bxw-ref=\"e12\"]" after tagging it on the DOM.
//
// Resolution order (F1 stable refs):
//  1. Navigation guard — if the loader changed since the snapshot, the page navigated;
//     refs are invalid and the caller must snapshot again.
//  2. Fast path — resolve the recorded backend node id and tag it.
//  3. Descriptor fallback — a benign same-loader re-render can recreate the node with a
//     new backend id; re-find it by its stable role+name descriptor from a fresh AX tree
//     so the agent's eN keeps pointing at the same logical element.
func (sess *tabSession) selector(ctx context.Context, el string) (string, error) {
	if el == "" {
		return "", exit.New(exit.Validation, "an element (CSS selector or snapshot ref such as e12) is required")
	}
	if !refPattern.MatchString(el) {
		return el, nil
	}

	_, known := sess.refs[el]
	desc, hasDesc := sess.refDescs[el]
	if !known && !hasDesc {
		return "", exit.New(exit.User, "%s is not in this tab's latest snapshot: run bxw snapshot %s again", el, sess.id)
	}

	// 1. Navigation invalidates all refs.
	if sess.refLoader != "" && sess.loaderID(ctx) != sess.refLoader {
		return "", exit.New(exit.User, "%s is from a previous page (it navigated): run bxw snapshot %s again", el, sess.id)
	}

	// 2. Fast path: the recorded backend node is still live.
	if backend, ok := sess.refs[el]; ok {
		if sel, err := sess.tagBackend(ctx, el, backend); err == nil {
			return sel, nil
		}
	}

	// 3. Descriptor fallback: re-find the element by its stable identity after a re-render.
	if hasDesc {
		if tree, err := cdp.Call(ctx, sess.conn, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{}); err == nil {
			freshNodes, freshRefs, _ := buildSnapshot(tree.Nodes)
			if ref, found := findRefByDescriptor(freshNodes, desc); found {
				if nb, ok := freshRefs[ref]; ok {
					if sel, err := sess.tagBackend(ctx, el, nb); err == nil {
						sess.refs[el] = nb // refresh the fast-path mapping
						return sel, nil
					}
				}
			}
		}
	}

	return "", exit.New(exit.User, "%s is no longer on the page: run bxw snapshot %s again", el, sess.id)
}

// tagBackend resolves a backend node id to a JS object, tags it with data-bxw-ref=<el>,
// and returns the attribute selector. Returns an error if the node can no longer be resolved.
func (sess *tabSession) tagBackend(ctx context.Context, el string, backend cdp.BackendNodeID) (string, error) {
	node, err := cdp.Call(ctx, sess.conn, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: backend})
	if err != nil || node.Object == nil || node.Object.ObjectID == "" {
		return "", fmt.Errorf("resolve backend node %d: %v", backend, err)
	}
	tagFn := fmt.Sprintf(`function() { (this.nodeType === 3 ? this.parentElement : this).setAttribute("data-bxw-ref", %q); }`, el)
	if _, err := cdp.Call(ctx, sess.conn, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: tagFn,
		ObjectID:            node.Object.ObjectID,
	}); err != nil {
		return "", exit.New(exit.Upstream, "mark %s: %v", el, err)
	}
	return fmt.Sprintf(`[data-bxw-ref=%q]`, el), nil
}

// settle waits for navigations to complete and DOM mutations to quiet down.
func (sess *tabSession) settle(ctx context.Context, started *atomic.Bool) {
	time.Sleep(100 * time.Millisecond)
	if started.Load() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			var state string
			_ = sess.eval(ctx, `document.readyState`, &state)
			if state == "complete" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	_ = sess.eval(ctx, stableDOMJS, nil)
}

const stableDOMJS = `new Promise(resolve => {
	let quiet;
	const done = () => { observer.disconnect(); resolve(true); };
	const observer = new MutationObserver(() => { clearTimeout(quiet); quiet = setTimeout(done, 100); });
	observer.observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
	quiet = setTimeout(done, 100);
	setTimeout(done, 1000);
})`
