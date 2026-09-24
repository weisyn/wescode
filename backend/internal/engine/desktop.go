package engine

import "github.com/weisyn/wesapp/desktop"

// GetDesktopSettings returns the current desktop automation settings as wesui View.
func (s *Service) GetDesktopSettings() desktop.SettingsView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return desktop.ViewSettings(desktop.Settings{Enabled: s.cfg.DesktopEnabled})
}

// SetDesktopEnabled enables or disables desktop automation and persists to config.
func (s *Service) SetDesktopEnabled(enabled bool) (restartRequired bool, err error) {
	s.mu.Lock()
	changed := s.cfg.DesktopEnabled != enabled
	s.cfg.DesktopEnabled = enabled
	cfg := s.cfg
	s.mu.Unlock()

	if changed {
		if err := saveConfig(cfg); err != nil {
			return false, err
		}
	}
	return changed, nil
}
