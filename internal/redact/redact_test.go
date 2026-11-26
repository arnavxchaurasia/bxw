package redact

import (
	"strings"
	"testing"
)

func TestS1TableRedaction(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "GitHub personal access token (classic)",
			input:    "Found key " + "gh" + "p_111122223333444455556666777788889999 in config",
			expected: "Found key [REDACTED] in config",
		},
		{
			name:     "GitHub PAT (fine-grained)",
			input:    "Token " + "github" + "_pat_11AABBCCDDEEFFGGHHIIJJ_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef01 ready",
			expected: "Token [REDACTED] ready",
		},
		{
			name:     "AWS Access Key ID",
			input:    "Connecting with AWS_ACCESS_KEY_ID=" + "AK" + "IAIOSFODNN7EXAMPLE to us-east-1",
			expected: "Connecting with AWS_ACCESS_KEY_ID=[REDACTED] to us-east-1",
		},
		{
			name:     "Slack token",
			input:    "slack_token: " + "xo" + "xb-123456789012-1234567890123-456789abcdef0123456789ab",
			expected: "slack_token: [REDACTED]",
		},
		{
			name:     "Google API Key",
			input:    "key=AIzaSyD-1234567890abcdefghijklmnopqrstuvw",
			expected: "key=[REDACTED]",
		},
		{
			name:     "Bearer token header",
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			expected: "Authorization: Bearer [REDACTED]",
		},
		{
			name:     "JWT string standalone",
			input:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.4pdODQn_c824mG5_7-1e5K7yvP6K6fW0vQ8",
			expected: "[REDACTED]",
		},
		{
			name:     "PEM private key block",
			input:    "Cert:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Yv...\n-----END RSA PRIVATE KEY-----\nEnd cert",
			expected: "Cert:\n-----BEGIN PRIVATE KEY-----\n[REDACTED]\n-----END PRIVATE KEY-----\nEnd cert",
		},
		{
			name:     "URL Basic Auth",
			input:    "Clone from https://alice:SuperSecretPassword123@github.com/org/repo.git",
			expected: "Clone from https://alice:[REDACTED]@github.com/org/repo.git",
		},
		{
			name:     "Sensitive parameters",
			input:    "login with password=MySecretP@ssw0rd! and user=alice",
			expected: "login with password=[REDACTED] and user=alice",
		},
		{
			name:     "Benign text untouched",
			input:    "Hello world, this is a normal test string with standard words and punctuation.",
			expected: "Hello world, this is a normal test string with standard words and punctuation.",
		},
		{
			name:     "Benign URL untouched",
			input:    "Navigate to https://example.com/search?q=golang+testing&page=2",
			expected: "Navigate to https://example.com/search?q=golang+testing&page=2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.expected {
				t.Errorf("mismatch for %s\nGot:      %q\nExpected: %q", tc.name, got, tc.expected)
			}
		})
	}
}

func TestS1RedactJSON(t *testing.T) {
	sampleToken := "gh" + "p_111122223333444455556666777788889999"
	inputJSON := []byte(`{
		"user": "alice",
		"password": "plaintext-password-123",
		"details": {
			"api_key": "custom-secret-key",
			"notes": "normal note"
		},
		"tokens": ["` + sampleToken + `", "benign-item"],
		"nodes": [
			{
				"ref": "e1",
				"role": "password",
				"name": "Password",
				"value": "MySuperSecret123"
			},
			{
				"ref": "e2",
				"role": "textbox",
				"props": ["password"],
				"value": "AnotherPassword456"
			}
		],
		"storage": {
			"cookies": [
				{"name": "session_id", "value": "secret-session-token"}
			],
			"local": {
				"jwt": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.4pdODQn_c824mG5_7-1e5K7yvP6K6fW0vQ8"
			}
		}
	}`)

	redacted := RedactJSON(inputJSON)
	s := string(redacted)

	if strings.Contains(s, "plaintext-password-123") {
		t.Errorf("password was not masked in JSON: %s", s)
	}
	if strings.Contains(s, "custom-secret-key") {
		t.Errorf("api_key was not masked in JSON: %s", s)
	}
	if strings.Contains(s, sampleToken) {
		t.Errorf("ghp token was not masked in JSON: %s", s)
	}
	if strings.Contains(s, "MySuperSecret123") {
		t.Errorf("password node value was not masked: %s", s)
	}
	if strings.Contains(s, "AnotherPassword456") {
		t.Errorf("password prop node value was not masked: %s", s)
	}
	if strings.Contains(s, "secret-session-token") {
		t.Errorf("session_id cookie value was not masked: %s", s)
	}
	if strings.Contains(s, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Errorf("jwt in storage was not masked: %s", s)
	}
	if !strings.Contains(s, "alice") || !strings.Contains(s, "normal note") || !strings.Contains(s, "benign-item") {
		t.Errorf("benign fields corrupted in JSON: %s", s)
	}
}
