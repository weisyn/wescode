package codeintel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"hash/crc32"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/weisyn/wescode/internal/codeintel/boundary"
	"github.com/weisyn/wescode/internal/treesitter"
	_ "modernc.org/sqlite"
)

// pass1Discover finds files that need (re-)indexing using two-stage delta:
//
//	Stage 1 (fast): mtime comparison — if mtime unchanged, skip immediately
//	Stage 2 (precise): SHA256 hash — catches rename/touch without content change
//
// Uses the unified boundary.Classifier four-layer filtering pipeline.
func (p *Pipeline) pass1Discover(_ context.Context) ([]FileToParse, error) {
	var files []FileToParse

	var prevHashes map[string]prevFileInfo
	if !p.FullRescan {
		prevHashes = p.loadPreviousHashes()
	}

	mode := boundary.ModeModerate
	if !p.FullRescan {
		mode = boundary.ModeFast
	}
	classifier := boundary.NewClassifier(mode, nil)
	classifier.SetWorkDir(p.WorkDir)
	classifier.SetWescodeignore(boundary.LoadWescodeignoreCached(p.WorkDir))

	detectLang := func(path string) (string, bool) {
		lang, ok := treesitter.DetectLang(path)
		return string(lang), ok
	}

	err := filepath.WalkDir(p.WorkDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			rel, _ := filepath.Rel(p.WorkDir, path)
			if skip, _ := classifier.ShouldSkipDir(d.Name(), rel); skip {
				return filepath.SkipDir
			}
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}

		verdict := classifier.ClassifyFile(path, info, detectLang)
		if verdict.Pipeline != boundary.PipelineCKG {
			return nil
		}

		currentMtime := info.ModTime().UnixNano()

		if prevHashes != nil {
			// prevHashes is keyed by the index representation (forward slashes),
			// while WalkDir hands out native separators. Looking up the raw path
			// misses every row on Windows, and the symptom is not an error: every
			// file looks new, so a full pipeline re-parses the entire workspace
			// on each run and the mtime/hash skip silently stops existing.
			key := IndexPath(path)
			if prev, ok := prevHashes[key]; ok && prev.mtimeNs == currentMtime {
				return nil
			}
			if prev, ok := prevHashes[key]; ok {
				content, readErr := os.ReadFile(path)
				if readErr == nil {
					hash := contentHashSHA(content)
					if hash == prev.hash {
						return nil
					}
				}
			}
		}

		files = append(files, FileToParse{
			Path:     path,
			MtimeNs:  currentMtime,
			Language: verdict.Language,
			Size:     info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

type prevFileInfo struct {
	mtimeNs int64
	hash    string
}

func (p *Pipeline) loadPreviousHashes() map[string]prevFileInfo {
	result := make(map[string]prevFileInfo, 1024)
	db, err := sql.Open("sqlite", p.DBPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return result
	}
	defer db.Close()

	rows, err := db.Query("SELECT file_path, content_hash, mtime_ns FROM file_hashes")
	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var path, hash string
		var mtime int64
		if rows.Scan(&path, &hash, &mtime) == nil {
			result[path] = prevFileInfo{mtimeNs: mtime, hash: hash}
		}
	}
	return result
}

// xxHashFast computes a fast non-cryptographic hash for change detection.
func xxHashFast(data []byte) string {
	h := crc32.NewIEEE()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// contentHashSHA is a fallback cryptographic hash for precise dedup.
func contentHashSHA(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ── Functions migrated from discover.go ─────────────────────────────────────

// DiscoverFiles returns all workspace files eligible for indexing.
// Uses the unified boundary.Classifier four-layer pipeline.
func DiscoverFiles(ctx context.Context, workDir string) ([]string, error) {
	files, err := discoverViaGit(ctx, workDir)
	if err != nil {
		files, err = discoverViaWalk(ctx, workDir)
		if err != nil {
			return nil, err
		}
	}

	classifier := boundary.NewClassifier(boundary.ModeModerate, nil)
	classifier.SetWescodeignore(boundary.LoadWescodeignoreCached(workDir))

	var result []string
	for _, f := range files {
		abs := f
		if !filepath.IsAbs(f) {
			abs = filepath.Join(workDir, f)
		}
		info, statErr := os.Stat(abs)
		if statErr != nil || info.IsDir() {
			continue
		}
		verdict := classifier.ClassifyFile(abs, info, nil)
		if verdict.Pipeline == boundary.PipelineExcluded {
			continue
		}
		result = append(result, abs)
	}
	return result, nil
}

func discoverViaGit(ctx context.Context, workDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z",
		"--cached", "--others", "--exclude-standard")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return splitNullTerminated(out), nil
}

func splitNullTerminated(data []byte) []string {
	var result []string
	for _, seg := range bytes.Split(data, []byte{0}) {
		s := string(seg)
		if s != "" {
			result = append(result, s)
		}
	}
	return result
}

func discoverViaWalk(ctx context.Context, workDir string) ([]string, error) {
	classifier := boundary.NewClassifier(boundary.ModeModerate, nil)
	classifier.SetWescodeignore(boundary.LoadWescodeignoreCached(workDir))

	var files []string
	err := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(workDir, path)
			if skip, _ := classifier.ShouldSkipDir(d.Name(), rel); skip {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// IsMinified reports whether a file appears to be minified code.
// Delegates to boundary.ClassifyContentFull.
func IsMinified(path string, content []byte) bool {
	r := boundary.ClassifyContentFull(path, content)
	return r != nil && *r == boundary.ReasonMinified
}
