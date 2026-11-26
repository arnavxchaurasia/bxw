package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"bxw/internal/browser"
	"bxw/internal/cache"
	"bxw/internal/daemon"
	"bxw/internal/exit"
)

const mcpProtocol = "2024-11-05"

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	run         func(args map[string]any) (string, error)
}

func runMCP(args []string) error {
	if len(args) > 0 && args[0] == "install" {
		return runMCPInstall(args[1:])
	}
	return serveMCP(os.Stdin, os.Stdout)
}

func serveMCP(in io.Reader, out io.Writer) error {
	tools := mcpTools()
	byName := make(map[string]mcpTool, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
	}

	var mu sync.Mutex
	enc := json.NewEncoder(out)
	send := func(m rpcMsg) {
		m.JSONRPC = "2.0"
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(m)
	}

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)

	var wg sync.WaitGroup
	for sc.Scan() {
		var m rpcMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			send(rpcMsg{Error: &rpcError{-32700, "parse error"}})
			continue
		}
		if len(m.ID) == 0 {
			continue // notification
		}
		wg.Add(1)
		go func(msg rpcMsg) {
			defer wg.Done()
			send(handleMCP(msg, tools, byName))
		}(m)
	}
	wg.Wait()
	return sc.Err()
}

func handleMCP(m rpcMsg, tools []mcpTool, byName map[string]mcpTool) rpcMsg {
	reply := rpcMsg{ID: m.ID}
	switch m.Method {
	case "initialize":
		reply.Result = map[string]any{
			"protocolVersion": mcpProtocol,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo":   map[string]any{"name": "bxw", "version": Version},
			"instructions": mcpInstructions,
		}
	case "ping":
		reply.Result = map[string]any{}
	case "resources/list":
		reply.Result = map[string]any{"resources": []any{}}
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if strings.HasPrefix(p.URI, "bxw://cache/") {
			data, err := cache.Get(p.URI)
			if err != nil {
				reply.Error = &rpcError{-32602, fmt.Sprintf("resource not found: %v", err)}
			} else {
				mime := "application/octet-stream"
				if strings.HasSuffix(p.URI, ".png") {
					mime = "image/png"
				} else if strings.HasSuffix(p.URI, ".html") {
					mime = "text/html"
				}
				reply.Result = map[string]any{
					"contents": []any{
						map[string]any{
							"uri":      p.URI,
							"mimeType": mime,
							"blob":     string(data),
						},
					},
				}
			}
		} else {
			reply.Error = &rpcError{-32602, fmt.Sprintf("unsupported URI scheme: %s", p.URI)}
		}
	case "tools/list":
		reply.Result = map[string]any{"tools": tools}
	case "tools/call":
		reply.Result = callTool(m.Params, byName)
	default:
		reply.Error = &rpcError{-32601, "method not found: " + m.Method}
	}
	return reply
}

const mcpInstructions = `bxw drives the user's running Windows Chromium browser (Edge, Brave, Chrome) over CDP. ` +
	`Start with list_tabs or open, then snapshot a tab: each element gets a ref like e12 that click, ` +
	`fill, type, hover, drag, and upload actions accept. Inspect pages with read, console, network, ` +
	`storage, and performance; emulate mobile or network with emulate. ` +
	`For form submission or buying/deleting, ask the user first.`

func callTool(params json.RawMessage, byName map[string]mcpTool) map[string]any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolError(exit.New(exit.Validation, "bad tools/call params: %v", err))
	}
	t, ok := byName[p.Name]
	if !ok {
		return toolError(exit.New(exit.Validation, "unknown tool %q", p.Name))
	}
	if p.Arguments == nil {
		p.Arguments = make(map[string]any)
	}
	out, err := t.run(p.Arguments)
	if err != nil {
		return toolError(err)
	}
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": out}},
	}
}

