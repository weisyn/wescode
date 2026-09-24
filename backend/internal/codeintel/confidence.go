package codeintel

import (
	"fmt"

	"github.com/weisyn/wesgine/tool"
)

// CKGBias states which way an incomplete index bends a tool's answer.
//
// The two-value domain is closed and the distinction is load-bearing: a tool
// that reports what it *found* under-reports when rows are missing, while a
// tool that reports what it did *not* find manufactures findings from the same
// missing rows. Telling the model "results may be incomplete" about an orphan
// list is worse than silence — it points suspicion at the one half of the
// answer that is actually sound.
type CKGBias uint8

const (
	// BiasUnderReport is the zero value, so a tool inherits it by not saying
	// anything. That default is the safe one: almost every CKG tool answers
	// "what matches", and a short index yields a short answer.
	BiasUnderReport CKGBias = iota
	// BiasOverReport marks absence-based queries (orphans, dead code): a
	// missing edge invents a result instead of hiding one.
	BiasOverReport
)

// CKGBiasDeclarer is implemented only by absence-based tools. Silence means
// BiasUnderReport — see the constant's comment for why that default is safe.
type CKGBiasDeclarer interface {
	CKGBias() CKGBias
}

// ConfidenceResult carries CKG readiness metadata for tool results.
//
// It deliberately holds no rendered warning: the sentence depends on the
// querying tool's CKGBias, which this type cannot know. Rendering lives in
// Disclosure.
type ConfidenceResult struct {
	Confidence float64
	Source     string
	Indexing   bool
}

// ComputeConfidence derives a ConfidenceResult from the current CodeIndex state.
func (ci *CodeIndex) ComputeConfidence() ConfidenceResult {
	if ci == nil {
		return ConfidenceResult{Confidence: 0.0, Source: "unavailable"}
	}
	r := ci.Readiness()

	var source string
	switch {
	case r.Completeness >= ReadinessHigh:
		source = "ckg"
	case r.Completeness >= ReadinessMedium:
		source = "ckg_partial"
	case r.Completeness >= ReadinessLow:
		source = "grep+ckg_partial"
	default:
		source = "grep"
	}

	return ConfidenceResult{
		Confidence: r.Completeness,
		Source:     source,
		Indexing:   r.Indexing,
	}
}

// Disclosure renders the model-facing sentence for this confidence level, or
// "" when the index is complete enough to answer at full fidelity.
//
// INV-CKG-CONF-01: the gate is ReadinessHigh — the same threshold that means
// "full fidelity" everywhere else in this package. There is deliberately no
// second, lower gate. A band that degrades the answer while staying silent is,
// to the model, indistinguishable from a healthy index; that band used to run
// from 0.7 to 0.9 and covered the common case of a repo still being indexed.
func (cr ConfidenceResult) Disclosure(bias CKGBias) string {
	if cr.Confidence >= ReadinessHigh {
		return ""
	}

	var trust string
	switch {
	case cr.Confidence >= ReadinessMedium:
		trust = "trust what is listed, but do not treat the list as exhaustive"
	case cr.Confidence >= ReadinessLow:
		trust = "confirm with read before making edit decisions"
	default:
		trust = "do NOT base edit decisions on this — confirm with read or grep"
	}

	var effect string
	switch bias {
	case BiasOverReport:
		// Absence-based query: unindexed references look like no references.
		effect = "listed items may be false positives"
	default:
		effect = "matches may be missing"
	}

	msg := fmt.Sprintf("CKG index %.0f%% complete", cr.Confidence*100)
	if cr.Indexing {
		msg += ", indexing in progress"
	}
	return msg + " — " + effect + "; " + trust
}

// EnrichResult attaches CKG readiness disclosure to a tool result.
//
// The text footer is the primary channel and is unconditional below
// ReadinessHigh. The metadata is a secondary signal for wesgine's loop, which
// injects its own advisory below 0.5 — but only for a *positive* confidence, so
// a completely unavailable index (0.0, the worst case) reaches the model through
// the footer alone. Relying on the metadata channel alone inverted severity:
// the emptier the index, the quieter the warning.
//
// Errored results are left untouched; "invalid input" does not become more
// trustworthy with a fuller index.
func (ci *CodeIndex) EnrichResult(result *tool.ToolResult, bias CKGBias) {
	if result == nil || result.IsError {
		return
	}
	cr := ci.ComputeConfidence()
	msg := cr.Disclosure(bias)
	if msg == "" {
		return // full fidelity: zero overhead, no metadata, no footer
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any, 3)
	}
	result.Metadata["confidence"] = cr.Confidence
	result.Metadata["source"] = cr.Source
	result.Metadata["warning"] = msg
	result.Content += "\n\n[" + msg + "]"
}
