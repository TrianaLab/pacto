package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestSuiteDigest(t *testing.T) {
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	fsys := fstest.MapFS{
		"pacto.yaml":          {Data: []byte("contract")},
		"evals/suite.yaml":    {Data: []byte("suite")},
		"evals/b.txt":         {Data: []byte("b")},
		"evals/b/cases.csv":   {Data: []byte("cases")},
		"evals/link":          {Data: []byte("x"), Mode: fs.ModeSymlink},
		"other/unrelated.txt": {Data: []byte("u")},
	}
	// What sha256sum prints, sorted the way LC_ALL=C sort does: b.txt before b/cases.csv.
	want := "sha256:" + sum(sum("b")+"  b.txt\n"+sum("cases")+"  b/cases.csv\n"+sum("suite")+"  suite.yaml\n")
	got, err := SuiteDigest(fsys, "evals/suite.yaml")
	if err != nil || got != want {
		t.Fatalf("SuiteDigest = %q %v, want %q", got, err, want)
	}
	// A case file beside the suite is part of it.
	fsys["evals/b/cases.csv"] = &fstest.MapFile{Data: []byte("weaker")}
	if again, _ := SuiteDigest(fsys, "evals/suite.yaml"); again == got {
		t.Fatal("a sibling case file changed but the suite digest did not")
	}
	// A suite at the bundle root takes in the whole bundle.
	fsys["root.yaml"] = &fstest.MapFile{Data: []byte("r")}
	root, err := SuiteDigest(fsys, "root.yaml")
	if err != nil || root == got {
		t.Fatalf("root suite = %q %v", root, err)
	}
	if _, err := SuiteDigest(fsys, "missing.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing suite file: %v", err)
	}
	if _, err := SuiteDigest(badFS{fsys}, "evals/suite.yaml"); err == nil {
		t.Fatal("an unreadable file must fail the digest")
	}
}

// badFS lists every file but reads none but the suite's own.
type badFS struct{ fstest.MapFS }

func (b badFS) ReadFile(name string) ([]byte, error) {
	if name == "evals/b.txt" {
		return nil, errors.New("unreadable")
	}
	return b.MapFS.ReadFile(name)
}
