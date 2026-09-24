package codeintel

import "path/filepath"

// IndexPath is the one representation `file_path` and `package_path` ever take
// inside the index: forward slashes, on every platform.
//
// Native separators cannot be stored, because every package-path predicate is a
// literal (`LIKE '%/pkg/%'`, `NOT LIKE '%/e2e/%'`, `ClearProject`'s prefix).
// On Windows those matched nothing, and the column had two contradictory
// readers on top of that: `sqlRootPrefix` built its prefix with
// filepath.Separator while the package predicates assumed slashes, so no single
// storage form could satisfy both. What broke, concretely: cross-package
// resolution fell through to bare-name matching (which INV-CKG-EDGE-01 forbids,
// since it either mis-binds homonyms or loses the ambiguous group — see
// TestPass4_CrossPackageAmbiguousGroupRecall), the e2e/bench exclusions stopped
// excluding, and ClearProject deleted nothing while reporting success.
// schemaVersion 6 rebuilds existing indexes for this reason: a backslash row
// and a slash row naming one file are two different rows to SQL.
//
// ToSlash is last, and that ordering is load-bearing: filepath.Clean and
// filepath.EvalSymlinks both rewrite forward slashes back to `\` on Windows, so
// normalizing before either one silently undoes the whole thing.
//
// Reading back needs no inverse. os.Stat / os.Open / os.ReadFile accept forward
// slashes on Windows, so a stored path can be handed to the OS as-is; only
// filepath helpers (Clean, Dir, Join, Rel, EvalSymlinks) re-nativize, which is
// why paths derived with those must be passed through here again.
// Exported because the RPC layer shares the contract: it builds LIKE prefixes
// over file_path and compares request params against stored values, so it has
// to speak the same representation or it queries a spelling that is not there.
func IndexPath(p string) string {
	if p == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(p))
}
