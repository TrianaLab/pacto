// Package cachehook holds the one interleaving seam of the OCI disk-cache entry
// read, so that the readers held to its coherence rule can prove it from
// wherever they live.
//
// A cache entry is two files, and the disk cache is SHARED: a competing writer
// commits a whole new generation between a reader's two observations. The defect
// that costs — bundle from one installed generation, identity from the next — is
// only observable if a test can commit at exactly that instant, and that instant
// is inside [github.com/trianalab/pacto/v3/pkg/oci]. A package-private seam there
// can only be driven by that package's own tests, while the walkers that must
// obey the same rule are elsewhere: the dashboard cache index and the fleet cache
// inventory. This package is that seam, reachable from all three.
//
// Production never sets it; the zero value is a no-op.
package cachehook

// AfterBundleRead runs inside a cache-entry read, after the bundle bytes have
// come off a held generation and before the identity beside them is read through
// the same handle. A test installs a competing writer here.
var AfterBundleRead = func() {}
