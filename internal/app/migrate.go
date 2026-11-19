package app

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// MigrateOptions are the inputs of pacto migrate. Empty Roots is the current
// directory; empty To is contract.Current().
type MigrateOptions struct {
	Roots  []string
	To     string
	DryRun bool
}

// MigrateStatus is what pacto migrate did with one pacto.yaml.
type MigrateStatus string

// The statuses of a MigratedFile.
const (
	MigrateRewritten MigrateStatus = "rewritten" // raised (or, under --dry-run, would be)
	MigrateCurrent   MigrateStatus = "current"   // already at or past the target
	MigrateKept      MigrateStatus = "kept"      // behind, and left as written: Message says why
	MigrateManual    MigrateStatus = "manual"    // 1.x, migrated by a person
	MigrateFailed    MigrateStatus = "failed"    // could not be read or written
)

// MigratedFile is one pacto.yaml (or one folder that could not be read) and
// what pacto migrate did with it. Message is the line printed after the path.
type MigratedFile struct {
	Path    string                    `json:"path"`
	Status  MigrateStatus             `json:"status"`
	From    string                    `json:"from,omitempty"`
	To      string                    `json:"to,omitempty"`
	Finding *contract.ValidationError `json:"finding,omitempty"`
	Message string                    `json:"message"`
}

// SkippedDir is a folder the walk named and did not enter, with the rule
// that cut it (".pactoignore:<line>" or "depth limit 8").
type SkippedDir struct {
	Path string `json:"path"`
	Rule string `json:"rule"`
}

// MigrateResult is what one pacto migrate run did.
type MigrateResult struct {
	To          string         `json:"to"`
	DryRun      bool           `json:"dryRun"`
	Files       []MigratedFile `json:"files"`
	SkippedDirs []SkippedDir   `json:"skippedDirs,omitempty"`
	NotFound    []string       `json:"notFound,omitempty"`
	Rewritten   int            `json:"rewritten"`
	Current     int            `json:"current"`
	Kept        int            `json:"kept"`
	Manual      int            `json:"manual"`
	Skipped     int            `json:"skipped"`
	Failed      int            `json:"failed"`
}

// add appends f and counts its status.
func (r *MigrateResult) add(f MigratedFile) {
	r.Files = append(r.Files, f)
	switch f.Status {
	case MigrateRewritten:
		r.Rewritten++
	case MigrateCurrent:
		r.Current++
	case MigrateKept:
		r.Kept++
	case MigrateManual:
		r.Manual++
	case MigrateFailed:
		r.Failed++
	}
}

// Summary is the last line of the text output.
func (r *MigrateResult) Summary() string {
	s := fmt.Sprintf("%d rewritten, %d current, %d kept, %d manual", r.Rewritten, r.Current, r.Kept, r.Manual)
	if r.Skipped > 0 {
		s += fmt.Sprintf(", %d skipped", r.Skipped)
	}
	if r.Failed > 0 {
		s += fmt.Sprintf(", %d failed", r.Failed)
	}
	if r.DryRun {
		s += " (dry run: nothing written)"
	}
	return s
}

