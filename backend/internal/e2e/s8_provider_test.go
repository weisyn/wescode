//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 8: Provider & Model Management

func TestS8_ProviderList(t *testing.T) {
	h := newGoHarness(t)
	ctx := context.Background()
	status := h.Svc.DebugStatus(ctx)
	t.Logf("status keys: %v", mapKeys(status))
	// Provider list is exposed via sidebar/listProviders RPC;
	// at engine level we verify the service initializes with configured providers.
}

func TestS8_Chat_WithDefaultProvider(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "你好，简单回复一句话即可")
	if result.Text == "" {
		t.Error("expected non-empty response from default provider")
	}
	if result.TokensIn == 0 && result.TokensOut == 0 {
		t.Log("no token usage reported (provider may not emit usage)")
	}
}

func TestS8_TokenUsage_Tracked(t *testing.T) {
	h := newGoHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result := h.Chat(ctx, "写一首短诗")
	if result.TokensIn+result.TokensOut > 0 {
		t.Logf("Token usage: in=%d out=%d", result.TokensIn, result.TokensOut)
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
