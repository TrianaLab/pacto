package pactify

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Many pactifies each folder in dirs with base, which names no folder or
// service. A folder that fails does not stop the others: the error names it,
// and its Result is nil when
// nothing could be planned.
func Many(ctx context.Context, base Options, dirs []string, writeFile func(string, []byte, os.FileMode) error) (*Bulk, error) {
	if base.Name != "" && len(dirs) > 1 {
		return nil, fmt.Errorf("a name is one service's, so it cannot be given with %d folders", len(dirs))
	}
	out := &Bulk{Results: make([]*Result, len(dirs)), Skipped: []string{}}
	var errs []error
	for i, dir := range dirs {
		opts := base
		opts.Dir = dir
		r, err := Init(ctx, opts, writeFile)
		out.Results[i] = r
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dir, err))
		}
	}
	return out, errors.Join(errs...)
}