func toolError(err error) map[string]any {
	msg := fmt.Sprintf("%v (exit code %d)", err, exit.CodeOf(err))
	return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": msg}}}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func number(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func argString(args map[string]any, k string) string {
	switch v := args[k].(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		if v == nil {
			return ""
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func argBool(args map[string]any, k string) bool {
	if b, ok := args[k].(bool); ok {
		return b
	}
	if s, ok := args[k].(string); ok {
		return s == "true" || s == "1"
	}
	return false
}

func mcpTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "list_browsers",
			Description: "List installed Chromium browsers, user data dirs, and their running/debugging status.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			run: func(args map[string]any) (string, error) {
				bs := browser.Installed()
				b, _ := json.MarshalIndent(bs, "", "  ")
				return string(b), nil
			},
		},
		{
			Name:        "list_tabs",
			Description: "List open tabs in the browser, including IDs, titles, URLs, and profiles.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"browser": str("Browser name (e.g. edge, brave). Defaults to active/first installed."),
					"profile": str("Filter tabs by profile display name or directory."),
				},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				data, err := daemon.Call(bName, "list_tabs", map[string]any{"profile": argString(args, "profile")})
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "open",
			Description: "Open a URL in a new tab, optionally targeting a specific browser profile.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":     str("The URL to open."),
					"profile": str("Profile name or directory (e.g. 'Work', 'Profile 1')."),
					"follow":  boolean("Bring the opened tab to the foreground window."),
					"browser": str("Browser name."),
				},
				"required": []string{"url"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				callArgs := map[string]any{
					"url":     argString(args, "url"),
					"profile": argString(args, "profile"),
				}
				if f, ok := args["follow"].(bool); ok && f {
					callArgs["follow"] = true
				}
				data, err := daemon.Call(bName, "open", callArgs)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "close_tab",
			Description: "Close an open tab by tab ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID to close."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				tab := argString(args, "tab")
				_, err = daemon.Call(bName, "close_tab", map[string]any{
					"tab": tab,
				})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("closed tab %s", short(tab)), nil
			},
		},
		{
			Name:        "goto",
			Description: "Navigate an existing tab to a new URL.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID to navigate."),
					"url":     str("Target URL."),
					"follow":  boolean("Bring the navigated tab to the foreground window."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "url"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				callArgs := map[string]any{
					"tab": argString(args, "tab"),
					"url": argString(args, "url"),
				}
				if f, ok := args["follow"].(bool); ok && f {
					callArgs["follow"] = true
				}
				data, err := daemon.Call(bName, "goto", callArgs)
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary("goto"), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "wait",
			Description: "Wait for a condition in a tab (DOM selector, text content, element removal, DOM stability, network idle, page load completion, or sleep ms).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":          str("Tab ID to wait on."),
					"selector":     str("Wait until element matching CSS selector or ref is present."),
					"text":         str("Wait until text appears on page."),
					"gone":         str("Wait until element matching CSS selector or ref is removed/detached."),
					"stable":       boolean("Wait until DOM mutations quiet down for 100ms."),
					"network_idle": boolean("Wait until 0 network requests in-flight for 500ms."),
					"load":         boolean("Wait until document.readyState === 'complete'."),
					"ms":           number("Sleep for specified milliseconds."),
					"timeout":      str("Timeout duration (e.g. '5s', '10s')."),
					"browser":      str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				load, _ := args["load"].(bool)
				stable, _ := args["stable"].(bool)
				netIdle, _ := args["network_idle"].(bool)
				var ms int
				if m, ok := args["ms"].(float64); ok {
					ms = int(m)
				}
				waitArgs := map[string]any{
					"tab": argString(args, "tab"),
				}
				if s := argString(args, "selector"); s != "" {
					waitArgs["selector"] = s
				}
				if t := argString(args, "text"); t != "" {
					waitArgs["text"] = t
				}
				if g := argString(args, "gone"); g != "" {
					waitArgs["gone"] = g
				}
				if stable {
					waitArgs["stable"] = true
				}
				if netIdle {
					waitArgs["network_idle"] = true
				}
				if load {
					waitArgs["load"] = true
				}
				if ms > 0 {
					waitArgs["ms"] = ms
				}
				if toStr := argString(args, "timeout"); toStr != "" {
					if dur, err := time.ParseDuration(toStr); err == nil {
						waitArgs["timeout_ms"] = int(dur.Milliseconds())
					}
				}
				data, err := daemon.Call(bName, "wait", waitArgs)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "read",
			Description: "Read the page's visible text content, token-budgeted and optional scoped to a ref or CSS selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":       str("Tab ID to read."),
					"ref":       str("Optional snapshot ref (e.g. 'e12') or CSS selector to scope text extraction."),
					"max":       number("Maximum characters to return before truncation (default 20000)."),
					"no_redact": boolean("Disable secret/PII redaction (requires permission grant for origin)."),
					"browser":   str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				var max int
				if m, ok := args["max"].(float64); ok {
					max = int(m)
				}
				readArgs := map[string]any{
					"tab": argString(args, "tab"),
				}
				if r := argString(args, "ref"); r != "" {
					readArgs["ref"] = r
				}
				if max > 0 {
					readArgs["max"] = max
				}
				if argBool(args, "no_redact") {
					readArgs["no_redact"] = true
				}
				data, err := daemon.Call(bName, "read", readArgs)
				if err != nil {
					return "", err
				}
				var res struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(data, &res); err == nil {
					return res.Text, nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "console",
			Description: "Tail console logs and runtime exceptions captured from a tab.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":       str("Tab ID."),
					"level":     str("Optional log level filter: 'info', 'warning', 'error', 'log'."),
					"limit":     number("Max entries to return (default 50)."),
					"no_redact": boolean("Disable secret/PII redaction (requires permission grant for origin)."),
					"browser":   str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				var limit int
				if l, ok := args["limit"].(float64); ok {
					limit = int(l)
				}
				cArgs := map[string]any{
					"tab": argString(args, "tab"),
				}
				if lvl := argString(args, "level"); lvl != "" {
					cArgs["level"] = lvl
				}
				if limit > 0 {
					cArgs["limit"] = limit
				}
				if argBool(args, "no_redact") {
					cArgs["no_redact"] = true
				}
				data, err := daemon.Call(bName, "console", cArgs)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "network",
			Description: "Inspect captured network requests and responses from a tab, or retrieve response body by request ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":        str("Tab ID."),
					"filter":     str("Optional substring filter on request URL."),
					"request_id": str("Optional request ID to fetch response body and details."),
					"limit":      number("Max entries to return (default 50)."),
					"no_redact":  boolean("Disable secret/PII redaction (requires permission grant for origin)."),
					"browser":    str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				var limit int
				if l, ok := args["limit"].(float64); ok {
					limit = int(l)
				}
				nArgs := map[string]any{
					"tab": argString(args, "tab"),
				}
				if f := argString(args, "filter"); f != "" {
					nArgs["filter"] = f
				}
				if id := argString(args, "request_id"); id != "" {
					nArgs["request_id"] = id
				}
				if limit > 0 {
					nArgs["limit"] = limit
				}
				if argBool(args, "no_redact") {
					nArgs["no_redact"] = true
				}
				data, err := daemon.Call(bName, "network", nArgs)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "storage",
			Description: "Inspect cookies, localStorage, and sessionStorage for a tab.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":       str("Tab ID."),
					"no_redact": boolean("Disable secret/PII redaction (requires permission grant for origin)."),
					"browser":   str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				sArgs := map[string]any{
					"tab": argString(args, "tab"),
				}
				if argBool(args, "no_redact") {
					sArgs["no_redact"] = true
				}
				data, err := daemon.Call(bName, "storage", sArgs)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "performance",
			Description: "Audit Core Web Vitals (LCP, FCP, CLS, TTFB) with ratings, long tasks, slowest resources, and DOM/heap sizes.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				data, err := daemon.Call(bName, "performance", map[string]any{
					"tab": argString(args, "tab"),
				})
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "snapshot",
			Description: "Take an accessibility snapshot of a tab, returning an indented element tree with stable refs (e1, e2...). Returns diffs vs previous snapshot by default to save tokens.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":       str("Tab ID from list_tabs or open."),
					"browser":   str("Browser name."),
					"full":      boolean("Return full accessibility tree without diffing (default false)."),
					"ref":       str("Optional element ref (e.g. 'e12') to scope snapshot to a subtree."),
					"find":      str("Optional search string to return matching elements and ancestors."),
					"budget":    number("Optional token budget to prune less salient subtrees with drill-down hints."),
					"no_redact": boolean("Disable secret/PII redaction (requires permission grant for origin)."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				full, _ := args["full"].(bool)
				callArgs := map[string]any{
					"tab":  argString(args, "tab"),
					"full": full,
					"ref":  argString(args, "ref"),
					"find": argString(args, "find"),
				}
				if b, ok := args["budget"].(float64); ok && b > 0 {
					callArgs["budget"] = int(b)
				}
				if argBool(args, "no_redact") {
					callArgs["no_redact"] = true
				}
				data, err := daemon.Call(bName, "snapshot", callArgs)
				if err != nil {
					return "", err
				}
				var snap daemon.Snapshot
				if err := json.Unmarshal(data, &snap); err != nil {
					return string(data), nil
				}
				return snap.Text(), nil
			},
		},
		{
			Name:        "query",
			Description: "Filter and rank elements on a page by role, accessible name, text, and proximity to another label. Returns ranked refs with 1-line context to save tokens.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":       str("Tab ID from list_tabs or open."),
					"browser":   str("Browser name."),
					"role":      str("Optional ARIA role to filter (e.g. 'button', 'textbox', 'link')."),
					"name":      str("Optional accessible name to match (exact or substring)."),
					"text":      str("Optional text to match in name or value."),
					"near":      str("Optional label or text of a nearby element to bias ranking by proximity."),
					"limit":     number("Maximum number of results to return (default 10)."),
					"no_redact": boolean("Disable secret/PII redaction (requires permission grant for origin)."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				callArgs := map[string]any{
					"tab": argString(args, "tab"),
				}
				if r := argString(args, "role"); r != "" {
					callArgs["role"] = r
				}
				if n := argString(args, "name"); n != "" {
					callArgs["name"] = n
				}
				if t := argString(args, "text"); t != "" {
					callArgs["text"] = t
				}
				if nr := argString(args, "near"); nr != "" {
					callArgs["near"] = nr
				}
				if l, ok := args["limit"].(float64); ok && l > 0 {
					callArgs["limit"] = int(l)
				}
				if argBool(args, "no_redact") {
					callArgs["no_redact"] = true
				}
				data, err := daemon.Call(bName, "query", callArgs)
				if err != nil {
					return "", err
				}
				var res daemon.QueryResult
				if err := json.Unmarshal(data, &res); err != nil {
					return string(data), nil
				}
				return res.Text(), nil
			},
		},
		{
			Name:        "screenshot",
			Description: "Capture page viewport as PNG, saving to content-addressed cache and returning a bxw://cache/<hash>.png resource URI.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				data, err := daemon.Call(bName, "screenshot", map[string]any{
					"tab": argString(args, "tab"),
				})
				if err != nil {
					return "", err
				}
				uri, _, err := cache.Put(data, ".png")
				if err != nil {
					return "", exit.New(exit.Internal, "cache screenshot: %v", err)
				}
				return uri, nil
			},
		},
		{
			Name:        "click",
			Description: "Click an element by ref (e.g. 'e12') or CSS selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"element": str("Ref from latest snapshot (e.g. 'e12') or CSS selector."),
					"double":  boolean("Double-click when true."),
					"wait":    boolean("Auto-wait for element actionability (visible, attached, enabled) before clicking."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "element"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				double, _ := args["double"].(bool)
				wait, _ := args["wait"].(bool)
				data, err := daemon.Call(bName, "click", map[string]any{
					"tab":      argString(args, "tab"),
					"selector": argString(args, "element"),
					"double":   double,
					"wait":     wait,
				})
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary(fmt.Sprintf("clicked %s", argString(args, "element"))), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "hover",
			Description: "Hover mouse over an element by ref (e.g. 'e12') or CSS selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"element": str("Ref from latest snapshot (e.g. 'e12') or CSS selector."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "element"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				el := argString(args, "element")
				data, err := daemon.Call(bName, "hover", map[string]any{
					"tab":      argString(args, "tab"),
					"selector": el,
				})
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary("hover " + el), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "drag",
			Description: "Drag an element from source to destination by ref (e.g. 'e12') or CSS selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"from":    str("Source element ref or CSS selector."),
					"to":      str("Destination element ref or CSS selector."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "from", "to"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				from := argString(args, "from")
				to := argString(args, "to")
				data, err := daemon.Call(bName, "drag", map[string]any{
					"tab":  argString(args, "tab"),
					"from": from,
					"to":   to,
				})
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary(fmt.Sprintf("drag %s -> %s", from, to)), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "upload",
			Description: "Upload files to a file input element by ref or CSS selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"element": str("Ref from latest snapshot (e.g. 'e12') or CSS selector to file input."),
					"paths": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "List of local file paths to attach.",
					},
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "element", "paths"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				el := argString(args, "element")
				var paths []string
				if pList, ok := args["paths"].([]any); ok {
					for _, item := range pList {
						if s, ok := item.(string); ok {
							paths = append(paths, s)
						}
					}
				}
				data, err := daemon.Call(bName, "upload", map[string]any{
					"tab":      argString(args, "tab"),
					"selector": el,
					"paths":    paths,
				})
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary(fmt.Sprintf("uploaded %d file(s) to %s", len(paths), el)), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "dialog",
			Description: "Accept or dismiss a JavaScript dialog (alert, confirm, prompt, beforeunload).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":         str("Tab ID."),
					"action":      str("Dialog action: 'accept' or 'dismiss'."),
					"prompt_text": str("Optional prompt text when accepting a prompt() dialog."),
					"browser":     str("Browser name."),
				},
				"required": []string{"tab", "action"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				action := argString(args, "action")
				dialogArgs := map[string]any{
					"tab":    argString(args, "tab"),
					"action": action,
				}
				if t := argString(args, "prompt_text"); t != "" {
					dialogArgs["prompt_text"] = t
				}
				_, err = daemon.Call(bName, "dialog", dialogArgs)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("dialog %s -> handled", action), nil
			},
		},
		{
			Name:        "fill",
			Description: "Fill form elements (inputs, textareas, selects, checkboxes) by ref or selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"element": str("Ref (e.g. 'e12') or CSS selector."),
					"value":   str("Value to insert, select option text/value, or 'true'/'false' for checkboxes."),
					"wait":    boolean("Auto-wait for element actionability (visible, attached, enabled) before filling."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "element", "value"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				pairs := [][2]string{{argString(args, "element"), argString(args, "value")}}
				pairsJSON, _ := json.Marshal(pairs)
				wait, _ := args["wait"].(bool)
				data, err := daemon.Call(bName, "fill", map[string]any{
					"tab":   argString(args, "tab"),
					"pairs": string(pairsJSON),
					"wait":  wait,
				})
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary(fmt.Sprintf("filled %s", argString(args, "element"))), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "type",
			Description: "Type text into the currently focused element, with optional submit key.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"text":    str("Text to type."),
					"submit":  str("Optional submit key such as 'Enter'."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "text"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				data, err := daemon.Call(bName, "type", map[string]any{
					"tab":    argString(args, "tab"),
					"text":   argString(args, "text"),
					"submit": argString(args, "submit"),
				})
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary(fmt.Sprintf("typed %q", argString(args, "text"))), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "key",
			Description: "Press a key or keyboard shortcut (e.g. 'Enter', 'Tab', 'Escape', 'Control+A').",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"key":     str("Key name or combo."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "key"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				data, err := daemon.Call(bName, "key", map[string]any{
					"tab":  argString(args, "tab"),
					"keys": argString(args, "key"),
				})
				if err != nil {
					return "", err
				}
				var res daemon.ActResult
				if err := json.Unmarshal(data, &res); err == nil {
					return res.TerseSummary(fmt.Sprintf("pressed %s", argString(args, "key"))), nil
				}
				return string(data), nil
			},
		},
		{
			Name:        "eval",
			Description: "Evaluate a JavaScript expression on the tab.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"script":  str("JavaScript expression."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab", "script"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				data, err := daemon.Call(bName, "eval", map[string]any{
					"tab":    argString(args, "tab"),
					"script": argString(args, "script"),
				})
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "cdp",
			Description: "Send any raw DevTools Protocol (CDP) command to a tab or browser target.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Optional Tab ID (omit for browser-level methods)."),
					"method":  str("DevTools command method (e.g. 'Runtime.evaluate', 'Page.reload')."),
					"params":  str("JSON object string containing command parameters."),
					"browser": str("Browser name."),
				},
				"required": []string{"method"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				data, err := daemon.Call(bName, "cdp", map[string]any{
					"tab":    argString(args, "tab"),
					"method": argString(args, "method"),
					"params": argString(args, "params"),
				})
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "emulate",
			Description: "Emulate device metrics, network conditions, CPU throttling, geolocation, or color scheme.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab":     str("Tab ID."),
					"device":  str("Device emulation ('mobile', 'desktop', 'reset')."),
					"network": str("Network conditions ('offline', 'slow3g', 'online', 'reset')."),
					"cpu":     number("CPU throttling rate (e.g. 2 for 2x slowdown, 1 for reset)."),
					"geo":     str("Geolocation coordinates ('lat,lng' e.g. '37.7749,-122.4194' or 'reset')."),
					"media":   str("Color scheme preference ('dark', 'light', 'reset')."),
					"browser": str("Browser name."),
				},
				"required": []string{"tab"},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				callArgs := map[string]any{
					"tab": argString(args, "tab"),
				}
				if d := argString(args, "device"); d != "" {
					callArgs["device"] = d
				}
				if n := argString(args, "network"); n != "" {
					callArgs["network"] = n
				}
				if c, ok := args["cpu"].(float64); ok && c > 0 {
					callArgs["cpu"] = c
				}
				if g := argString(args, "geo"); g != "" {
					callArgs["geo"] = g
				}
				if m := argString(args, "media"); m != "" {
					callArgs["media"] = m
				}
				data, err := daemon.Call(bName, "emulate", callArgs)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "doctor",
			Description: "Self-check browsers, profiles, process status, debugging preference, and daemon state on this machine.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"browser": str("Optional browser name to inspect."),
				},
			},
			run: func(args map[string]any) (string, error) {
				return inspectDoctor(argString(args, "browser"))
			},
		},
		{
			Name:        "setup",
			Description: "Toggle the remote-debugging preference for a browser in Local State.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"browser": str("Browser name."),
					"off":     boolean("Disable remote debugging when true (revert)."),
				},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				b, ok := browser.Find(bName)
				if !ok {
					return "", exit.New(exit.NotFound, "browser %q not found", bName)
				}
				off, _ := args["off"].(bool)
				if off {
					if err := b.SetDebugging(false); err != nil {
						return "", err
					}
					return fmt.Sprintf("reverted: remote debugging disabled for %s", b.Label), nil
				}
				if b.Running() {
					return "", exit.New(exit.User, "quit %s first: it rewrites its settings on exit", b.Label)
				}
				if err := b.SetDebugging(true); err != nil {
					return "", err
				}
				return fmt.Sprintf("enabled remote debugging for %s in Local State", b.Label), nil
			},
		},
		{
			Name:        "launch",
			Description: "Start an isolated agent browser instance with remote debugging (CDP) enabled.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"browser":      str("Browser name."),
					"headless":     boolean("Run browser in headless mode (default: false)."),
					"url":          str("Initial URL to open (default: about:blank)."),
					"real_profile": boolean("Launch with real profile (Brave only; blocked on Chrome/Edge 136+)."),
				},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				headless, _ := args["headless"].(bool)
				realProfile, _ := args["real_profile"].(bool)
				targetURL := argString(args, "url")
				if targetURL == "" {
					targetURL = "about:blank"
				}
				data, err := daemon.Call(bName, "launch", map[string]any{
					"headless":     headless,
					"url":          targetURL,
					"real_profile": realProfile,
				})
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "recall",
			Description: "Episodic recall query over dated session notes and browser history. Read-only selective, relevance-ranked timeline with lazy pointers.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session": str("Session ID to recall notes from (default: 'default')."),
					"tab":     str("Filter history events to a specific tab ID."),
					"grep":    str("Keyword or action pattern to search and rank by."),
					"since":   str("ISO timestamp or Go duration string (e.g. '1h', '24h') to filter since."),
					"limit":   number("Max items to return (default: 20)."),
					"browser": str("Browser name (default: active/first)."),
				},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				session := argString(args, "session")
				if session == "" {
					session = "default"
				}
				tab := argString(args, "tab")
				grep := argString(args, "grep")
				since := argString(args, "since")
				limit := 20
				if l, ok := args["limit"].(float64); ok && l > 0 {
					limit = int(l)
				}
				data, err := daemon.Call(bName, "recall", map[string]any{
					"session": session,
					"tab":     tab,
					"grep":    grep,
					"since":   since,
					"limit":   limit,
					"browser": bName,
				})
				if err != nil {
					return "", err
				}
				var res daemon.RecallResult
				if err := json.Unmarshal(data, &res); err != nil {
					return string(data), nil
				}
				return res.Text(), nil
			},
		},
		{
			Name:        "done",
			Description: "Close out an agent session by distilling notes into a durable ~5-line takeaway persisted into the session narrative.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session":  str("Session ID to close out (default: 'default')."),
					"takeaway": str("Distilled takeaway summary (optional; auto-synthesised if omitted)."),
					"browser":  str("Browser name (default: active/first)."),
				},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				session := argString(args, "session")
				if session == "" {
					session = "default"
				}
				takeaway := argString(args, "takeaway")
				data, err := daemon.Call(bName, "done", map[string]any{
					"session":  session,
					"takeaway": takeaway,
				})
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},
		{
			Name:        "list_profiles",
			Description: "List all user profiles found for an installed browser.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"browser": str("Browser name."),
				},
			},
			run: func(args map[string]any) (string, error) {
				bName, err := resolveBrowser(argString(args, "browser"))
				if err != nil {
					return "", err
				}
				b, ok := browser.Find(bName)
				if !ok {
					return "", exit.New(exit.NotFound, "browser %q not found", bName)
				}
				ps, err := browser.Profiles(b)
				if err != nil {
					return "", exit.New(exit.Upstream, "%v", err)
				}
				data, _ := json.Marshal(ps)
				return string(data), nil
			},
		},
	}
}
