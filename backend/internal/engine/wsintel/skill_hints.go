package wsintel

import wesgine "github.com/weisyn/wesgine"

// Skill selection is driven by what the workspace *is*, never by a guess at
// what the user is trying to do.
//
// Both functions below once took an ActivityMode inferred from the user's
// message by keyword matching. That inference was the same shape the engine
// deleted in wesgine anti-pattern 259 ("认知判断离开引擎": the regex extractor
// that decided what was worth remembering) and 255 (the engine deciding plan
// step order): a rule engine guessing intent, then acting on the guess. It
// mislabelled about half of ordinary messages — "帮我实现一个配置解析器" read as
// ops work because it contains 配置, "把这段日志解析逻辑实现一下" read as
// debugging because it contains 日志 — and each mislabel force-injected an
// entire irrelevant skill.
//
// What survives is fact-driven: whether the repository has tests, a CI config,
// a recent commit, any source at all. Those are probed and verifiable, so the
// engine may state them. Which skill to load given those facts is the model's
// call, through the skill tool.

// BuildAutoActivateSkills returns skill names to force-activate from strong,
// verifiable workspace signals. This compensates for wesgine implementing only
// SkillHints suppression (Priority<0.3) without promotion.
func BuildAutoActivateSkills(ctx *WorkspaceContext) []string {
	var skills []string
	if IsLegacy(ctx) {
		skills = append(skills, "legacy-navigation")
	}
	if IsEmpty(ctx) {
		skills = append(skills, "system-design")
	}
	return skills
}

// BuildSkillHints produces priority hints for the skill system from workspace
// context signals. Hints are advisory — wesgine currently implements only
// suppression (Priority<0.3); promotion hints document intent.
func BuildSkillHints(ctx *WorkspaceContext) []wesgine.SkillPriorityHint {
	var hints []wesgine.SkillPriorityHint

	if IsLegacy(ctx) {
		hints = append(hints,
			wesgine.SkillPriorityHint{SkillName: "legacy-navigation", Priority: 1.8, Reason: "legacy project detected"},
			wesgine.SkillPriorityHint{SkillName: "investigation", Priority: 1.5, Reason: "legacy: thorough investigation needed"},
			wesgine.SkillPriorityHint{SkillName: "agentic-execution", Priority: 0.5, Reason: "legacy: act-first is risky"},
		)
	}

	if IsEmpty(ctx) {
		hints = append(hints,
			wesgine.SkillPriorityHint{SkillName: "system-design", Priority: 1.8, Reason: "empty project: bootstrap mode"},
		)
	}

	if IsMigration(ctx) {
		hints = append(hints,
			wesgine.SkillPriorityHint{SkillName: "refactoring", Priority: 1.5, Reason: "migration in progress"},
			wesgine.SkillPriorityHint{SkillName: "investigation", Priority: 1.5, Reason: "migration: understand scope first"},
		)
	}

	return hints
}
