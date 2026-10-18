package translate

import (
	"crypto/rand"
	"fmt"
	"strings"
)

func responseID(id, prefix string) string {
	if id == "" {
		return prefix + rand.Text()
	}
	return id
}

// responseCallIDs assigns missing identities once per buffered response and
// rejects collisions rather than making existing call references ambiguous.
type responseCallIDs map[string]bool

func (ids responseCallIDs) resolve(id, prefix string) (string, error) {
	if strings.TrimSpace(id) == "" {
		id = "call_" + rand.Text()
	}
	if prefix != "" && !strings.HasPrefix(id, prefix) {
		id = prefix + id
	}
	if ids[id] {
		return "", fmt.Errorf("upstream returned duplicate tool call id %q", id)
	}
	ids[id] = true
	return id, nil
}
