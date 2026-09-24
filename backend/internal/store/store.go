package store

import (
	"encoding/base64"
	"os"
	"sort"
	"strings"

	"github.com/weisyn/wesapp/wire"
	wesgine "github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/message"
)

// UIMessage is the read-time projection of wes_messages into a UI-ready format.
// No separate database table — folded on the fly from the single source of truth.
type UIMessage struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Content      string `json:"content"`
	CreatedAt    string `json:"createdAt"`
	AgentID      string `json:"agentId,omitempty"`
	ContentParts any    `json:"contentParts,omitempty"`
}

// DisplayText returns the message's text regardless of where FoldToUI put it.
//
// Callers cannot just read Content: for an assistant message whose parts carry
// text, FoldToUI deliberately blanks Content so a rendering frontend does not
// print the same words twice. Every non-rendering consumer that wants "what did
// it say" has to know that, and the one that did not — the cron run history's
// last_message, gated on `Content != ""` — showed nothing at all for every run
// ever recorded. One place knows the rule; nobody else has to.
func DisplayText(m UIMessage) string {
	if m.Content != "" {
		return m.Content
	}
	parts, ok := m.ContentParts.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok || pm["kind"] != "text" {
			continue
		}
		if s, ok := pm["text"].(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

// FoldToUI converts raw wes_messages (LLM protocol format) into UI-ready
// messages by grouping per-run and folding tool chains into content_parts.
// Input must be ordered by seq (ascending); full-session load guarantees this.
//
// Delegates to wesapp/wire.FoldHistoryWith for the shared fold pipeline
// (tool pairing, plan merging, edit_status, thinking) and injects wescode-
// specific hooks for context_ref, skill_ref, and local-image preview.
func FoldToUI(raw []wesgine.MessageInfo) []UIMessage {
	if len(raw) == 0 {
		return nil
	}

	// Defensive sort (SQL already returns ASC for limit=0).
	sort.Slice(raw, func(i, j int) bool {
		return raw[i].CreatedAt.Before(raw[j].CreatedAt)
	})

	hms := wire.FoldHistoryWith(raw, wire.FoldOptions{
		UserPartHook: wescodeUserPartHook,
	})

	out := make([]UIMessage, 0, len(hms))
	for _, hm := range hms {
		var partsAny any
		if len(hm.ContentParts) > 0 {
			partsAny = hm.ContentParts
		}
		out = append(out, UIMessage{
			ID:           hm.ID,
			Role:         hm.Role,
			Content:      hm.Content,
			CreatedAt:    hm.CreatedAt,
			AgentID:      hm.AgentID,
			ContentParts: partsAny,
		})
	}
	return out
}

// wescodeUserPartHook handles wescode-specific user content blocks that the
// shared wire pipeline does not know about: context_ref chips (INV-CTX-REF-
// 01/03), skill_ref chips, and local-image preview / localPath for file_ref.
// Returning non-nil overrides wire's default handling for that block.
func wescodeUserPartHook(b message.ContentBlock) map[string]any {
	switch b.Type {
	case "context_ref":
		part := map[string]any{
			"kind":   "context_ref",
			"refId":  b.ID,
			"source": "",
			"label":  b.Name,
		}
		if b.ContextRefMeta != nil {
			part["source"] = b.ContextRefMeta.Source
			if b.ContextRefMeta.Label != "" {
				part["label"] = b.ContextRefMeta.Label
			}
			if b.ContextRefMeta.URI != "" {
				part["uri"] = b.ContextRefMeta.URI
			}
			if b.ContextRefMeta.Detail != "" {
				part["detail"] = b.ContextRefMeta.Detail
			}
		}
		return part
	case "skill_ref":
		return map[string]any{
			"kind": "skill_ref",
			"name": b.Name,
		}
	case "file_ref":
		// Override wire's default file_ref to add local-image preview and
		// localPath for non-image attachments persisted by stripInlineImages.
		part := map[string]any{
			"kind":     "file_ref",
			"fileId":   b.ID,
			"fileName": b.Name,
			"mimeType": b.MIMEType,
		}
		if p := localImageDataURL(b); p != "" {
			part["previewUrl"] = p
		} else if isLocalDiskPath(b.Source) {
			part["localPath"] = b.Source
		}
		return part
	case "image":
		part := map[string]any{
			"kind":     "file_ref",
			"fileId":   b.ID,
			"fileName": b.Name,
			"mimeType": b.MIMEType,
		}
		if b.Source != "" {
			part["previewUrl"] = b.Source
		}
		return part
	}
	return nil
}

// localImageDataURL returns a renderable data URL for a locally-persisted
// inline image (absolute disk path in Source). Inline data: URLs pass through
// untouched; anything else that is not a readable local image file yields "".
func localImageDataURL(b message.ContentBlock) string {
	if b.Source == "" {
		return ""
	}
	if strings.HasPrefix(b.Source, "data:") {
		return b.Source
	}
	if !strings.HasPrefix(strings.ToLower(b.MIMEType), "image/") {
		return ""
	}
	raw, err := os.ReadFile(b.Source)
	if err != nil {
		return ""
	}
	// Cap at 10 MB to keep the list response sane for very large screenshots.
	if len(raw) > 10*1024*1024 {
		return ""
	}
	return "data:" + b.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// isLocalDiskPath reports whether Source is a persisted local file path
// (stripInlineImages output) rather than an inline/remote reference.
func isLocalDiskPath(source string) bool {
	if source == "" {
		return false
	}
	if strings.HasPrefix(source, "data:") || strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return false
	}
	return true
}
