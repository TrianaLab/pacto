//go:build !windows

package discover

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWalkReportsUnreadableThroughOnErr(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	root := walkFixture(t, map[string]string{"a/pacto.yaml": "", "blocked/b/pacto.yaml": "", "c/pacto.yaml": ""})
	blocked := filepath.Join(root, "blocked")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	var seen []string
	res, err := Walk(context.Background(), root, func(p string, _ error) error {
		seen = append(seen, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []string{blocked}) {
		t.Errorf("onErr saw %v, want [%s]", seen, blocked)
	}
	if got := relDirs(t, root, res.Dirs); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("Dirs = %v", got)
	}
}
