// Package logging provides shared structured logging helpers.
package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
)

const redactedValue = "[REDACTED]"

// Nop returns a logger that discards all records.
func Nop() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// RedactingHandlerOptions returns slog handler options with redaction enabled.
func RedactingHandlerOptions(level slog.Leveler) *slog.HandlerOptions {
	return &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: RedactAttr,
	}
}

// RedactAttr replaces sensitive attribute values with a fixed marker.
func RedactAttr(_ []string, attr slog.Attr) slog.Attr {
	if IsSensitiveKey(attr.Key) {
		return slog.String(attr.Key, redactedValue)
	}

	return attr
}

// IsSensitiveKey reports whether key should be redacted before log emission.
func IsSensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false
	}
	if isSensitiveExactKey(normalized) {
		return true
	}
	for _, fragment := range []string{
		"authorization",
		"bearer",
		"credential",
		"password",
		"privatekey",
		"secret",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}

	return false
}

func isSensitiveExactKey(key string) bool {
	switch key {
	case "accesskey",
		"accesskeyid",
		"apikey",
		"apitoken",
		"bearertoken",
		"bootstraptoken",
		"dsn",
		"plaintext",
		"plaintexttoken",
		"postgresurl",
		"sessiontoken",
		"token",
		"tokenhash",
		"tokensha256",
		"urlpassword",
		"secretaccesskey":
		return true
	default:
		return false
	}
}

// SubjectHash returns a stable, non-reversible identifier for an external subject.
func SubjectHash(provider string, subject string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + subject))

	return hex.EncodeToString(sum[:])
}

func normalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")

	return replacer.Replace(key)
}
