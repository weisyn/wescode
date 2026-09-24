//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Stage 0: Boot & Health — verifies the engine initializes correctly
// with various project types and exposes subsystem status.

func TestS0_EmptyWorkspace_Initialize(t *testing.T) {
	h := NewHarness(t, "")
	status := h.Svc.DebugStatus(context.Background())
	if status["initialized"] != true {
		t.Error("expected initialized=true")
	}
}

func TestS0_GoProject_Initialize(t *testing.T) {
	dir := GenerateGoProject(t)
	h := NewHarness(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count := h.WaitForIndex(ctx, 3)
	if count < 3 {
		t.Errorf("expected >= 3 symbols after indexing, got %d", count)
	}
}

func TestS0_DebugStatus_Complete(t *testing.T) {
	dir := GenerateGoProject(t)
	h := NewHarness(t, dir)
	status := h.Svc.DebugStatus(context.Background())

	if status["codeIndex"] == nil {
		t.Error("DebugStatus missing codeIndex")
	}
	if status["qualityGate"] == nil {
		t.Error("DebugStatus missing qualityGate")
	}
	if status["constraints"] == nil {
		t.Error("DebugStatus missing constraints")
	}
}

func TestS0_Constraint_AutoInfer(t *testing.T) {
	dir := GenerateGoProject(t)
	h := NewHarness(t, dir)
	status := h.Svc.DebugStatus(context.Background())

	constraints, ok := status["constraints"].(map[string]any)
	if !ok {
		t.Fatal("constraints not a map")
	}
	active, _ := constraints["active"].(int)
	if active == 0 {
		t.Error("expected at least 1 active constraint (Go conventions), got 0")
	}
	total, _ := constraints["total"].(int)
	t.Logf("constraints: total=%d active=%d", total, active)
}

func TestS0_CKG_BackgroundIndex(t *testing.T) {
	dir := GenerateGoProject(t)
	h := NewHarness(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count := h.WaitForIndex(ctx, 5)
	t.Logf("CKG indexed %d symbols", count)
}

func TestS0_Initialize_Idempotent(t *testing.T) {
	dir := GenerateGoProject(t)
	h := NewHarness(t, dir)

	ctx := context.Background()
	_, err := h.Svc.Initialize(ctx, dir, []string{dir})
	if err != nil {
		t.Fatalf("second Initialize failed: %v", err)
	}
	status := h.Svc.DebugStatus(ctx)
	if status["initialized"] != true {
		t.Error("not initialized after second call")
	}
}
