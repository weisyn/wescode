/**
 * Whether `p` is an absolute filesystem path.
 *
 * `p.startsWith('/')` is POSIX-only: `F:\proj\x.go`, `F:/proj/x.go` and
 * `\\server\share` all answer false, so on Windows a drop of a real file was
 * filtered out and the gesture did nothing — no attachment, no error. The Go
 * backend stores slash-normalized paths, so the drive-letter form with forward
 * slashes is the common case here, not an edge one.
 *
 * Mirrors `editor/src/vs/workbench/contrib/wescode/common/wescodePath.ts`; the
 * two cannot share a module (separate Vite app vs. VS Code source tree).
 */
export function isAbsoluteFilePath(p: string): boolean {
  if (!p) return false
  if (p.startsWith('/')) return true
  if (p.startsWith('\\\\') || p.startsWith('//')) return true
  return /^[a-zA-Z]:[\\/]/.test(p)
}

/**
 * Last path segment, for either separator.
 *
 * `p.split('/').pop()` is the reflex, and on a path from the host's file picker
 * (`uri.fsPath`, backslashes on Windows) it returns the whole path — so an
 * error message meant to name `main.go` names `F:\proj\internal\main.go`.
 * Paths reaching this webview come from two sources with different separators:
 * drops carry forward slashes, the picker carries native ones.
 */
export function fileBasename(p: string): string {
  const parts = p.split(/[\\/]/)
  return parts[parts.length - 1] || p
}

/**
 * Normalizes a path extracted from a `file://` URI.
 *
 * Stripping the scheme off `file:///F:/proj/x.go` leaves `/F:/proj/x.go`, which
 * passes any absolute-path test while naming nothing — the leading slash belongs
 * to the URI, not the path.
 */
export function filePathFromUriString(uri: string): string {
  const raw = uri.startsWith('file://')
    ? decodeURIComponent(uri.replace(/^file:\/\//, ''))
    : uri
  return /^\/[a-zA-Z]:[\\/]/.test(raw) ? raw.slice(1) : raw
}
