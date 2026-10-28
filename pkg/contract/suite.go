package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// SuiteDigest is the sha256 of the suite a check ships: every file in the
// directory that holds its file, not the file alone, since a runner can read
// cases, fixtures or golden answers from beside it. It is the sha256 of what
// `sha256sum` prints for those files, sorted by their path relative to that
// directory:
//
//	cd <dir> && find . -type f | sed 's|^\./||' | LC_ALL=C sort | xargs sha256sum | sha256sum
//
// A suite must therefore be self-contained in its own directory; one at the
// bundle root takes in the whole bundle. An error is a file that cannot be
// read, the suite's own file included.
func SuiteDigest(fsys fs.FS, file string) (string, error) {
	if _, err := fs.Stat(fsys, file); err != nil {
		return "", err
	}
	dir := path.Dir(path.Clean(file))
	var lines [][2]string // path, line
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel := strings.TrimPrefix(p, dir+"/")
		if dir == "." {
			rel = p
		}
		lines = append(lines, [2]string{rel, fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), rel)})
		return nil
	})
	if err != nil {
		return "", err
	}
	// Byte order of the whole path, as LC_ALL=C sort: WalkDir's per-directory
	// order puts b/x before b.txt.
	slices.SortFunc(lines, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l[1])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
