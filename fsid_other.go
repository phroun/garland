//go:build !unix

package garland

import "os"

// localFileIdentity returns "" on platforms without a cheap stable
// file-identity concept (Windows, wasm, plan9). Identity-based
// replacement detection is disabled there; callers already skip empty
// identities, so change detection falls back to size + mtime.
func localFileIdentity(info os.FileInfo) string {
	return ""
}
