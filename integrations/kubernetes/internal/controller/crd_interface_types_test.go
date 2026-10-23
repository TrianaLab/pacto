/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/


package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestPactoCRDAcceptsEveryInterfaceType: the status names each declared
// interface's type, so a type the CRD's enum lacks fails every status write of
// that Pacto (an agent contract with an mcp interface was never reconciled).
func TestPactoCRDAcceptsEveryInterfaceType(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "pacto.trianalab.io_pactos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{contract.InterfaceTypeOpenAPI, contract.InterfaceTypeAsyncAPI, contract.InterfaceTypeGRPC, contract.InterfaceTypeMCP} {
		if !strings.Contains(string(raw), "\n                      - "+typ+"\n") {
			t.Errorf("the Pacto CRD's interface type enum lacks %q", typ)
		}
	}
}
