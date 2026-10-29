package checker

import (
	"fmt"
	"strings"
)

// dockerNPipePath converts Docker's npipe:// URL form into a Windows pipe path.
func dockerNPipePath(host string) (string, error) {
	raw := strings.TrimSpace(host)
	if !strings.HasPrefix(raw, "npipe://") {
		return "", fmt.Errorf("invalid npipe Docker endpoint %q", host)
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "npipe://"))
	switch {
	case strings.HasPrefix(raw, `\\.\`):
		// Already a Windows path.
	case strings.HasPrefix(raw, "//./"):
		raw = strings.TrimPrefix(raw, "//./")
		raw = `\\.\` + raw
	case strings.HasPrefix(raw, "./"):
		raw = strings.TrimPrefix(raw, "./")
		raw = `\\.\` + raw
	default:
		return "", fmt.Errorf("invalid npipe Docker endpoint %q", host)
	}
	raw = strings.ReplaceAll(raw, "/", `\`)
	if !strings.HasPrefix(strings.ToLower(raw), `\\.\pipe\`) {
		return "", fmt.Errorf("invalid npipe Docker endpoint %q", host)
	}
	return raw, nil
}
