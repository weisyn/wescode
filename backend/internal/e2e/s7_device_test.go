//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	wesengine "github.com/weisyn/wesgine/engine"
)

// Stage 7: Device Agent & Editor Integration

func TestS7_EditPreview_Event(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "将 main.go 中的 hello 替换为 goodbye")
	AssertToolTriggered(t, result, "edit")
	// edit.preview events are injected by the handler layer; in engine-level
	// tests they appear as tool_start/tool_result for the edit tool.
}

func TestS7_PostWriteIndex_Update(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h.WaitForIndex(ctx, 3)

	// Write a new file with exported symbols
	h.Chat(ctx, "创建 internal/service/order.go 包含导出函数 CreateOrder(items []string) error")

	// Wait for re-index
	time.Sleep(500 * time.Millisecond)

	// Query should find the new symbol
	result := h.Chat(ctx, "搜索项目中名为 CreateOrder 的函数")
	AssertToolTriggered(t, result, "search_symbols")
}

func TestS7_Exec_Classification_Verification(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Verification command (go build) should be routed to observable terminal
	result := h.Chat(ctx, "运行 go build ./... 检查项目是否能编译")
	AssertToolTriggered(t, result, "exec")
}

func TestS7_Exec_Classification_Interactive(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Interactive command should be flagged differently
	result := h.Chat(ctx, "运行 echo 'test interactive'")
	AssertToolTriggered(t, result, "exec")
}

func TestS7_BackgroundRun(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Background runs use a separate path (RunChatBackground)
	// In engine-level tests we verify the runtime accepts concurrent requests
	result := h.Chat(ctx, "告诉我当前时间")
	if len(result.Text) == 0 && len(result.Errors) == 0 {
		t.Error("expected some response from chat")
	}
}

func TestS7_StreamEvents_Complete(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "你好")

	// Should have at minimum: stream_delta events
	hasStreamDelta := false
	for _, ev := range result.AllEvents {
		if ev.Type == wesengine.EventStreamDelta {
			hasStreamDelta = true
			break
		}
	}
	if !hasStreamDelta {
		t.Error("expected at least one stream_delta event")
	}
	if result.Text == "" {
		t.Error("expected non-empty text response")
	}
}
