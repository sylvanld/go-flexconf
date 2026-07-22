package flexconf

import "fmt"

// resolveSecret resolves a $(secret:[vault:]namespace/key) token. The full
// vault-backed implementation lands with the secret-resolver feature; until
// then the scheme is declared but unavailable.
func (l *Loader) resolveSecret(path string) (string, error) {
	return "", fmt.Errorf("secret: resolver not yet available (path %q)", path)
}
