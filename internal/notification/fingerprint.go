package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var multiSpace = regexp.MustCompile(`\s+`)

// alertFingerprint builds a stable SHA256 fingerprint for deduplication.
// alertType is one of: "log", "metric", "event", "watchdog"
func alertFingerprint(subscriptionName, hostName, containerName, alertType, rawMessage string) string {
	msg := normalizeMessage(rawMessage)
	parts := strings.Join([]string{subscriptionName, hostName, containerName, alertType, msg}, "\x00")
	sum := sha256.Sum256([]byte(parts))
	return hex.EncodeToString(sum[:])
}

func normalizeMessage(s string) string {
	s = multiSpace.ReplaceAllString(strings.TrimSpace(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
