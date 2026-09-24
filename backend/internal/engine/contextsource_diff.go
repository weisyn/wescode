package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// diffContextSource provides git diff information as context.
type diffContextSource struct {
	getWorkspace func() string
	getAllRoots  func() []string
}

func (s *diffContextSource) ID() string { return "diff" }

func (s *diffContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "diff", Label: "Git 变更", Icon: "git-commit",
		Searchable: true, Available: s.getWorkspace() != "",
	}
}

func (s *diffContextSource) Search(ctx context.Context, query string, limit int) ([]ContextSearchItem, error) {
	roots := s.getAllRoots()
	if len(roots) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))

	type changedFile struct {
		root string
		path string
	}
	var allChanges []changedFile

	for _, root := range roots {
		if out, err := execGit(ctx, root, "diff", "--name-only"); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if line != "" {
					allChanges = append(allChanges, changedFile{root: root, path: line})
				}
			}
		}
		if out, err := execGit(ctx, root, "diff", "--staged", "--name-only"); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if line != "" {
					allChanges = append(allChanges, changedFile{root: root, path: line})
				}
			}
		}
	}

	seen := make(map[string]bool)
	var unique []changedFile
	for _, c := range allChanges {
		key := c.root + ":" + c.path
		if !seen[key] {
			seen[key] = true
			unique = append(unique, c)
		}
	}
	if len(unique) == 0 {
		return nil, nil
	}

	var results []ContextSearchItem
	results = append(results, ContextSearchItem{
		ID:       "diff:*",
		SourceID: "diff",
		Label:    fmt.Sprintf("全部变更 (%d 个文件)", len(unique)),
		Icon:     "git-commit",
	})

	for _, c := range unique {
		if len(results) >= limit {
			break
		}
		rootName := filepath.Base(c.root)
		display := rootName + "/" + c.path
		if query != "" && query != "*" && !strings.Contains(strings.ToLower(display), query) {
			continue
		}
		results = append(results, ContextSearchItem{
			ID:       "diff:" + c.root + ":" + c.path,
			SourceID: "diff",
			Label:    display,
			Icon:     "diff",
			Data:     map[string]any{"root": c.root, "path": c.path},
		})
	}
	return results, nil
}

func (s *diffContextSource) Resolve(ctx context.Context, itemID string) (*ContextResolved, error) {
	target := strings.TrimPrefix(itemID, "diff:")
	var diffContent string

	if target == "*" {
		for _, root := range s.getAllRoots() {
			if out, err := execGit(ctx, root, "diff"); err == nil && out != "" {
				diffContent += out + "\n"
			}
			if staged, err := execGit(ctx, root, "diff", "--staged"); err == nil && staged != "" {
				diffContent += staged + "\n"
			}
		}
	} else {
		parts := strings.SplitN(target, ":", 2)
		if len(parts) == 2 {
			root, file := parts[0], parts[1]
			if out, err := execGit(ctx, root, "diff", "--", file); err == nil {
				diffContent = out
			}
			if staged, err := execGit(ctx, root, "diff", "--staged", "--", file); err == nil && staged != "" {
				if diffContent != "" {
					diffContent += "\n"
				}
				diffContent += staged
			}
		}
	}

	return &ContextResolved{
		ID:      itemID,
		Content: strings.TrimSpace(diffContent),
	}, nil
}

func dedup(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
