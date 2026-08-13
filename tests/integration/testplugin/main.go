// testplugin is a minimal pacto plugin for e2e testing.
// It reads a GenerateRequest from stdin and writes a GenerateResponse with
// two generated files (deployment.yaml and service.yaml) to stdout.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type generateRequest struct {
	ProtocolVersion string `json:"protocolVersion"`
	Contract        struct {
		Service struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"service"`
	} `json:"contract"`
	OutputDir string `json:"outputDir"`
}

type generatedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type generateResponse struct {
	Files   []generatedFile `json:"files"`
	Message string          `json:"message,omitempty"`
}

func main() {
	var req generateRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "failed to decode input: %v", err)
		os.Exit(1)
	}

	name := req.Contract.Service.Name
	version := req.Contract.Service.Version

	resp := generateResponse{
		Files: []generatedFile{
			{
				Path: "deployment.yaml",
				Content: fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  labels:
    app: %s
    version: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
        version: %s
    spec:
      containers:
        - name: %s
          image: %s:%s
`, name, name, version, name, name, version, name, name, version),
			},
			{
				Path: "service.yaml",
				Content: fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
spec:
  selector:
    app: %s
  ports:
    - port: 80
      targetPort: 8080
`, name, name),
			},
		},
		Message: fmt.Sprintf("Generated manifests for %s@%s", name, version),
	}

	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode output: %v", err)
		os.Exit(1)
	}
}
