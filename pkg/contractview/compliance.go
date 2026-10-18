package contractview

// ComputeCompliance maps a contract status onto the compliance badge. Compliance
// comes from the engine's findings, which the contract status already ranks.
func ComputeCompliance(cs ContractStatus) *ComplianceInfo {
	switch cs {
	case StatusReference, StatusNotEvaluated:
		return &ComplianceInfo{Status: ComplianceReference}
	case StatusInvalid, StatusNonCompliant:
		return &ComplianceInfo{Status: ComplianceError}
	case StatusUnknown:
		return &ComplianceInfo{Status: ComplianceUnknown}
	case StatusWarning:
		return &ComplianceInfo{Status: ComplianceWarning}
	default:
		return &ComplianceInfo{Status: ComplianceOK}
	}
}
