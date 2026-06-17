package lock

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/fs"
	"sort"
)

// HashFS returns a deterministic "sha256:<hex>" digest over every regular file
// in fsys, independent of walk order. Used for local (non-OCI) dependencies.
func HashFS(fsys fs.FS) (string, error) {
	type file struct {
		path string
		data []byte
	}
	var files []file
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return rerr
		}
		files = append(files, file{path: p, data: data})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	h := sha256.New()
	var lenbuf [8]byte
	for _, f := range files {
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(f.path)))
		_, _ = h.Write(lenbuf[:])
		_, _ = h.Write([]byte(f.path))
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(f.data)))
		_, _ = h.Write(lenbuf[:])
		_, _ = h.Write(f.data)
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}
