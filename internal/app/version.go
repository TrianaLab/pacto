package app

// buildVersion is the CLI version recorded into pacto.lock provenance.
func (s *Service) buildVersion() string {
	if s.Version == "" {
		return "dev"
	}
	return s.Version
}
