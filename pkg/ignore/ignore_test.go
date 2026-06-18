package ignore

import "testing"

func TestMatcherIgnored(t *testing.T) {
	tests := []struct {
		name  string
		extra []string
		path  string
		isDir bool
		want  bool
	}{
		{"default git dir", nil, ".git", true, true},
		{"default ds_store at depth", nil, "docs/.DS_Store", false, true},
		{"lock shipped by default", nil, "pacto.lock", false, false},
		{"lock still ignorable when opted out", []string{"pacto.lock"}, "pacto.lock", false, true},
		{"default ignore file", nil, ".pactoignore", false, true},
		{"pacto.yaml never ignored", []string{"*.yaml"}, "pacto.yaml", false, false},
		{"basename glob any depth", []string{"*.tmp"}, "build/x.tmp", false, true},
		{"anchored only at root", []string{"/secrets"}, "sub/secrets", true, false},
		{"anchored matches root", []string{"/secrets"}, "secrets", true, true},
		{"doublestar", []string{"a/**/c"}, "a/b/b2/c", false, true},
		{"dir-only pattern", []string{"node_modules/"}, "node_modules", true, true},
		{"dir-only does not match file", []string{"node_modules/"}, "node_modules", false, false},
		{"negation re-includes", []string{"*.log", "!keep.log"}, "keep.log", false, false},
		{"negation order matters", []string{"!keep.log", "*.log"}, "keep.log", false, true},
		{"comment and blank ignored", []string{"# a comment", "", "*.bak"}, "x.bak", false, true},
		{"plain file no match", []string{"*.tmp"}, "main.go", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(tt.extra)
			if got := m.Ignored(tt.path, tt.isDir); got != tt.want {
				t.Errorf("Ignored(%q,%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
			}
		})
	}
}
