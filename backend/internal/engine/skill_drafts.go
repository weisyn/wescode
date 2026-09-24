package engine

import (
	"fmt"
	"os"
	"path/filepath"
)

// SkillHealthReport summarizes compatibility issues for installed skills.
type SkillHealthReport struct {
	Total   int                `json:"total"`
	Healthy int                `json:"healthy"`
	Issues  []SkillHealthIssue `json:"issues"`
}

type SkillHealthIssue struct {
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Problems []string `json:"problems"`
}

// SkillHealth checks all installed skills for compatibility issues.
func (s *Service) SkillHealth() (*SkillHealthReport, error) {
	skills, err := s.ListSkills()
	if err != nil {
		return nil, err
	}

	var registeredTools map[string]struct{}
	s.mu.Lock()
	cell := s.cell
	s.mu.Unlock()
	if cell != nil {
		names := cell.Tools().Names()
		registeredTools = make(map[string]struct{}, len(names))
		for _, n := range names {
			registeredTools[n] = struct{}{}
		}
	}

	report := &SkillHealthReport{Total: len(skills)}
	for _, sk := range skills {
		var problems []string

		if sk.Name == "" {
			problems = append(problems, "missing name in frontmatter")
		}
		if sk.Description == "" {
			problems = append(problems, "missing description")
		}

		if registeredTools != nil {
			for _, op := range sk.Operators {
				if _, ok := registeredTools[op]; !ok {
					problems = append(problems, fmt.Sprintf("operator %q not registered", op))
				}
			}
		}

		if len(problems) > 0 {
			report.Issues = append(report.Issues, SkillHealthIssue{
				Slug:     sk.Slug,
				Name:     sk.Name,
				Problems: problems,
			})
		} else {
			report.Healthy++
		}
	}
	return report, nil
}

// SkillStatusReport returns engine skill injection snapshot info.
type SkillStatusReport struct {
	InstalledCount int              `json:"installedCount"`
	EnabledCount   int              `json:"enabledCount"`
	Skills         []SkillStatusRow `json:"skills"`
}

type SkillStatusRow struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	TokenEstimate int    `json:"tokenEstimate"`
}

// SkillStatus returns a summary of installed skills with token estimates.
func (s *Service) SkillStatus() (*SkillStatusReport, error) {
	skills, err := s.ListSkills()
	if err != nil {
		return nil, err
	}

	report := &SkillStatusReport{
		InstalledCount: len(skills),
		Skills:         make([]SkillStatusRow, 0, len(skills)),
	}
	for _, sk := range skills {
		if sk.Enabled {
			report.EnabledCount++
		}
		tokens := 0
		if data, err := os.ReadFile(sk.Path); err == nil {
			tokens = len(data) / 4
		}
		report.Skills = append(report.Skills, SkillStatusRow{
			Slug:          sk.Slug,
			Name:          sk.Name,
			Enabled:       sk.Enabled,
			TokenEstimate: tokens,
		})
	}
	return report, nil
}

// CheckSkillBindingsResult reports which slugs are installed and which are not.
type CheckSkillBindingsResult struct {
	Installed    []string `json:"installed"`
	NotInstalled []string `json:"notInstalled"`
}

// CheckSkillBindings checks which skill slugs from the given list are installed.
func (s *Service) CheckSkillBindings(slugs []string) (*CheckSkillBindingsResult, error) {
	skills, err := s.ListSkills()
	if err != nil {
		return nil, err
	}
	installed := make(map[string]struct{}, len(skills))
	for _, sk := range skills {
		installed[sk.Slug] = struct{}{}
	}

	result := &CheckSkillBindingsResult{
		Installed:    make([]string, 0),
		NotInstalled: make([]string, 0),
	}
	for _, slug := range slugs {
		if _, ok := installed[slug]; ok {
			result.Installed = append(result.Installed, slug)
		} else {
			result.NotInstalled = append(result.NotInstalled, slug)
		}
	}
	return result, nil
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
