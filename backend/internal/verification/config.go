package verification

type Config struct {
	L0Syntax      bool   `yaml:"l0_syntax"`
	L1Compile     bool   `yaml:"l1_compile"`
	L1Method      string `yaml:"l1_method"`
	L2Test        string `yaml:"l2_test"`
	L2Timeout     int    `yaml:"l2_timeout"`
	L2ExpandDeps  bool   `yaml:"l2_expand_deps"`
	L25Regression string `yaml:"l25_regression"` // "auto" | "off" (default "auto")
	L3Review      string `yaml:"l3_review"`
	MaxRevisions  struct {
		Compile    int `yaml:"compile"`
		Test       int `yaml:"test"`
		Regression int `yaml:"regression"`
		Review     int `yaml:"review"`
	} `yaml:"max_revisions"`
	// MaxTotalRevisions caps the cumulative revision count across ALL criteria
	// (compilation + test + regression + review). Prevents runaway revision
	// loops when criteria alternate (P0-3 fix). 0 = use default (3).
	MaxTotalRevisions int `yaml:"max_total_revisions"`
	// BenchMode skips L2/L2.5/L3 verification (bench has its own verify_cmd).
	// L0/L1 remain active — syntax and compile errors are deterministic and
	// don't interfere with bench evaluation.
	BenchMode bool `yaml:"-"`
}

func DefaultConfig() Config {
	c := Config{
		L0Syntax:          true,
		L1Compile:         true,
		L1Method:          "build",
		L2Test:            "auto",
		L2Timeout:         60,
		L2ExpandDeps:      false,
		L25Regression:     "auto",
		L3Review:          "manual",
		MaxTotalRevisions: 3,
	}
	c.MaxRevisions.Compile = 3
	c.MaxRevisions.Test = 2
	c.MaxRevisions.Regression = 2
	c.MaxRevisions.Review = 1
	return c
}

// MergeConfig overlays user-provided values onto defaults. Zero-value fields
// in override are treated as "not set" and inherit from base.
func MergeConfig(base, override Config) Config {
	if override.L1Method != "" {
		base.L1Method = override.L1Method
	}
	if override.L2Test != "" {
		base.L2Test = override.L2Test
	}
	if override.L2Timeout > 0 {
		base.L2Timeout = override.L2Timeout
	}
	if override.L2ExpandDeps {
		base.L2ExpandDeps = true
	}
	if override.L25Regression != "" {
		base.L25Regression = override.L25Regression
	}
	if override.L3Review != "" {
		base.L3Review = override.L3Review
	}
	if !override.L0Syntax {
		base.L0Syntax = false
	}
	if !override.L1Compile {
		base.L1Compile = false
	}
	if override.MaxRevisions.Compile > 0 {
		base.MaxRevisions.Compile = override.MaxRevisions.Compile
	}
	if override.MaxRevisions.Test > 0 {
		base.MaxRevisions.Test = override.MaxRevisions.Test
	}
	if override.MaxRevisions.Regression > 0 {
		base.MaxRevisions.Regression = override.MaxRevisions.Regression
	}
	if override.MaxRevisions.Review > 0 {
		base.MaxRevisions.Review = override.MaxRevisions.Review
	}
	if override.MaxTotalRevisions > 0 {
		base.MaxTotalRevisions = override.MaxTotalRevisions
	}
	return base
}
