package presets

import "embed"

//go:embed agents/*.yaml
var AgentsFS embed.FS

//go:embed all:skills
var SkillsFS embed.FS
