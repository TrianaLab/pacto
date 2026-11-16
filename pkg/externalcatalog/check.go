package externalcatalog

import (
	"context"
	"fmt"
)

// Summary is what [Check] reports about a catalog file.
type Summary struct {
	// Digest is the artifact digest of the checked file: the extension when one
	// is given, else the default.
	Digest string
	// Entries counts the merged catalog's entries.
	Entries int
}

// Check validates the default catalog bytes and, when ext is not nil, the
// extension merged over it.
func Check(def, ext []byte) (Summary, error) {
	d, err := load(def)
	if err != nil {
		return Summary{}, fmt.Errorf("default catalog: %w", err)
	}
	if ext == nil {
		return Summary{Digest: Digest(def), Entries: len(d.Entries)}, nil
	}
	o, err := load(ext)
	if err != nil {
		return Summary{}, fmt.Errorf("extension catalog: %w", err)
	}
	ref := Ref{Origin: OriginOrganization, Digest: Digest(ext)}
	return Summary{Digest: ref.Digest, Entries: Merge(d, Ref{Origin: OriginDefault, Digest: Digest(def)}, o, &ref).Len()}, nil
}

// PushFunc writes catalog bytes to ref and returns the artifact digest; [Write]
// bound to a client is the production one.
type PushFunc func(ctx context.Context, ref string, b []byte) (string, error)

// Push publishes the embedded default catalog to ref and refuses a result whose
// digest differs from [DefaultDigest].
func Push(ctx context.Context, push PushFunc, ref string) (string, error) {
	got, err := push(ctx, ref, DefaultBytes())
	if err != nil {
		return "", err
	}
	if want := DefaultDigest(); got != want {
		return "", fmt.Errorf("pushed %s as %s, want the embedded digest %s", ref, got, want)
	}
	return got, nil
}
