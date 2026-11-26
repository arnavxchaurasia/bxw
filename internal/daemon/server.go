package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"

	"bxw/internal/browser"
	"bxw/internal/config"
	"bxw/internal/contextmgr"
	"bxw/internal/exit"
	"bxw/internal/osport"
)

// Daemon manages the browser connection and handles client requests.
type Daemon struct {
	br       browser.Browser
	mu       sync.Mutex
	launchMu sync.Mutex
	cdpPort  int
	agentPID int
	headless bool
	stop     chan struct{}
	since    time.Time

	sessions  map[string]*tabSession
	profileOf map[cdp.BrowserContextID]string
}

// Serve runs the daemon for browserName until stopped.
func Serve(browserName string) int {
	b, ok := browser.Find(browserName)
	if !ok {
		return reportStart(exit.New(exit.NotFound, "browser %q not found", browserName))
	}

	p := osport.Current()
	sock := p.SocketPath(b.Name)
	lock := p.LockPath(b.Name)
	portF := portFile(p, b.Name)
	_ = os.Remove(sock) // clean up stale socket

	ln, err := net.Listen("unix", sock)
	if err != nil {
		return reportStart(exit.New(exit.Upstream, "listen on %s: %v", sock, err))
	}
	defer func() {
		_ = os.Remove(sock)
		_ = os.Remove(lock)
		_ = os.Remove(portF)
	}()
	defer ln.Close()

	d := &Daemon{
		br:        b,
		stop:      make(chan struct{}),
		since:     time.Now(),
		sessions:  make(map[string]*tabSession),
		profileOf: make(map[cdp.BrowserContextID]string),
	}

	// Try discovering existing port (if already running)
	_, _ = d.discoverPort()

	reportStart(nil)

	go d.accept(ln)

	select {
	case <-d.stop:
		log.Printf("stop requested; daemon exiting")
	}
	d.teardownBrowser(p)
	return 0
}

// teardownBrowser terminates the browser instance this daemon launched into its
// own persistent bxw profile, so launch→stop cycles leave no orphaned process
// and the next launch starts clean instead of colliding with the singleton
// lock. Only the bxw-managed profile path is targeted; a real-profile (brave)
// attach launched nothing here, and the user's own browser uses a different
// profile, so neither is touched. The on-disk profile (logins, cookies)
// persists — only the running process is killed.
func (d *Daemon) teardownBrowser(p osport.Platform) {
	profileDir := p.ProfileDir(d.br.Name)
	if n, err := p.KillByProfileDir(profileDir); err != nil {
		log.Printf("browser teardown for %s failed: %v", d.br.Name, err)
	} else if n > 0 {
		log.Printf("terminated %d browser process(es) for profile %s", n, profileDir)
	}
}

func reportStart(err error) int {
	r := Response{}
	if err != nil {
		r = errorResponse(err)
		log.Printf("daemon start failed: %s", r.Error)
	}
	_ = json.NewEncoder(os.Stdout).Encode(r)
	_ = os.Stdout.Close()
	if err != nil {
		return r.Code
	}
	return 0
}

func (d *Daemon) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.stop:
				return
			default:
				log.Printf("accept err: %v", err)
				return
			}
		}
		go d.handle(conn)
	}
}

