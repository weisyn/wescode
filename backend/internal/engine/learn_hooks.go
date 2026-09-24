package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/weisyn/wesgine/tool"
)

// agentWriteTrackHook returns a PostCallHook that records file hashes after
// the agent writes/edits files. Used by AcceptEdits to detect S2 signals
// (user modified the agent's edit before accepting).
func (s *Service) agentWriteTrackHook() tool.PostCallHook {
	return func(_ context.Context, call tool.ToolCall, result *tool.ToolResult, _ *tool.ToolContext) {
		if result == nil || result.IsError {
			return
		}
		if call.Name != "edit" && call.Name != "write" && call.Name != "apply_patch" {
			return
		}
		path := extractFilePath(call.Input)
		if path == "" {
			return
		}
		if h := quickHash(path); h != "" {
			s.agentFileHashes.Store(path, h)
		}
	}
}

func extractFilePath(input json.RawMessage) string {
	var args struct {
		File string `json:"file"`
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &args)
	if args.File != "" {
		return args.File
	}
	return args.Path
}

func quickHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}
