package engine

import (
	"context"
)

// CheckWesBillingFull returns the billing status from the weisyn platform.
// This is a pure query — the application has no billing management authority.
// The weisyn LLM proxy is the sole enforcement point (INV-BILLING-03).
func (s *Service) CheckWesBillingFull(ctx context.Context) map[string]any {
	var out map[string]any
	if s.wesHandler != nil {
		out = s.wesHandler.GetBilling(ctx)
	}
	if out == nil {
		out = map[string]any{"enabled": false}
	}
	// Auth session is the capability source of truth. A missing or stale
	// GetBilling snapshot must not leave WES selectable (INV-CHAT-02).
	if s.wesGrantWithdrawn(ctx) {
		out["enabled"] = true
		out["active"] = false
		if reason, _ := out["reason"].(string); reason == "" {
			out["reason"] = WESStateBillingOverdue
		}
	}
	return out
}
