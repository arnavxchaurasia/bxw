package perms

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bxw/internal/osport"
)

// Capability constants mapping to risk tiers.
const (
	CapFormSubmit       = "form_submit"
	CapPurchase         = "purchase"
	CapDelete           = "delete"
	CapDownload         = "download"
	CapEnterCredentials = "enter_credentials"
	CapCrossOriginNav   = "cross_origin_nav"
)

// State constants.
const (
	StateGranted = "granted"
	StateDenied  = "denied"
	StatePrompt  = "prompt"
)

// Scope constants.
const (
	ScopeOnce    = "once"
	ScopeSession = "session"
	ScopeAlways  = "always"
)

// PermissionRecord defines a capability grant for an origin.
type PermissionRecord struct {
	Origin     string    `json:"origin"`
	Capability string    `json:"capability"`
	State      string    `json:"state"`
	Scope      string    `json:"scope"`
	GrantedAt  time.Time `json:"granted_at"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

// Store manages durable and in-memory session capability grants.
type Store struct {
	mu      sync.RWMutex
	session map[string]PermissionRecord // key: origin|capability
}

var (
	defaultStore *Store
	once         sync.Once
)

// Default returns the singleton permissions store.
func Default() *Store {
	once.Do(func() {
		defaultStore = &Store{
			session: make(map[string]PermissionRecord),
		}
	})
	return defaultStore
}

// NormalizeOrigin extracts scheme://host[:port] from a raw URL.
func NormalizeOrigin(rawURL string) string {
	if rawURL == "" || rawURL == "about:blank" {
		return "about:blank"
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return rawURL
	}
	return u.Scheme + "://" + u.Host
}

// IsGated reports whether a capability is gated by risk policies.
func IsGated(capability string) bool {
	switch strings.ToLower(capability) {
	case CapFormSubmit, CapPurchase, CapDelete, CapDownload, CapEnterCredentials, CapCrossOriginNav:
		return true
	default:
		return false
	}
}

// Check evaluates whether a capability is granted for an origin.
func (s *Store) Check(rawURL, capability string) (allowed bool, state string) {
	if !IsGated(capability) {
		return true, StateGranted
	}

	origin := NormalizeOrigin(rawURL)
	key := origin + "|" + capability

	s.mu.RLock()
	rec, ok := s.session[key]
	s.mu.RUnlock()

	now := time.Now()
	if ok {
		if rec.ExpiresAt.IsZero() || rec.ExpiresAt.After(now) {
			return rec.State == StateGranted, rec.State
		}
	}

	// Consult durable store
	records, err := s.loadDurable()
	if err != nil {
		return false, StatePrompt
	}

	for _, r := range records {
		if (r.Origin == origin || r.Origin == "*") && (r.Capability == capability || r.Capability == "*") {
			if r.ExpiresAt.IsZero() || r.ExpiresAt.After(now) {
				return r.State == StateGranted, r.State
			}
		}
	}

	return false, StatePrompt
}

// Grant grants a capability for an origin with a scope and TTL.
func (s *Store) Grant(rawURL, capability, scope string, ttl time.Duration) error {
	origin := NormalizeOrigin(rawURL)
	now := time.Now()
	var expiresAt time.Time
	// For "always" scope, permissions never expire. Keep TTL only for session/once.
	if scope != ScopeAlways && ttl > 0 {
		expiresAt = now.Add(ttl)
	}

	rec := PermissionRecord{
		Origin:     origin,
		Capability: capability,
		State:      StateGranted,
		Scope:      scope,
		GrantedAt:  now,
		ExpiresAt:  expiresAt,
	}

	if scope == ScopeSession || scope == ScopeOnce {
		s.mu.Lock()
		s.session[origin+"|"+capability] = rec
		s.mu.Unlock()
		return nil
	}

	// Durable "always"
	return s.saveDurable(rec)
}

// Revoke revokes a permission grant for an origin.
func (s *Store) Revoke(rawURL, capability string) error {
	origin := NormalizeOrigin(rawURL)
	s.mu.Lock()
	if capability == "" || capability == "*" {
		for k := range s.session {
			if strings.HasPrefix(k, origin+"|") {
				delete(s.session, k)
			}
		}
	} else {
		delete(s.session, origin+"|"+capability)
	}
	s.mu.Unlock()

	records, err := s.loadDurable()
	if err != nil {
		return err
	}

	var filtered []PermissionRecord
	for _, r := range records {
		if r.Origin == origin {
			if capability != "" && capability != "*" && r.Capability != capability {
				filtered = append(filtered, r)
			}
			continue
		}
		filtered = append(filtered, r)
	}

	return s.writeDurable(filtered)
}

// List returns all currently active permissions (durable + session).
func (s *Store) List() []PermissionRecord {
	s.mu.RLock()
	var all []PermissionRecord
	for _, r := range s.session {
		all = append(all, r)
	}
	s.mu.RUnlock()

	durable, err := s.loadDurable()
	if err == nil {
		all = append(all, durable...)
	}
	return all
}

func (s *Store) loadDurable() ([]PermissionRecord, error) {
	p := osport.Current()
	f := p.PermissionsFile()
	data, err := os.ReadFile(f)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var records []PermissionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Store) writeDurable(records []PermissionRecord) error {
	p := osport.Current()
	f := p.PermissionsFile()
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f, data, 0o644)
}

func (s *Store) saveDurable(rec PermissionRecord) error {
	records, _ := s.loadDurable()
	updated := false
	for i, r := range records {
		if r.Origin == rec.Origin && r.Capability == rec.Capability {
			records[i] = rec
			updated = true
			break
		}
	}
	if !updated {
		records = append(records, rec)
	}
	return s.writeDurable(records)
}
