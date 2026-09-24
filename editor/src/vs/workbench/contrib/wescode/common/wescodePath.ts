/*---------------------------------------------------------------------------------------------
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

/**
 * Whether `p` is an absolute filesystem path.
 *
 * The obvious test — `p.startsWith('/')` — is POSIX-only, and every Windows
 * absolute path fails it: `F:\proj\x.go`, `F:/proj/x.go` and `\\server\share`
 * all answer false. Callers then treat an absolute path as workspace-relative,
 * and none of them report that: the file link resolves against each workspace
 * folder, every stat misses, and the user gets "file not found" for a file that
 * is right there. The Go backend stores slash-normalized paths, so the
 * drive-letter form with forward slashes is the common case, not an edge one.
 */
export function isAbsoluteFilePath(p: string): boolean {
	if (!p) {
		return false;
	}
	if (p.startsWith('/')) {
		return true;
	}
	// UNC: \\server\share or //server/share
	if (p.startsWith('\\\\') || p.startsWith('//')) {
		return true;
	}
	// Drive letter, either separator: C:\... or C:/...
	return /^[a-zA-Z]:[\\/]/.test(p);
}

/**
 * Last path segment, for either separator.
 *
 * `p.split('/').pop()` is the reflex, and on a Windows fsPath it returns the
 * whole path — so a label meant to read `main.go` renders as
 * `F:\proj\internal\main.go` instead. Silent in the sense that matters: the
 * string is still a correct path, just not the one the UI asked for.
 */
export function fileBasename(p: string): string {
	const parts = p.split(/[\\/]/);
	return parts[parts.length - 1] || p;
}

/**
 * Whether `p` sits inside a `.git` directory, for either separator.
 *
 * `p.includes('.git/')` never matches a Windows fsPath, so the filter it
 * guards stops filtering — here that meant CKG queries firing for files under
 * `.git\`. Anchored on a separator so a legitimate `foo.github/` is not caught.
 */
export function isUnderGitDir(p: string): boolean {
	return /[\\/]\.git([\\/]|$)/.test(p);
}

/**
 * Normalizes a path extracted from a `file://` URI.
 *
 * Stripping the scheme off `file:///F:/proj/x.go` leaves `/F:/proj/x.go`, which
 * starts with a slash and so passes any absolute-path test while naming nothing
 * — the leading slash belongs to the URI, not to the path. Dropping it is
 * required before the value reaches the backend or `URI.file`.
 */
export function filePathFromUriString(uri: string): string {
	const raw = uri.startsWith('file://')
		? decodeURIComponent(uri.replace(/^file:\/\//, ''))
		: uri;
	const driveWithLeadingSlash = /^\/([a-zA-Z]:[\\/])/.exec(raw);
	return driveWithLeadingSlash ? raw.slice(1) : raw;
}
