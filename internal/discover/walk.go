package discover

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/ignore"
)

// maxScanDepth bounds how deep Walk descends below its root.
const maxScanDepth = 8

// Skip is a folder the walk did not descend into and says so: one the root
// .pactoignore hid, or one the depth cap cut.
type Skip struct {
	Rel  string // slash path below the root
	Rule string // ".pactoignore:<line>" or "depth limit 8"
}

// WalkResult is what one repository walk found.
type WalkResult struct {
	Dirs    []string // every directory holding a pacto.yaml file, in walk (lexical) order
	Skipped []Skip   // .pactoignore and depth-cap skips; hidden, node_modules and vendor are never listed
}

// Walk finds every service folder under root. It skips hidden directories
// (except the root), node_modules, vendor, every directory the root
// .pactoignore ignores and anything past the depth cap; the last two it
// records in Skipped with the deciding rule, so a cut folder is named. Only directories are asked, never
// pacto.yaml, so a file pattern never hides a service. Walk errors go to
// onErr, whose result the walk returns to filepath.WalkDir.
//
// ponytail: one root .pactoignore; nested .pactoignore files stay packaging-only (D2).
func Walk(ctx context.Context, root string, onErr func(path string, err error) error) (WalkResult, error) {
	m, err := ignore.Load(os.DirFS(root))
	if err != nil {
		return WalkResult{}, fmt.Errorf("read %s: %w", ignore.IgnoreFileName, err)
	}
	var res WalkResult
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return onErr(p, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			if d.Name() == "pacto.yaml" {
				res.Dirs = append(res.Dirs, filepath.Dir(p))
			}
			return nil
		}
		if skipDir(root, p, d) {
			return fs.SkipDir
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p) // skipDir already refused a path Rel cannot compute
		rel = filepath.ToSlash(rel)
		if line, ignored := m.IgnoredBy(rel, true); ignored && line > 0 {
			res.Skipped = append(res.Skipped, Skip{Rel: rel, Rule: ignore.IgnoreFileName + ":" + strconv.Itoa(line)})
			return fs.SkipDir
		}
		if strings.Count(rel, "/") >= maxScanDepth {
			res.Skipped = append(res.Skipped, Skip{Rel: rel, Rule: "depth limit " + strconv.Itoa(maxScanDepth)})
			return fs.SkipDir
		}
		return nil
	})
	return res, err
}

// skipDir reports whether a directory is silently not descended into: hidden
// dirs (except the root itself) and well-known vendor/dependency dirs. Walk
// applies the depth cap itself, because it lists that cut.
func skipDir(root, p string, d fs.DirEntry) bool {
	if p == root {
		return false
	}
	name := d.Name()
	if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
		return true
	}
	_, err := filepath.Rel(root, p)
	return err != nil
}
