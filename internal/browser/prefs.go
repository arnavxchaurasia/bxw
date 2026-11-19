package browser

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"bxw/internal/exit"
	"bxw/internal/osport"
)

// The Local State setting that the inspect page's "Allow remote debugging" switch stores.
var debugPref = []string{"devtools", "remote_debugging", "user-enabled"}

// Running reports whether any process with this browser's executable name is currently running.
func (b Browser) Running() bool {
	p := osport.Current()
	return p.ProcessRunning(filepath.Base(b.Exe))
}

// DebuggingEnabled reports whether the remote-debugging switch is on in Local State.
func (b Browser) DebuggingEnabled() (bool, error) {
	state, err := b.readLocalState()
	if err != nil {
		return false, err
	}
	v, _ := lookupPath(state, debugPref).(bool)
	return v, nil
}

// SetDebugging turns the remote-debugging switch on or off by editing Local State.
// The browser rewrites Local State when it quits, so it must not be running.
func (b Browser) SetDebugging(on bool) error {
	if b.Running() {
		return exit.New(exit.User, "quit %s first: it rewrites its settings on exit", b.Label)
	}

	p := osport.Current()
	path := p.LocalStatePath(b.DataDir)
	orig, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return exit.New(exit.User, "%s has no settings yet: launch it once, then close it", b.Label)
		}
		return exit.New(exit.Upstream, "read %s settings: %v", b.Label, err)
	}

	// Backup before first modification
	bak := path + ".bxw.bak"
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		_ = os.WriteFile(bak, orig, 0o644)
	}

	state, err := decodeState(orig)
	if err != nil {
		return err
	}

	setPath(state, debugPref, on)

	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return exit.New(exit.Internal, "encode Local State: %v", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return exit.New(exit.User, "write Local State: %v", err)
	}

	return os.Rename(tmp, path)
}

func (b Browser) readLocalState() (map[string]any, error) {
	p := osport.Current()
	path := p.LocalStatePath(b.DataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, exit.New(exit.NotFound, "%s has no Local State yet", b.Label)
		}
		return nil, exit.New(exit.Upstream, "read Local State for %s: %v", b.Label, err)
	}
	return decodeState(data)
}

// decodeState decodes JSON preserving numbers accurately.
func decodeState(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, exit.New(exit.Upstream, "parse Local State: %v", err)
	}
	return m, nil
}

func lookupPath(m map[string]any, path []string) any {
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[k]
	}
	return cur
}

func setPath(m map[string]any, path []string, v any) {
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}
