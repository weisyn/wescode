package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/weisyn/wescode/internal/codeintel"
	"github.com/weisyn/wescode/internal/codeintel/boundary"
)

// fileContextSource provides file search via codeintel.DiscoverFiles.
type fileContextSource struct {
	getWorkspace func() string
	getAllRoots  func() []string
}

func (s *fileContextSource) ID() string { return "file" }

func (s *fileContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "file", Label: "文件", Icon: "file",
		Searchable: true, Available: s.getWorkspace() != "",
	}
}

func (s *fileContextSource) Search(ctx context.Context, query string, limit int) ([]ContextSearchItem, error) {
	roots := s.getAllRoots()
	if len(roots) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))

	var results []ContextSearchItem
	for _, root := range roots {
		if len(results) >= limit {
			break
		}
		files, err := codeintel.DiscoverFiles(ctx, root)
		if err != nil {
			continue
		}
		rootName := filepath.Base(root)
		for _, f := range files {
			if len(results) >= limit {
				break
			}
			rel, _ := filepath.Rel(root, f)
			if rel == "" {
				rel = f
			}
			name := filepath.Base(f)
			displayPath := rootName + "/" + rel
			if query == "" || query == "*" || strings.Contains(strings.ToLower(rel), query) || strings.Contains(strings.ToLower(name), query) {
				results = append(results, ContextSearchItem{
					ID:       "file:" + filepath.Join(root, rel),
					SourceID: "file",
					Label:    name,
					Detail:   displayPath,
					Icon:     "file",
				})
			}
		}
	}
	return results, nil
}

func (s *fileContextSource) Resolve(_ context.Context, itemID string) (*ContextResolved, error) {
	path := strings.TrimPrefix(itemID, "file:")
	return &ContextResolved{
		ID:       itemID,
		FilePath: path,
	}, nil
}

// folderContextSource provides folder search via filepath.WalkDir.
type folderContextSource struct {
	getWorkspace func() string
	getAllRoots  func() []string
}

func (s *folderContextSource) ID() string { return "folder" }

func (s *folderContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "folder", Label: "文件夹", Icon: "folder",
		Searchable: true, Available: s.getWorkspace() != "",
	}
}

func (s *folderContextSource) Search(_ context.Context, query string, limit int) ([]ContextSearchItem, error) {
	roots := s.getAllRoots()
	if len(roots) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))

	classifier := boundary.NewClassifier(boundary.ModeFast, nil)

	var results []ContextSearchItem
	for _, root := range roots {
		if len(results) >= limit {
			break
		}
		rootName := filepath.Base(root)
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			name := d.Name()
			rel, _ := filepath.Rel(root, path)
			if skip, _ := classifier.ShouldSkipDir(name, rel); skip {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			if rel == "." {
				return nil
			}
			displayPath := rootName + "/" + rel
			if query == "" || query == "*" || strings.Contains(strings.ToLower(rel), query) || strings.Contains(strings.ToLower(name), query) {
				results = append(results, ContextSearchItem{
					ID:       "folder:" + filepath.Join(root, rel),
					SourceID: "folder",
					Label:    name,
					Detail:   displayPath,
					Icon:     "folder",
				})
				if len(results) >= limit {
					return filepath.SkipAll
				}
			}
			return nil
		})
	}
	return results, nil
}

func (s *folderContextSource) Resolve(_ context.Context, itemID string) (*ContextResolved, error) {
	path := strings.TrimPrefix(itemID, "folder:")
	return &ContextResolved{
		ID:       itemID,
		FilePath: path,
	}, nil
}
