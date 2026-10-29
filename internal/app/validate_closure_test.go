package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const closurePolicySchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["service"],
  "properties": {
    "service": {
      "type": "object",
      "required": ["owner"],
      "properties": {"owner": {"type": "object", "required": ["dri"]}}
    }
  }
}`

// writeClosureFile writes content to root/rel, creating parent directories.
func writeClosureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// closureService renders a contract with an optional DRI and dependencies.
func closureService(name, dri string, deps ...string) string {
	var b strings.Builder
	b.WriteString("pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: 1.0.0\n  owner:\n    team: " + name + "\n")
	if dri != "" {
		b.WriteString("    dri: " + dri + "\n")
	}
	if len(deps) > 0 {
		b.WriteString("dependencies:\n")
		for _, d := range deps {
			b.WriteString("  - name: " + d + "\n    ref: ../" + d + "\n    compatibility: ^1.0.0\n    required: true\n")
		}
	}
	return b.String()
}

func TestValidate_Closure(t *testing.T) {
	rootPolicy := "policies:\n  - name: platform-policy\n    ref: ../platform-policy\n"

	tests := []struct {
		name      string
		closure   bool
		ledgerDRI string
		rootDeps  []string
		extra     map[string]string // additional bundles: dir -> pacto.yaml
		wantValid bool
		wantCodes []string // codes of the closure findings, in order
		wantPath  string   // path of the first closure finding
	}{
		{
			name: "clean closure passes", closure: true, ledgerDRI: "bob@example.com",
			rootDeps: []string{"ledger"}, wantValid: true,
		},
		{
			name: "dependency violating a root policy fails and is named", closure: true,
			rootDeps: []string{"ledger"}, wantCodes: []string{"CLOSURE_POLICY_VIOLATION"},
			wantPath: "dependencies[ledger@1.0.0]",
		},
		{
			name: "unresolvable dependency fails closed", closure: true, ledgerDRI: "bob@example.com",
			rootDeps: []string{"ledger", "missing"}, wantCodes: []string{"CLOSURE_DEPENDENCY_UNRESOLVED"},
			wantPath: "dependencies",
		},
		{
			name: "transitive violation in a diamond is reported once", closure: true,
			rootDeps:  []string{"ledger", "audit"},
			extra:     map[string]string{"audit": closureService("audit", "carol@example.com", "ledger")},
			wantCodes: []string{"CLOSURE_POLICY_VIOLATION"},
			wantPath:  "dependencies[ledger@1.0.0]",
		},
		{
			name: "dependency cycle fails closed", closure: true,
			rootDeps:  []string{"ledger"},
			extra:     map[string]string{"ledger": closureService("ledger", "bob@example.com", "payments")},
			wantCodes: []string{"CLOSURE_DEPENDENCY_UNRESOLVED"},
			// The cycle closes at payments reached again through ledger.
			wantPath: "dependencies[payments@1.0.0]",
		},
		{
			name: "transitive unresolvable dependency names its declarer", closure: true,
			rootDeps:  []string{"ledger"},
			extra:     map[string]string{"ledger": closureService("ledger", "bob@example.com", "missing")},
			wantCodes: []string{"CLOSURE_DEPENDENCY_UNRESOLVED"},
			wantPath:  "dependencies[ledger@1.0.0]",
		},
		{
			name: "two bundles sharing a name and version are both checked", closure: true,
			ledgerDRI: "bob@example.com", rootDeps: []string{"ledger", "ledger-copy"},
			extra:     map[string]string{"ledger-copy": closureService("ledger", "")},
			wantCodes: []string{"CLOSURE_POLICY_VIOLATION"},
			wantPath:  "dependencies[ledger@1.0.0]",
		},
		{
			name: "without the flag dependencies are not checked", closure: false,
			rootDeps: []string{"ledger", "missing"}, wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeClosureFile(t, root, "platform-policy/pacto.yaml", closureService("platform-policy", ""))
			writeClosureFile(t, root, "platform-policy/policy/schema.json", closurePolicySchema)
			writeClosureFile(t, root, "ledger/pacto.yaml", closureService("ledger", tt.ledgerDRI))
			for dir, y := range tt.extra {
				writeClosureFile(t, root, dir+"/pacto.yaml", y)
			}
			writeClosureFile(t, root, "payments/pacto.yaml",
				closureService("payments", "alice@example.com", tt.rootDeps...)+rootPolicy)

			svc := NewService(&mockBundleStore{}, nil)
			res, err := svc.Validate(context.Background(), ValidateOptions{
				Path: filepath.Join(root, "payments"), Closure: tt.closure,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Valid != tt.wantValid {
				t.Fatalf("valid = %v, want %v (errors: %v)", res.Valid, tt.wantValid, res.Errors)
			}
			var codes []string
			for _, e := range res.Errors {
				codes = append(codes, e.Code)
			}
			if strings.Join(codes, ",") != strings.Join(tt.wantCodes, ",") {
				t.Fatalf("codes = %v, want %v (errors: %v)", codes, tt.wantCodes, res.Errors)
			}
			if tt.wantPath != "" && res.Errors[0].Path != tt.wantPath {
				t.Errorf("path = %q, want %q", res.Errors[0].Path, tt.wantPath)
			}
			if tt.wantPath == "dependencies[ledger@1.0.0]" && !strings.Contains(res.Errors[0].Message, "ledger@1.0.0") {
				t.Errorf("message does not name the dependency: %q", res.Errors[0].Message)
			}
		})
	}
}

// A root that violates its own policy still fails with the plain
// POLICY_VIOLATION, and closure findings are added after it.
func TestValidate_Closure_RootViolationKept(t *testing.T) {
	root := t.TempDir()
	writeClosureFile(t, root, "platform-policy/pacto.yaml", closureService("platform-policy", ""))
	writeClosureFile(t, root, "platform-policy/policy/schema.json", closurePolicySchema)
	writeClosureFile(t, root, "ledger/pacto.yaml", closureService("ledger", ""))
	writeClosureFile(t, root, "payments/pacto.yaml",
		closureService("payments", "", "ledger")+"policies:\n  - name: platform-policy\n    ref: ../platform-policy\n")

	res, err := NewService(&mockBundleStore{}, nil).Validate(context.Background(),
		ValidateOptions{Path: filepath.Join(root, "payments"), Closure: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 2 || res.Errors[0].Code != "POLICY_VIOLATION" || res.Errors[1].Code != "CLOSURE_POLICY_VIOLATION" {
		t.Fatalf("errors = %v", res.Errors)
	}
}
