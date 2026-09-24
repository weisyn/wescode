package engine

// GetRunSettings returns the run configuration the next Run will enforce.
//
// Normalization lives in RunSettings.Resolve, shared with run.go, so the number
// shown here is the number that binds.
func (s *Service) GetRunSettings() RunSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Run.Resolve()
}

// UpdateRunSettings merges a field-level patch into config.yaml.
//
// Merging rather than replacing is what keeps the three UI save entry points
// independent: each sends only the field it owns, and the others survive.
func (s *Service) UpdateRunSettings(p RunSettingsPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Run = s.cfg.Run.Apply(p)
	return saveConfig(s.cfg)
}
