package constraints

// BoostFromRegression increases confidence for an existing constraint
// when L2.5 detects a regression matching the constraint's pattern.
// INV-CSE-07: capped at +0.2 per event.
//
// Returns the constraint as stored after the boost, or nil when the ID is not
// registered. Callers use nil to decide "create it" — an already-capped
// constraint still returns non-nil, because it exists.
func BoostFromRegression(reg *Registry, constraintID string) *Constraint {
	c := reg.Get(constraintID)
	if c == nil {
		return nil
	}
	if boost := headroomCapped(c.Confidence, 0.2); boost > 0 {
		return reg.PromoteByID(constraintID, boost)
	}
	return c
}

// BoostFromTestPass increases confidence by a small delta when the AI
// obeys a constraint and all tests pass. INV-CSE-07 cap applies.
func BoostFromTestPass(reg *Registry, constraintID string) *Constraint {
	c := reg.Get(constraintID)
	if c == nil {
		return nil
	}
	if boost := headroomCapped(c.Confidence, 0.05); boost > 0 {
		return reg.PromoteByID(constraintID, boost)
	}
	return c
}

// DemoteFromFalsePositive decreases confidence when a constraint
// warning is ignored and tests still pass (suggesting the constraint
// may not be valid). Delta is small to avoid rapid decay.
func DemoteFromFalsePositive(reg *Registry, constraintID string) {
	reg.DemoteByID(constraintID, 0.05)
}

// headroomCapped clamps a boost to the distance left below confidence 1.0.
func headroomCapped(confidence, delta float64) float64 {
	if headroom := 1.0 - confidence; headroom < delta {
		return headroom
	}
	return delta
}
