package skills

import (
	"testing"
	"testing/fstest"
)

func TestListAndRead(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/refund_customer.md":  {Data: []byte("# Refund\nsteps")},
		"skills/onboard_customer.md": {Data: []byte("# Onboard")},
		"skills/notes.txt":           {Data: []byte("ignored")},
		"pacto.yaml":                 {Data: []byte("x")},
	}
	names, err := List(fsys)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 || names[0] != "onboard_customer.md" || names[1] != "refund_customer.md" {
		t.Fatalf("names = %v (want sorted .md only)", names)
	}
	content, err := Read(fsys, "refund_customer.md")
	if err != nil || content != "# Refund\nsteps" {
		t.Fatalf("Read content=%q err=%v", content, err)
	}
	if _, err := Read(fsys, "../pacto.yaml"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := Read(fsys, "nope.md"); err == nil {
		t.Fatal("expected missing-skill error")
	}
}

func TestListWithSubdir(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/a.md":     {Data: []byte("a")},
		"skills/sub/b.md": {Data: []byte("b")}, // creates a "sub" dir entry
	}
	names, err := List(fsys)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 1 || names[0] != "a.md" {
		t.Fatalf("names = %v (dir must be skipped)", names)
	}
}

func TestListNoDir(t *testing.T) {
	names, err := List(fstest.MapFS{"pacto.yaml": {Data: []byte("x")}})
	if err != nil || names != nil {
		t.Fatalf("expected nil,nil got %v,%v", names, err)
	}
}
