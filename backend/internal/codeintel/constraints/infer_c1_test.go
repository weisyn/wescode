package constraints

import (
	"math"
	"testing"
)

func TestC1ConfidenceScaling(t *testing.T) {
	tests := []struct {
		callers int
		want    float64 // expected confidence (rounded to 2dp)
		active  bool    // confidence >= 0.8 activation threshold
	}{
		{5, 0.57, false},     // minimum caller count (HAVING >= 5)
		{10, 0.60, false},    // baseline (was the old fixed 0.6)
		{100, 0.70, false},   // strong evidence
		{1000, 0.80, true},   // reaches activation threshold
		{10000, 0.90, true},  // capped at 0.9
		{100000, 0.90, true}, // cap holds for huge fan-in
	}
	for _, tt := range tests {
		got := math.Round(C1Confidence(tt.callers)*100) / 100
		if got != tt.want {
			t.Errorf("callers=%d: confidence = %.2f, want %.2f", tt.callers, got, tt.want)
		}
		if active := got >= 0.8; active != tt.active {
			t.Errorf("callers=%d: active = %v, want %v", tt.callers, active, tt.active)
		}
	}
}

func TestC1ConfidenceMonotonic(t *testing.T) {
	// More callers must never produce lower confidence.
	prev := 0.0
	for callers := 5; callers <= 5000; callers += 7 {
		c := C1Confidence(callers)
		if c < prev {
			t.Fatalf("confidence decreased at callers=%d: %.4f < %.4f", callers, c, prev)
		}
		prev = c
	}
}
