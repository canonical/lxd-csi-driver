package testutils

import (
	"crypto/rand"
	"strings"
	"time"
)

// GenerateStringN returns a random alphanumeric string of the given length.
func GenerateStringN(length int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"

	b := make([]byte, 5)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}

	return string(b)
}

// GenerateName returns a unique name composed of the given prefix, the current
// timestamp (YYYYMMDD-hhmmss), and a 5-character random suffix.
// Example: For input "pvc" the result is "pvc-20250818-153045-a1b2c".
func GenerateName(prefix string) string {
	if prefix != "" && !strings.HasSuffix(prefix, "-") {
		prefix += "-"
	}

	suffix := GenerateStringN(5)
	ts := time.Now().Format("20060102-150405")

	return prefix + ts + "-" + suffix
}
