package utils

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// ParseUnixSocketURL parses a unix socket endpoint URL and returns the parsed
// URL and resolved socket path.
func ParseUnixSocketURL(endpoint string) (*url.URL, string, error) {
	url, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to parse endpoint %q: %v", endpoint, err)
	}

	if url.Scheme != "unix" {
		return nil, "", fmt.Errorf("Invalid endpoint %q: Unsupported scheme %q: Only unix sockets are supported", endpoint, url.Scheme)
	}

	socketPath := filepath.FromSlash(url.Path)
	if url.Host != "" {
		socketPath = filepath.Join(url.Host, socketPath)
	}

	if !strings.HasPrefix(socketPath, "/") {
		socketPath = "/" + socketPath
	}

	if socketPath == "" || strings.HasSuffix(socketPath, "/") {
		return nil, "", fmt.Errorf("Invalid endpoint %q: Socket path cannot be empty or point to a directory", endpoint)
	}

	return url, socketPath, nil
}
