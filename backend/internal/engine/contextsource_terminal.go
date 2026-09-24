package engine

import "context"

// terminalContextSource lists active terminals.
// Terminal data comes from the extension host's TerminalService (device agent).
// For now, returns empty results — the extension host fills via resolve.
type terminalContextSource struct{}

func (s *terminalContextSource) ID() string { return "terminal" }

func (s *terminalContextSource) Info() ContextSourceInfo {
	return ContextSourceInfo{
		ID: "terminal", Label: "终端", Icon: "terminal",
		Searchable: false, Available: false,
	}
}

func (s *terminalContextSource) Search(_ context.Context, _ string, _ int) ([]ContextSearchItem, error) {
	return nil, nil
}

func (s *terminalContextSource) Resolve(_ context.Context, itemID string) (*ContextResolved, error) {
	return &ContextResolved{
		ID:      itemID,
		Content: "",
	}, nil
}
