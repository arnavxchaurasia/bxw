package redact

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
)

// Documented patterns for credential formats, tokens, and keys.
var (
	// GitHub personal access token (ghp_), fine-grained (github_pat_), OAuth (gho_), app (ghu_, ghs_, ghr_)
	reGitHubToken = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,255}\b|github_pat_[A-Za-z0-9_]{22}_[A-Za-z0-9_]{59}\b`)

	// AWS Access Key ID (AKIA..., ASIA...)
	reAWSAccessKey = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)

	// Slack token (xox[baprs]-...)
	reSlackToken = regexp.MustCompile(`\bxox[baprs]-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*\b`)

	// Google API key (AIza...)
	reGoogleAPIKey = regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35,40}\b`)

	// Bearer token in headers or text
	reBearer = regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9\-_=]{16,})\b`)

	// JSON Web Token (JWT) pattern: header.payload.signature (each base64url encoded)
	reJWT = regexp.MustCompile(`\beyJ[A-Za-z0-9\-_]{10,}\.eyJ[A-Za-z0-9\-_]{10,}\.[A-Za-z0-9\-_]{10,}\b`)

	// PEM private key blocks
	rePEM = regexp.MustCompile(`(?s)-----BEGIN[ A-Z0-9_-]+PRIVATE KEY-----.*?-----END[ A-Z0-9_-]+PRIVATE KEY-----`)

	// Password field / sensitive parameter assignment: password=..., pwd=..., secret=..., api_key=...
	reParamSecret = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|api_?key|auth_?token|access_?token|client_?secret|private_?key)\s*[:=]\s*([^\s,;'"&>]+)`)

	// Basic auth in URL: http(s)://user:password@host
	reBasicAuth = regexp.MustCompile(`(https?://[^:]+:)([^@]+)(@)`)
)

const maskReplacement = "[REDACTED]"

// ShannonEntropy calculates the Shannon entropy (in bits per symbol) of a string.
func ShannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	counts := make(map[rune]float64)
	for _, r := range s {
		counts[r]++
	}
	total := float64(len(s))
	entropy := 0.0
	for _, count := range counts {
		p := count / total
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// isHighEntropyCandidate checks whether a standalone token has high randomness/entropy.
// Tokens of length >= 32 with entropy >= 4.5 bits/char are classified as high-entropy secrets.
func isHighEntropyCandidate(s string) bool {
	if len(s) < 32 || len(s) > 256 {
		return false
	}
	// Avoid false-positives on paths, URLs, or hyphenated words
	if strings.Contains(s, "/") || strings.Contains(s, "\\") || strings.Contains(s, " ") || strings.Contains(s, "=") {
		return false
	}
	return ShannonEntropy(s) >= 4.5
}

// Redact applies the documented secret and PII pattern set to an arbitrary string.
// It masks passwords, API tokens, JWTs, PEM blocks, and high-entropy secret candidates.
func Redact(text string) string {
	if text == "" {
		return ""
	}

	// 1. PEM blocks
	text = rePEM.ReplaceAllString(text, "-----BEGIN PRIVATE KEY-----\n"+maskReplacement+"\n-----END PRIVATE KEY-----")

	// 2. URL basic auth
	text = reBasicAuth.ReplaceAllString(text, "${1}"+maskReplacement+"${3}")

	// 3. Known format tokens
	text = reGitHubToken.ReplaceAllString(text, maskReplacement)
	text = reAWSAccessKey.ReplaceAllString(text, maskReplacement)
	text = reSlackToken.ReplaceAllString(text, maskReplacement)
	text = reGoogleAPIKey.ReplaceAllString(text, maskReplacement)
	text = reJWT.ReplaceAllString(text, maskReplacement)

	// 4. Bearer tokens
	text = reBearer.ReplaceAllString(text, "Bearer "+maskReplacement)

	// 5. Named sensitive parameters
	text = reParamSecret.ReplaceAllString(text, "${1}="+maskReplacement)

	// 6. High-entropy standalone word pass
	words := strings.Fields(text)
	for _, w := range words {
		if strings.Contains(w, "=") {
			parts := strings.SplitN(w, "=", 2)
			val := strings.Trim(parts[1], `"'(),;:<>[]{}!=`)
			if isHighEntropyCandidate(val) {
				text = strings.ReplaceAll(text, val, maskReplacement)
			}
			continue
		}
		clean := strings.Trim(w, `"'(),;:<>[]{}!=`)
		if isHighEntropyCandidate(clean) {
			text = strings.ReplaceAll(text, clean, maskReplacement)
		}
	}

	return text
}

// RedactJSON traverses structured JSON and redacts known sensitive keys (password, token, etc.)
// as well as running the Redact string pass over all string values.
func RedactJSON(data []byte) []byte {
	var val any
	if err := json.Unmarshal(data, &val); err != nil {
		// Fall back to pure string redaction
		return []byte(Redact(string(data)))
	}
	redactedVal := redactValue(val)
	out, err := json.Marshal(redactedVal)
	if err != nil {
		return []byte(Redact(string(data)))
	}
	return out
}

func redactValue(v any) any {
	switch val := v.(type) {
	case string:
		return Redact(val)
	case map[string]any:
		res := make(map[string]any, len(val))

		// Check if this map represents a password element/node or sensitive cookie
		isSecretNode := false
		if r, ok := val["role"].(string); ok && strings.EqualFold(r, "password") {
			isSecretNode = true
		}
		if t, ok := val["type"].(string); ok && strings.EqualFold(t, "password") {
			isSecretNode = true
		}
		if props, ok := val["props"].([]any); ok {
			for _, p := range props {
				if ps, ok := p.(string); ok && strings.EqualFold(ps, "password") {
					isSecretNode = true
					break
				}
			}
		}
		if name, ok := val["name"].(string); ok {
			nLower := strings.ToLower(name)
			if nLower == "password" || strings.Contains(nLower, "password") {
				isSecretNode = true
			} else if isSensitiveKey(nLower) {
				if _, hasVal := val["value"]; hasVal {
					isSecretNode = true
				}
			}
		}

		for k, child := range val {
			kLower := strings.ToLower(k)
			switch child.(type) {
			case map[string]any, []any:
				res[k] = redactValue(child)
			default:
				if isSensitiveKey(kLower) {
					res[k] = maskReplacement
				} else if isSecretNode && (k == "value" || k == "text") {
					res[k] = maskReplacement
				} else {
					res[k] = redactValue(child)
				}
			}
		}
		return res
	case []any:
		res := make([]any, len(val))
		for i, child := range val {
			res[i] = redactValue(child)
		}
		return res
	default:
		return v
	}
}

func isSensitiveKey(k string) bool {
	switch k {
	case "password", "passwd", "pwd", "secret", "token", "auth", "authorization",
		"apikey", "api_key", "accesstoken", "access_token", "refreshtoken",
		"refresh_token", "privatekey", "private_key", "clientsecret", "client_secret",
		"jwt", "session", "sessionid", "session_id", "sid", "cookie", "credentials":
		return true
	default:
		return strings.Contains(k, "password") || strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.Contains(k, "auth") || strings.Contains(k, "api_key")
	}
}
