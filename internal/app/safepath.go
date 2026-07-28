package app

import (
	"fmt"
	"strings"
)

// safeOutputName validates that a contract-supplied service name is safe to use as
// a single filesystem path component (an output directory or a filename). The name
// comes from a remote-controlled bundle, so a value containing a path separator or
// "." / ".." (e.g. "../../etc/cron.d/x") must be refused before it is joined into
// an on-disk output location, or extraction/writes could escape the working dir.
func safeOutputName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("refusing to use unsafe service name %q as an output path component", name)
	}
	return nil
}
