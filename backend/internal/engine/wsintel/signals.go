package wsintel

import (
	"os"
	"path/filepath"
	"time"
)

// IsLegacy detects legacy codebase signals. Returns true when 3+ signals match.
func IsLegacy(ctx *WorkspaceContext) bool {
	signals := 0

	if ctx.Primary.DeprecatedCount > 5 {
		signals++
	}
	if ctx.Primary.LastCommitAge > 6*30*24*time.Hour {
		signals++
	}
	if ctx.Primary.TestFileCount == 0 {
		signals++
	}
	if !hasCI(ctx.Primary.Path) {
		signals++
	}
	if !hasLinter(ctx.Primary.Path) {
		signals++
	}

	return signals >= 3
}

// IsEmpty detects empty/bootstrapping project.
func IsEmpty(ctx *WorkspaceContext) bool {
	return ctx.Primary.Type == "unknown" && ctx.Primary.FileCount < 5
}

// IsMigration detects active migration signals by checking for
// common migration indicator files and directories in the project root.
func IsMigration(ctx *WorkspaceContext) bool {
	migrationFiles := []string{
		"MIGRATION.md", "UPGRADE.md", "BREAKING_CHANGES.md",
		"MIGRATING.md", "CHANGELOG-MIGRATION.md",
	}
	for _, f := range migrationFiles {
		if _, err := os.Stat(filepath.Join(ctx.Primary.Path, f)); err == nil {
			return true
		}
	}
	migrationDirs := []string{"migrations", "db/migrations", "db/migrate"}
	for _, d := range migrationDirs {
		full := filepath.Join(ctx.Primary.Path, d)
		if fi, err := os.Stat(full); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}
