package flexvault

import (
	"fmt"
	"strings"
)

// ParseAddress validates and normalizes a secret address. An address is
// exactly two non-empty, `/`-separated, case-sensitive segments —
// "namespace/key" — with surrounding whitespace trimmed. Anything else is an
// error.
func ParseAddress(addr string) (namespace, key string, err error) {
	trimmed := strings.TrimSpace(addr)
	namespace, key, found := strings.Cut(trimmed, "/")
	if !found || namespace == "" || key == "" || strings.Contains(key, "/") {
		return "", "", fmt.Errorf("flexvault: invalid secret address %q (want namespace/key)", addr)
	}
	return namespace, key, nil
}
