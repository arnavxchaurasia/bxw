package perms

import (
	"testing"
	"time"
)

func TestPermissionsLifecycle(t *testing.T) {
	s := Default()
	testOrigin := "https://example.org"

	// 1. Free action should be allowed
	allowed, state := s.Check(testOrigin, "navigate")
	if !allowed || state != StateGranted {
		t.Errorf("free action should be allowed, got %v (%s)", allowed, state)
	}

	// 2. Gated action should initially prompt
	allowed, state = s.Check(testOrigin, CapFormSubmit)
	if allowed || state != StatePrompt {
		t.Errorf("gated action without grant should prompt, got %v (%s)", allowed, state)
	}

	// 3. Grant session capability
	if err := s.Grant(testOrigin, CapFormSubmit, ScopeSession, 10*time.Minute); err != nil {
		t.Fatalf("Grant failed: %v", err)
	}

	allowed, state = s.Check(testOrigin, CapFormSubmit)
	if !allowed || state != StateGranted {
		t.Errorf("granted capability should be allowed, got %v (%s)", allowed, state)
	}

	// 4. Revoke capability
	if err := s.Revoke(testOrigin, CapFormSubmit); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	allowed, state = s.Check(testOrigin, CapFormSubmit)
	if allowed || state != StatePrompt {
		t.Errorf("revoked capability should prompt, got %v (%s)", allowed, state)
	}

	// 5. Always grant never expires
	if err := s.Grant(testOrigin, CapPurchase, ScopeAlways, 10*time.Minute); err != nil {
		t.Fatalf("Grant always failed: %v", err)
	}
	defer s.Revoke(testOrigin, CapPurchase)

	records := s.List()
	var found *PermissionRecord
	for _, r := range records {
		if r.Origin == testOrigin && r.Capability == CapPurchase {
			found = &r
			break
		}
	}
	if found == nil {
		t.Fatalf("expected always grant record in list")
	}
	if !found.ExpiresAt.IsZero() {
		t.Errorf("expected zero ExpiresAt for always grant, got %v", found.ExpiresAt)
	}
}
