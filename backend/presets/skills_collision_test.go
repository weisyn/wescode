package presets

import (
	"io/fs"
	"testing"

	"github.com/weisyn/wesgine"
)

// TestPresetSkillsDoNotShadowTierE asserts that no bundled skill shares a
// name with an engine (Tier E) skill.
//
// The check lives in this repo's own CI and takes its list from the
// engine binary rather than a copy: "applications must not shadow Tier E"
// was written in prose for months with zero enforcement points, and one
// product was violating it the whole time — with a stale fork of an
// always-on engine skill that reached every prompt and could never be
// updated.
//
// The engine now refuses such a name at seed time (ErrSkillImmutable),
// so this test is not the only defence; it is the one that fails in the
// pull request instead of at a user's first boot.
func TestPresetSkillsDoNotShadowTierE(t *testing.T) {
	engineNames := make(map[string]struct{})
	for _, n := range wesgine.EngineSkillNames() {
		engineNames[n] = struct{}{}
	}
	if len(engineNames) == 0 {
		t.Fatal("engine reported no Tier E skills — the check would be vacuous")
	}

	root, err := fs.Sub(SkillsFS, "skills")
	if err != nil {
		t.Fatalf("sub skills: %v", err)
	}
	bundled := wesgine.SkillDirNames(root)
	if len(bundled) == 0 {
		t.Fatal("no bundled skills found — the check would be vacuous")
	}

	for _, name := range bundled {
		if _, clash := engineNames[name]; clash {
			t.Errorf("bundled skill %q shadows a Tier E skill: rename it "+
				"(wescode-retrieval is the pattern) instead of overriding "+
				"the engine's copy", name)
		}
	}
}
