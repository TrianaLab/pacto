package capability

import (
	"testing"
	"testing/fstest"
)

func TestSkillsAndSkill(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/refund_customer.md":  {Data: []byte("# Refund\nsteps")},
		"skills/onboard_customer.md": {Data: []byte("# Onboard")},
		"skills/notes.txt":           {Data: []byte("ignored")},
		"pacto.yaml":                 {Data: []byte("x")},
	}
	names, err := Skills(fsys)
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(names) != 2 || names[0] != "onboard_customer.md" || names[1] != "refund_customer.md" {
		t.Fatalf("names = %v (want sorted .md only)", names)
	}
	content, err := Skill(fsys, "refund_customer.md")
	if err != nil || content != "# Refund\nsteps" {
		t.Fatalf("Skill content=%q err=%v", content, err)
	}
	if _, err := Skill(fsys, "../pacto.yaml"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := Skill(fsys, "nope.md"); err == nil {
		t.Fatal("expected missing-skill error")
	}
}

func TestSkillsWithSubdir(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/a.md":     {Data: []byte("a")},
		"skills/sub/b.md": {Data: []byte("b")}, // creates a "sub" dir entry
	}
	names, err := Skills(fsys)
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(names) != 1 || names[0] != "a.md" {
		t.Fatalf("names = %v (dir must be skipped)", names)
	}
}

func TestSkillsNoDir(t *testing.T) {
	names, err := Skills(fstest.MapFS{"pacto.yaml": {Data: []byte("x")}})
	if err != nil || names != nil {
		t.Fatalf("expected nil,nil got %v,%v", names, err)
	}
}