func (d *Daemon) handle(conn net.Conn) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: "malformed request", Code: int(exit.Usage)})
		return
	}

	resp := d.dispatch(req)
	_ = json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) dispatch(req Request) Response {
	timeout := 30 * time.Second
	if tStr, ok := req.Args["timeout"].(string); ok && tStr != "" {
		if t, err := time.ParseDuration(tStr); err == nil && t > 0 {
			timeout = t
		}
	}

	switch req.Op {
	case "tabs", "list_tabs":
		profileRef := ""
		if req.Args != nil {
			if pr, ok := req.Args["profile"].(string); ok {
				profileRef = pr
			}
		}
		tabs, err := d.listTabsFiltered(profileRef)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(tabs)
		return Response{Data: data}

	case "open":
		profileRef := ""
		targetURL := "about:blank"
		followOverride := false
		hasFollowOverride := false
		if req.Args != nil {
			if pr, ok := req.Args["profile"].(string); ok {
				profileRef = pr
			}
			if u, ok := req.Args["url"].(string); ok && u != "" {
				targetURL = u
			}
			if f, ok := req.Args["follow"].(bool); ok {
				followOverride = f
				hasFollowOverride = true
			}
		}
		tab, err := d.open(profileRef, targetURL, timeout)
		if err != nil {
			return errorResponse(err)
		}
		if hasFollowOverride && followOverride && !config.IsFollowEnabled() {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			if bconn, err := d.browserConn(ctx); err == nil {
				_, _ = cdp.Call(ctx, bconn, target.ActivateTarget, target.ActivateTargetParams{TargetID: target.ID(tab.ID)})
				_ = bconn.Close()
			}
		}
		data, _ := json.Marshal(tab)
		return Response{Data: data}

	case "close_tab":
		tab, _ := req.Args["tab"].(string)
		if tab == "" {
			return errorResponse(exit.New(exit.Validation, "close_tab requires tab id"))
		}
		t, err := d.findTab(tab)
		if err != nil {
			return errorResponse(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		bconn, err := d.browserConn(ctx)
		if err != nil {
			return errorResponse(err)
		}
		defer bconn.Close()

		// Refuse to close the last tab to avoid abrupt browser exit and lost CDP session
		if tabs, err := d.listTabs(); err == nil && len(tabs) <= 1 {
			return errorResponse(exit.New(exit.User, "refusing to close the last tab — the browser would exit; run bxw stop %s to shut down, or open another tab first", d.br.Name))
		}

		if _, err := cdp.Call(ctx, bconn, target.CloseTarget, target.CloseTargetParams{TargetID: target.ID(t.ID)}); err != nil {
			return errorResponse(exit.New(exit.Upstream, "close target: %v", err))
		}

		d.mu.Lock()
		delete(d.sessions, t.ID)
		d.mu.Unlock()

		_ = DeleteTabState(d.br.Name, t.ID)

		data, _ := json.Marshal(map[string]any{"closed": t.ID, "success": true})
		return Response{Data: data}

	case "goto":
		tab, _ := req.Args["tab"].(string)
		targetURL, _ := req.Args["url"].(string)
		followOverride := false
		hasFollowOverride := false
		if f, ok := req.Args["follow"].(bool); ok {
			followOverride = f
			hasFollowOverride = true
		}
		if targetURL == "" {
			return errorResponse(exit.New(exit.Validation, "goto requires url"))
		}
		res, err := d.gotoURL(tab, targetURL, timeout)
		if err != nil {
			return errorResponse(err)
		}
		if hasFollowOverride && followOverride && !config.IsFollowEnabled() {
			if sess, err := d.tabSession(tab); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				_, _ = cdp.Call(ctx, sess.conn, page.BringToFront, cdp.Empty{})
			}
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "wait":
		tab, _ := req.Args["tab"].(string)
		selector, _ := req.Args["selector"].(string)
		text, _ := req.Args["text"].(string)
		gone, _ := req.Args["gone"].(string)
		stable, _ := req.Args["stable"].(bool)
		netIdle, _ := req.Args["network_idle"].(bool)
		if !netIdle {
			netIdle, _ = req.Args["networkIdle"].(bool)
		}
		load, _ := req.Args["load"].(bool)
		var ms int
		if msVal, ok := req.Args["ms"].(float64); ok {
			ms = int(msVal)
		} else if msInt, ok := req.Args["ms"].(int); ok {
			ms = msInt
		}
		cond := waitCond{
			Selector:    selector,
			Text:        text,
			Gone:        gone,
			Stable:      stable,
			NetworkIdle: netIdle,
			Load:        load,
			MS:          ms,
		}
		res, err := d.wait(tab, cond, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "read":
		tab, _ := req.Args["tab"].(string)
		ref, _ := req.Args["ref"].(string)
		var max int
		if mVal, ok := req.Args["max"].(float64); ok {
			max = int(mVal)
		} else if mInt, ok := req.Args["max"].(int); ok {
			max = mInt
		}
		text, err := d.read(tab, ref, max, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(map[string]any{"text": text})
		return Response{Data: data}

	case "launch":
		headless := false
		realProfile := false
		targetURL := "about:blank"
		if req.Args != nil {
			if h, ok := req.Args["headless"].(bool); ok {
				headless = h
			}
			if r, ok := req.Args["real_profile"].(bool); ok {
				realProfile = r
			}
			if u, ok := req.Args["url"].(string); ok && u != "" {
				targetURL = u
			}
		}
		if err := d.launch(headless, targetURL, realProfile); err != nil {
			return errorResponse(err)
		}
		d.mu.Lock()
		d.headless = headless
		port := d.cdpPort
		d.mu.Unlock()
		data, _ := json.Marshal(map[string]any{"port": port, "ws": fmt.Sprintf("ws://127.0.0.1:%d", port)})
		return Response{Data: data}

	case "status":
		d.mu.Lock()
		port := d.cdpPort
		pid := d.agentPID
		d.mu.Unlock()
		st := map[string]any{
			"browser":   d.br.Name,
			"pid":       os.Getpid(),
			"cdp_port":  port,
			"agent_pid": pid,
			"since":     d.since,
		}
		data, _ := json.Marshal(st)
		return Response{Data: data}

	case "snapshot":
		tab, _ := req.Args["tab"].(string)
		full, _ := req.Args["full"].(bool)
		scopeRef, _ := req.Args["ref"].(string)
		findQuery, _ := req.Args["find"].(string)
		budget := 0
		if b, ok := req.Args["budget"].(float64); ok {
			budget = int(b)
		} else if b, ok := req.Args["budget"].(int); ok {
			budget = b
		}
		snap, err := d.snapshot(tab, full, scopeRef, findQuery, budget, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(snap)
		return Response{Data: data}

	case "query":
		tab, _ := req.Args["tab"].(string)
		role, _ := req.Args["role"].(string)
		name, _ := req.Args["name"].(string)
		text, _ := req.Args["text"].(string)
		near, _ := req.Args["near"].(string)
		limit := 0
		if l, ok := req.Args["limit"].(float64); ok {
			limit = int(l)
		} else if l, ok := req.Args["limit"].(int); ok {
			limit = l
		}
		res, err := d.query(tab, QueryParams{
			Role:  role,
			Name:  name,
			Text:  text,
			Near:  near,
			Limit: limit,
		}, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "click":
		tab, _ := req.Args["tab"].(string)
		sel, _ := req.Args["selector"].(string)
		double, _ := req.Args["double"].(bool)
		wait, _ := req.Args["wait"].(bool)
		res, err := d.click(tab, sel, double, wait, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "hover":
		tab, _ := req.Args["tab"].(string)
		sel, _ := req.Args["selector"].(string)
		res, err := d.hover(tab, sel, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "drag":
		tab, _ := req.Args["tab"].(string)
		from, _ := req.Args["from"].(string)
		to, _ := req.Args["to"].(string)
		res, err := d.drag(tab, from, to, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "upload":
		tab, _ := req.Args["tab"].(string)
		sel, _ := req.Args["selector"].(string)
		var paths []string
		if pSlice, ok := req.Args["paths"].([]any); ok {
			for _, p := range pSlice {
				if s, ok := p.(string); ok {
					paths = append(paths, s)
				}
			}
		} else if pStr, ok := req.Args["paths"].(string); ok {
			_ = json.Unmarshal([]byte(pStr), &paths)
		}
		res, err := d.upload(tab, sel, paths, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "fill":
		tab, _ := req.Args["tab"].(string)
		var pairs [][2]string
		if raw, ok := req.Args["pairs"].(string); ok {
			_ = json.Unmarshal([]byte(raw), &pairs)
		}
		wait, _ := req.Args["wait"].(bool)
		res, err := d.fill(tab, pairs, wait, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "type":
		tab, _ := req.Args["tab"].(string)
		text, _ := req.Args["text"].(string)
		submit, _ := req.Args["submit"].(string)
		res, err := d.typeText(tab, text, submit, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "key":
		tab, _ := req.Args["tab"].(string)
		keys, _ := req.Args["keys"].(string)
		res, err := d.key(tab, keys, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "eval":
		tab, _ := req.Args["tab"].(string)
		script, _ := req.Args["script"].(string)
		sess, err := d.tabSession(tab)
		if err != nil {
			return errorResponse(err)
		}
		sess.mu.Lock()
		defer sess.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var out any
		if err := sess.eval(ctx, script, &out); err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(out)
		return Response{Data: data}

	case "screenshot":
		tab, _ := req.Args["tab"].(string)
		pngBytes, err := d.screenshot(tab, timeout)
		if err != nil {
			return errorResponse(err)
		}
		return Response{Data: pngBytes}

	case "dialog":
		tab, _ := req.Args["tab"].(string)
		action, _ := req.Args["action"].(string)
		promptText, _ := req.Args["prompt_text"].(string)
		if action != "accept" && action != "dismiss" {
			return errorResponse(exit.New(exit.Validation, "dialog action must be 'accept' or 'dismiss'"))
		}
		sess, err := d.tabSession(tab)
		if err != nil {
			return errorResponse(err)
		}
		sess.mu.Lock()
		defer sess.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		accept := action == "accept"
		if _, err := cdp.Call(ctx, sess.conn, page.HandleJavaScriptDialog, page.HandleJavaScriptDialogParams{
			Accept:     accept,
			PromptText: promptText,
		}); err != nil {
			return errorResponse(exit.New(exit.Upstream, "handle dialog: %v", err))
		}

		sess.dmu.Lock()
		sess.dialog = nil
		sess.dmu.Unlock()

		data, _ := json.Marshal(map[string]any{"action": action, "handled": true})
		return Response{Data: data}

	case "console":
		tab, _ := req.Args["tab"].(string)
		level, _ := req.Args["level"].(string)
		var limit int
		if lVal, ok := req.Args["limit"].(float64); ok {
			limit = int(lVal)
		} else if lInt, ok := req.Args["limit"].(int); ok {
			limit = lInt
		}
		logs, err := d.console(tab, level, limit)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(logs)
		return Response{Data: data}

	case "network":
		tab, _ := req.Args["tab"].(string)
		filter, _ := req.Args["filter"].(string)
		requestID, _ := req.Args["request_id"].(string)
		var limit int
		if lVal, ok := req.Args["limit"].(float64); ok {
			limit = int(lVal)
		} else if lInt, ok := req.Args["limit"].(int); ok {
			limit = lInt
		}
		res, err := d.network(tab, filter, requestID, limit)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "storage":
		tab, _ := req.Args["tab"].(string)
		res, err := d.storage(tab, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "performance":
		tab, _ := req.Args["tab"].(string)
		res, err := d.performance(tab, timeout)
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "emulate":
		tab, _ := req.Args["tab"].(string)
		device, _ := req.Args["device"].(string)
		netCond, _ := req.Args["network"].(string)
		var cpu float64
		if cVal, ok := req.Args["cpu"].(float64); ok {
			cpu = cVal
		} else if cInt, ok := req.Args["cpu"].(int); ok {
			cpu = float64(cInt)
		}
		geo, _ := req.Args["geo"].(string)
		media, _ := req.Args["media"].(string)

		opts := emulateOpts{
			Device:  device,
			Network: netCond,
			CPU:     cpu,
			Geo:     geo,
			Media:   media,
		}
		if err := d.emulate(tab, opts, timeout); err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(map[string]any{"emulated": true})
		return Response{Data: data}

	case "cdp":
		tab, _ := req.Args["tab"].(string)
		method, _ := req.Args["method"].(string)
		paramsStr, _ := req.Args["params"].(string)
		if method == "" {
			return errorResponse(exit.New(exit.Validation, "cdp requires method"))
		}

		var p any
		if paramsStr != "" {
			if err := json.Unmarshal([]byte(paramsStr), &p); err != nil {
				return errorResponse(exit.New(exit.Validation, "bad params json: %v", err))
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		var raw json.RawMessage
		if tab != "" {
			sess, err := d.tabSession(tab)
			if err != nil {
				return errorResponse(err)
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			if err := sess.conn.Call(ctx, method, p, &raw); err != nil {
				return errorResponse(exit.New(exit.Upstream, "%s: %v", method, err))
			}
		} else {
			bconn, err := d.browserConn(ctx)
			if err != nil {
				return errorResponse(err)
			}
			defer bconn.Close()
			if err := bconn.Call(ctx, method, p, &raw); err != nil {
				return errorResponse(exit.New(exit.Upstream, "%s: %v", method, err))
			}
		}

		if len(raw) == 0 {
			raw = []byte("{}")
		}
		return Response{Data: []byte(raw)}

	case "recall":
		session, _ := req.Args["session"].(string)
		tab, _ := req.Args["tab"].(string)
		grep, _ := req.Args["grep"].(string)
		since, _ := req.Args["since"].(string)
		var limit int
		if lVal, ok := req.Args["limit"].(float64); ok {
			limit = int(lVal)
		} else if lInt, ok := req.Args["limit"].(int); ok {
			limit = lInt
		}
		browserName, _ := req.Args["browser"].(string)
		res, err := d.Recall(RecallParams{
			Session: session,
			Tab:     tab,
			Grep:    grep,
			Since:   since,
			Limit:   limit,
			Browser: browserName,
		})
		if err != nil {
			return errorResponse(err)
		}
		data, _ := json.Marshal(res)
		return Response{Data: data}

	case "done":
		session, _ := req.Args["session"].(string)
		takeaway, _ := req.Args["takeaway"].(string)
		if err := contextmgr.CloseoutSession(session, takeaway); err != nil {
			return errorResponse(err)
		}
		sc, _ := contextmgr.LoadSession(session)
		data, _ := json.Marshal(map[string]any{
			"session_id": sc.SessionID,
			"takeaway":   sc.Takeaway,
			"closed_at":  sc.UpdatedAt,
		})
		return Response{Data: data}

	case "stop":
		select {
		case <-d.stop:
		default:
			close(d.stop)
		}
		return Response{}

	default:
		return Response{Error: fmt.Sprintf("unknown op %q", req.Op), Code: int(exit.Usage)}
	}
}

func (d *Daemon) findTab(tabID string) (Tab, error) {
	tabs, err := d.listTabs()
	if err != nil {
		return Tab{}, err
	}
	if len(tabs) == 0 {
		return Tab{}, exit.New(exit.NotFound, "no open tabs in %s", d.br.Label)
	}

	tabIDLower := strings.ToLower(strings.TrimSpace(tabID))
	if tabIDLower == "" && len(tabs) == 1 {
		return tabs[0], nil
	}

	for _, t := range tabs {
		if strings.EqualFold(t.ID, tabIDLower) || strings.EqualFold(shortTabID(t.ID), tabIDLower) {
			return t, nil
		}
	}
	// Prefix match
	for _, t := range tabs {
		if strings.HasPrefix(strings.ToLower(t.ID), tabIDLower) {
			return t, nil
		}
	}

	return Tab{}, exit.New(exit.NotFound, "tab %q not found (run: bxw list tabs %s)", tabID, d.br.Name)
}

func (d *Daemon) tabSession(tabID string) (*tabSession, error) {
	tab, err := d.findTab(tabID)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	if sess := d.sessions[tab.ID]; sess != nil && sess.conn != nil && sess.conn.closeErr == nil {
		d.mu.Unlock()
		return sess, nil
	}
	d.mu.Unlock()

	tc, err := dialTab(context.Background(), tab.WS)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _ = cdp.Call(ctx, tc, page.Enable, page.EnableParams{})
	_, _ = cdp.Call(ctx, tc, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true})
	_, _ = cdp.Call(ctx, tc, dom.Enable, dom.EnableParams{})
	_, _ = cdp.Call(ctx, tc, accessibility.Enable, cdp.Empty{})
	_, _ = cdp.Call(ctx, tc, runtime.Enable, cdp.Empty{})
	_, _ = cdp.Call(ctx, tc, cdplog.Enable, cdp.Empty{})
	_, _ = cdp.Call(ctx, tc, network.Enable, network.EnableParams{})

	sess := &tabSession{
		id:           tab.ID,
		wsURL:        tab.WS,
		conn:         tc,
		refs:         make(map[string]cdp.BackendNodeID),
		refDescs:     make(map[string]RefDescriptor),
		consoleBuf:   newRingBuffer(500),
		netBuf:       newRingBuffer(500),
		inFlightReqs: make(map[network.RequestID]time.Time),
	}
	sess.watchDialogs(context.Background())
	sess.startDebugCapture(context.Background())

	d.mu.Lock()
	d.sessions[tab.ID] = sess
	d.mu.Unlock()

	return sess, nil
}

func shortTabID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func errorResponse(err error) Response {
	var e *exit.Error
	if errorsAs(err, &e) {
		return Response{Error: e.Msg, Code: int(e.Class)}
	}
	return Response{Error: fmt.Sprint(err), Code: int(exit.Upstream)}
}

func errorsAs(err error, target any) bool {
	if e, ok := err.(*exit.Error); ok {
		if ptr, ok := target.(**exit.Error); ok {
			*ptr = e
			return true
		}
	}
	return false
}
