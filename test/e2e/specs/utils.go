package specs

import (
	"crypto/rand"
	"time"
)

// prettyName returns a string consisting of resource's namespace and name.
// If the namespace is empty, it returns only the name.
func prettyName(namespace string, name string) string {
	if namespace == "" {
		return name
	}

	return namespace + "/" + name
}

// generateName returns a unique name composed of the given prefix, the current
// timestamp (YYYYMMDD-hhmmss), and a 5-character random suffix.
// Example: For input "pvc" the result is "pvc-20250818-153045-a1b2c".
func generateName(prefix string) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"

	b := make([]byte, 5)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}

	ts := time.Now().Format("20060102-150405")
	return prefix + "-" + ts + "-" + string(b)
}
