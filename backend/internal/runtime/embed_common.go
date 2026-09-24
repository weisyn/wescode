// Package runtime owns the embedded Python runtime assets for wescode.
//
// Platform-selective embedding: each supported platform has its own embed file
// constrained by build tags. The Go compiler selects the correct file based on
// the target platform, embedding only that platform's Python tarball.
// Unsupported platforms fall back to embed_other.go (.gitkeep) so the
// provisioner degrades to fallback download or system python3.
//
// python_assets/ is a symlink to wesclaw's python_assets/ to avoid duplicating
// ~700MB of tarballs. All products share the same CPython build + preinstalled
// packages; product-specific Python tool needs (pyright, pytest, etc.) are
// system-PATH tools, not bundled packages.
package runtime

import "io/fs"

// PythonFS returns the embedded Python assets FS for the current platform.
func PythonFS() fs.FS { return pythonAssetsFS }
