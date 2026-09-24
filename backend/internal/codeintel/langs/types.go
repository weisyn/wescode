package langs

// LanguageConfig describes a programming language's toolchain, AST structure,
// and development conventions. Loaded from embedded JSON files at startup.
// Adding a new language = adding a new JSON file (no Go code changes needed).
type LanguageConfig struct {
	Language      LanguageMeta   `json:"language"`
	Calls         CallConfig     `json:"calls"`
	TestRunner    RunnerConfig   `json:"test_runner"`
	CompileRunner RunnerConfig   `json:"compile_runner"`
	Topology      TopologyConfig `json:"topology"`
}

// LanguageMeta identifies the language and its file/project conventions.
type LanguageMeta struct {
	Name              string   `json:"name"`
	ID                string   `json:"id"`
	Extensions        []string `json:"extensions"`
	TestFilePatterns  []string `json:"test_file_patterns"`
	MarkerFiles       []string `json:"marker_files"`
	TreeSitterGrammar string   `json:"tree_sitter_grammar"`
}

// CallConfig describes how function and method calls appear in the AST.
type CallConfig struct {
	FunctionCall CallNodeDef `json:"function_call"`
	MethodCall   CallNodeDef `json:"method_call"`
}

// CallNodeDef maps AST node types and field names for call extraction.
type CallNodeDef struct {
	NodeType      string `json:"node_type"`
	FunctionField string `json:"function_field"`
	ReceiverField string `json:"receiver_field"`
	MethodField   string `json:"method_field"`
}

// RunnerConfig describes a compile or test command and its output parsing.
type RunnerConfig struct {
	Command      string            `json:"command"`
	Args         []string          `json:"args"`
	ParsePattern map[string]string `json:"parse_pattern"`
}

// TopologyConfig declares how to discover local path dependencies
// from this language's project manifests.
type TopologyConfig struct {
	DependencyFiles []DependencyFileConfig `json:"dependency_files"`
}

// DependencyFileConfig describes one manifest file and how to parse local deps from it.
type DependencyFileConfig struct {
	File   string `json:"file"`
	Format string `json:"format"`
}
