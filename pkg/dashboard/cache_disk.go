package dashboard

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// userHomeDir is a package-level variable so tests can override os.UserHomeDir.
var userHomeDir = os.UserHomeDir

// DiskCache implements Cache backed by the filesystem at $HOME/.cache/pacto.
// It stores entries as JSON files with optional TTL metadata.
type DiskCache struct {
	root string // e.g. $HOME/.cache/pacto
}

type diskEntry struct {
	Value     json.RawMessage `json:"value"`
	ExpiresAt *time.Time      `json:"expiresAt,omitempty"`
}

// NewDiskCache creates a disk-backed cache rooted at the given directory.
// It creates the directory structure if it doesn't exist.
func NewDiskCache(root string) (*DiskCache, error) {
	for _, sub := range []string{"oci", "index", "metadata"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return nil, fmt.Errorf("creating cache dir %s: %w", sub, err)
		}
	}
	return &DiskCache{root: root}, nil
}

// DefaultCacheDir returns $HOME/.cache/pacto.
func DefaultCacheDir() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "pacto"), nil
}

func (c *DiskCache) Get(key string) (any, bool) {
	path := c.keyPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	var entry diskEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}

	// Check TTL.
	if entry.ExpiresAt != nil && time.Now().After(*entry.ExpiresAt) {
		_ = os.Remove(path)
		return nil, false
	}

	// Return raw JSON for the caller to unmarshal.
	return entry.Value, true
}

func (c *DiskCache) Set(key string, value any, ttl time.Duration) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}

	entry := diskEntry{Value: data}
	if ttl > 0 {
		exp := time.Now().Add(ttl)
		entry.ExpiresAt = &exp
	}

	// entry contains only json.RawMessage and *time.Time, both always marshallable.
	entryData, _ := json.Marshal(entry)

	path := c.keyPath(key)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, entryData, 0o644)
}

// setImmutable stores a value with no expiration (for OCI bundles keyed by digest).
func (c *DiskCache) setImmutable(key string, value any) {
	c.Set(key, value, 0)
}

func (c *DiskCache) keyPath(key string) string {
	h := sha256.Sum256([]byte(key))
	hex := fmt.Sprintf("%x", h)
	// Use first 2 chars as subdirectory to avoid flat directory with too many files.
	return filepath.Join(c.root, "index", hex[:2], hex+".json")
}

// ociBundlePath returns the path for storing an OCI bundle by digest.
func (c *DiskCache) ociBundlePath(digest string) string {
	return filepath.Join(c.root, "oci", digest+".json")
}

// getOCIBundle retrieves a cached OCI bundle by digest.
func (c *DiskCache) getOCIBundle(digest string) (json.RawMessage, bool) {
	path := c.ociBundlePath(digest)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// setOCIBundle stores an OCI bundle by digest (immutable, no TTL).
func (c *DiskCache) setOCIBundle(digest string, data []byte) {
	path := c.ociBundlePath(digest)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

// setMetadata stores metadata (e.g. tag-to-digest mappings).
func (c *DiskCache) setMetadata(key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	path := filepath.Join(c.root, "metadata", key+".json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

// getMetadata retrieves stored metadata.
func (c *DiskCache) getMetadata(key string, dest any) bool {
	path := filepath.Join(c.root, "metadata", key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, dest) == nil
}
