package engine

import (
	"context"

	"github.com/weisyn/wesgine/knowledge"
)

type KBFileInfo = knowledge.FileInfo

func (s *Service) DocSyncListFiles(ctx context.Context) ([]KBFileInfo, error) {
	s.mu.Lock()
	ds := s.docSync
	s.mu.Unlock()
	if ds == nil {
		return nil, nil
	}
	return ds.ListFiles(ctx)
}

func (s *Service) DocSyncRescan(ctx context.Context) (int, int, error) {
	s.mu.Lock()
	ds := s.docSync
	s.mu.Unlock()
	if ds == nil {
		return 0, 0, nil
	}
	return ds.Rescan(ctx)
}

func (s *Service) DocSyncSearch(ctx context.Context, query string) ([]knowledge.SearchResult, error) {
	s.mu.Lock()
	ds := s.docSync
	s.mu.Unlock()
	if ds == nil {
		return nil, nil
	}
	return ds.Search(ctx, query)
}

func (s *Service) DocSyncStats(ctx context.Context) (*knowledge.KnowledgeStats, error) {
	s.mu.Lock()
	ds := s.docSync
	s.mu.Unlock()
	if ds == nil {
		return nil, nil
	}
	return ds.Stats(ctx)
}

func (s *Service) DocSyncReindexQuarantined(ctx context.Context) (int, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil || cell.Knowledge() == nil {
		return 0, nil
	}
	return cell.Knowledge().ReindexQuarantined(ctx)
}

func (s *Service) DocSyncClear(ctx context.Context) error {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil {
		return nil
	}
	kh := cell.Knowledge()
	if kh == nil {
		return nil
	}
	files, err := kh.ListFiles(ctx, knowledge.FileListOptions{Limit: 10000})
	if err != nil {
		return err
	}
	for _, f := range files {
		_ = kh.DeleteFile(ctx, f.ID)
	}
	return nil
}

// DocSyncReindexFile triggers a reindex of a single file by ID.
func (s *Service) DocSyncReindexFile(ctx context.Context, fileID string) error {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil || cell.Knowledge() == nil {
		return nil
	}
	return cell.Knowledge().Reindex(ctx, fileID)
}

// DocSyncDeleteFile removes a single file from the knowledge index.
func (s *Service) DocSyncDeleteFile(ctx context.Context, fileID string) error {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil || cell.Knowledge() == nil {
		return nil
	}
	return cell.Knowledge().DeleteFile(ctx, fileID)
}

// DocSyncReindexErrors re-indexes all files with status=error. Returns the
// count of files for which reindex was attempted.
func (s *Service) DocSyncReindexErrors(ctx context.Context) (int, error) {
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell == nil || cell.Knowledge() == nil {
		return 0, nil
	}
	kh := cell.Knowledge()
	files, err := kh.ListFiles(ctx, knowledge.FileListOptions{Status: "error", Limit: 10000})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, f := range files {
		if reErr := kh.Reindex(ctx, f.ID); reErr == nil {
			count++
		}
	}
	return count, nil
}

func (s *Service) DocSyncSyncRoots(ctx context.Context, newRoots []string) {
	s.mu.Lock()
	ds := s.docSync
	s.mu.Unlock()
	if ds != nil {
		ds.SyncRoots(ctx, newRoots)
	}
}
