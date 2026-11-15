package dashboard

import (
	"os"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestResourceActionsNoteMatchesFrontend: the service page's Resources footer
// copies fleet.ResourceActionsNote word for word, since the frontend cannot
// import it, and the CLI and MCP read the constant itself.
func TestResourceActionsNoteMatchesFrontend(t *testing.T) {
	src, err := os.ReadFile("frontend/src/views/entity/ServiceEntity.svelte")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), fleet.ResourceActionsNote) {
		t.Fatalf("ServiceEntity.svelte does not carry fleet.ResourceActionsNote %q", fleet.ResourceActionsNote)
	}
}
