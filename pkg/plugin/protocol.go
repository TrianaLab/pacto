package plugin

import "github.com/trianalab/pacto/pkg/contract"

// ProtocolVersion is the current plugin protocol version.
const ProtocolVersion = "1"

// GenerateRequest is the JSON payload written to a plugin's stdin.
type GenerateRequest struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Contract        *contract.Contract `json:"contract"`
	BundleDir       string             `json:"bundleDir"`
	OutputDir       string             `json:"outputDir"`
	Options         map[string]any     `json:"options,omitempty"`
}

// GenerateResponse is the JSON payload read from a plugin's stdout.
type GenerateResponse struct {
	Files   []GeneratedFile `json:"files"`
	Message string          `json:"message,omitempty"`
}

// GeneratedFile describes a single file produced by a plugin.
type GeneratedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