// Migrate raises every pacto.yaml under opts.Roots to opts.To through
// contract.Upgrade, which raises a file only when validation finds nothing
// new, and writes each raised file atomically. It walks with the walker the
// local fleet source uses. It returns an error only for an
// unsupported target or a cancelled context; a file that cannot be read or
// written is a failed row.
func (s *Service) Migrate(ctx context.Context, opts MigrateOptions) (*MigrateResult, error) {
	to := cmp.Or(opts.To, contract.Current())
	if !contract.Known(to) {
		return nil, fmt.Errorf("--to %q is not a supported pactoVersion; supported: %s", to, contract.Supported())
	}
	roots := opts.Roots
	if len(roots) == 0 {
		roots = []string{"."}
	}
	res := &MigrateResult{To: to, DryRun: opts.DryRun, Files: []MigratedFile{}}
	seen := map[string]bool{} // ponytail: map of seen folders, fine for any repository
	for _, root := range roots {
		failed := res.Failed
		w, err := discover.Walk(ctx, root, func(p string, err error) error {
			res.add(failedRow(p, "cannot read", err))
			return fs.SkipDir
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			res.add(failedRow(root, "cannot read "+ignore.IgnoreFileName, err)) // the root .pactoignore, Walk's only other error
			continue
		}
		for _, sk := range w.Skipped {
			res.SkippedDirs = append(res.SkippedDirs, SkippedDir{Path: filepath.Join(root, filepath.FromSlash(sk.Rel)), Rule: sk.Rule})
		}
		if len(w.Dirs) == 0 && res.Failed == failed {
			res.NotFound = append(res.NotFound, root)
		}
		for _, dir := range w.Dirs {
			shown := filepath.Join(dir, DefaultContractPath)
			abs, err := s.env().absPath(dir)
			if err != nil {
				res.add(failedRow(shown, "cannot read", err))
				continue
			}
			if !seen[abs] {
				seen[abs] = true
				res.add(s.migrateFile(ctx, dir, shown, to, opts.DryRun))
			}
		}
	}
	res.Skipped = len(res.SkippedDirs)
	return res, nil
}

// failedRow is the failed row of path: what could not be done and why, with
// the *fs.PathError wrapper dropped so no absolute path is printed.
func failedRow(path, what string, err error) MigratedFile {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return MigratedFile{Path: path, Status: MigrateFailed, Message: what + ": " + err.Error()}
}

// migrateFile raises dir's pacto.yaml, shown as shown, to to and writes it
// unless dryRun. Anything Upgrade cannot vouch for leaves the file as it is.
func (s *Service) migrateFile(ctx context.Context, dir, shown, to string, dryRun bool) MigratedFile {
	path := filepath.Join(dir, DefaultContractPath)
	fi, raw, stop := readContract(path, shown)
	if stop != nil {
		return *stop
	}
	m, err := ignore.Load(os.DirFS(dir))
	if err != nil {
		return failedRow(shown, "cannot read "+ignore.IgnoreFileName, err)
	}
	var resolver validation.BundleResolver
	if s.BundleStore != nil {
		resolver = s.PolicyResolver(dir)
	}
	dropped, notes, ok := dropRemoved(raw)
	if !ok {
		return MigratedFile{Path: shown, Status: MigrateKept, Message: "kept: delivery is written in flow style; delete it by hand"}
	}
	check := validation.UpgradeCheck(ctx, ignore.FS(os.DirFS(dir), m), resolver, s.env().now())
	// The raise is judged on what migrate writes: the raised document without
	// the builds detection reproduces, so their deprecation adds no finding.
	up, err := contract.Upgrade(dropped, to, func(b []byte) ([]contract.ValidationError, []contract.ValidationWarning, error) {
		b, _ = dropDetectedBuilds(dir, b)
		return check(b)
	})
	row := upgradeRow(MigratedFile{Path: shown, From: up.From, To: up.To}, up, to, err)
	if row.Status != MigrateRewritten && row.Status != MigrateCurrent {
		return row // a kept or manual file is never written, the drops included
	}
	out, built := dropDetectedBuilds(dir, up.Raw)
	row = withNotes(row, append(notes, built...), !bytes.Equal(out, raw))
	if row.Status != MigrateRewritten {
		return row
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		from := cmp.Or(up.From, contract.Current())
		row.Status, row.To = MigrateKept, from
		row.Message = fmt.Sprintf("kept at %s: pacto.yaml is a symbolic link; edit it by hand", from)
		return row
	}
	if !dryRun {
		if err := s.writeAtomic(path, fi.Mode().Perm(), out); err != nil {
			return failedRow(shown, "cannot write", err)
		}
	}
	return row
}

// readContract reads the pacto.yaml at path, shown as shown, or returns the
// row that says why it was not read: a failed read, or a kept file that is
// neither regular nor a symbolic link (a FIFO would block the read).
func readContract(path, shown string) (fs.FileInfo, []byte, *MigratedFile) {
	fi, err := os.Lstat(path)
	if err == nil && !fi.Mode().IsRegular() && fi.Mode()&fs.ModeSymlink == 0 {
		return nil, nil, &MigratedFile{Path: shown, Status: MigrateKept, Message: "kept: pacto.yaml is not a regular file; edit it by hand"}
	}
	var raw []byte
	if err == nil {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		f := failedRow(shown, "cannot read", err)
		return nil, nil, &f
	}
	return fi, raw, nil
}

// upgradeRow is row with the status and message Upgrade's result up and its
// error err give for the target to.
func upgradeRow(row MigratedFile, up contract.Upgraded, to string, err error) MigratedFile {
	var manual *contract.ErrManualMigration
	switch {
	case errors.As(err, &manual):
		row.Status = MigrateManual
		row.Message = fmt.Sprintf("pactoVersion %q is not migrated automatically; see %s", up.From, contract.MigrationGuideURL)
	case err != nil && up.From == "":
		row.Status, row.Message = MigrateKept, "kept: "+err.Error()
	case err != nil:
		row.Status, row.Message = MigrateKept, fmt.Sprintf("kept at %s: %s", up.From, err)
	case up.Kept != nil:
		row.Status, row.Finding, row.Message = MigrateKept, up.Kept, up.Change()
	case up.To != up.From:
		row.Status, row.Message = MigrateRewritten, up.Change()
	case up.From == "":
		row.Status, row.Message = MigrateCurrent, fmt.Sprintf("already current (no pactoVersion; read as %s)", contract.Current())
	case to == contract.Current():
		row.Status, row.Message = MigrateCurrent, fmt.Sprintf("already current (%s)", up.From)
	default:
		row.Status, row.Message = MigrateCurrent, fmt.Sprintf("already at or past %q (%s)", to, up.From)
	}
	return row
}

// writeAtomic replaces path with data at mode. It refuses a file the caller
// cannot write (a rename needs only the folder's permission), then writes a
// temporary file beside it and renames it over path, so a failure leaves the
// original and no temporary file.
//
// ponytail: ownership and extended attributes are not preserved, which the
// writability probe makes moot for files the caller cannot write;
// internal/mcp/create.go:2014 has the same pattern bound to its own test
// seams; one helper for both when a third writer appears.
func (s *Service) writeAtomic(path string, mode fs.FileMode, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_ = f.Close()
	t, err := os.CreateTemp(filepath.Dir(path), ".pacto-tmp-pacto.yaml-*")
	if err != nil {
		return err
	}
	tmp := t.Name()
	_ = t.Close()
	err = s.env().writeFile(tmp, data, mode)
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = s.env().rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
