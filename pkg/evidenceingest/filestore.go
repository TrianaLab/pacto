package evidenceingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Filesystem seams, overridable in tests to exercise the atomic-write error
// paths without a real fault-injecting filesystem.
var (
	fsWriteFile = os.WriteFile
	fsRename    = os.Rename
	fsReadFile  = os.ReadFile
	fsMarshal   = func(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
)

// FileStore is a file-backed [Store] for local and demo operation: one JSON file
// per target key (last write wins), written atomically (temp file + rename) so a
// crash never leaves a partial record. It is safe for concurrent use within one
// process.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore returns a file store rooted at dir, creating it if needed.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

// path maps a target key to a stable filename (content-hashed so the key's "/"
// separators never escape the store directory).
func (f *FileStore) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:])+".json")
}

// Put writes the record atomically (write a sibling temp file, then rename).
func (f *FileStore) Put(_ context.Context, key string, rec Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := fsMarshal(rec)
	if err != nil {
		return err
	}
	final := f.path(key)
	tmp := final + ".tmp"
	if err := fsWriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := fsRename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// List reads every stored record in deterministic filename order.
func (f *FileStore) List(_ context.Context) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fsReadFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}
