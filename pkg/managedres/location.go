package managedres

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Remote state backends a person may list in controller.terraformStates.
const (
	SchemeS3    = "s3"
	SchemeGCS   = "gs"
	SchemeAzure = "azurerm"
)

// Location is one remote Terraform or OpenTofu state: an S3 object
// (Host is the bucket, Region optional), a Cloud Storage object (Host is the
// bucket) or an Azure blob (Host is the storage account, Container its
// container). It names where the state is, never what it holds.
type Location struct {
	Scheme, Host, Container, Key, Region string
}

// hostRE is a bucket or storage account name.
var hostRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,62}$`)

// regionRE is a cloud region.
var regionRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// ParseLocation reads s3://bucket/key?region=r, gs://bucket/object or
// azurerm://account/container/key. Credentials, a fragment, a query key
// other than region (S3 only) and a ".." or empty path segment are refused,
// so a listed location can only name an object.
func ParseLocation(s string) (Location, error) {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || strings.ContainsAny(s, "#%") {
		return Location{}, fmt.Errorf("terraform state location %q is not <scheme>://<bucket>/<key> with no credentials, escapes or fragment", s)
	}
	loc := Location{Scheme: u.Scheme, Host: u.Host, Key: strings.TrimPrefix(u.Path, "/")}
	if err := loc.query(u.Query()); err != nil {
		return Location{}, fmt.Errorf("terraform state location %q: %w", s, err)
	}
	if loc.Scheme == SchemeAzure {
		loc.Container, loc.Key, _ = strings.Cut(loc.Key, "/")
	}
	if err := loc.check(); err != nil {
		return Location{}, fmt.Errorf("terraform state location %q: %w", s, err)
	}
	return loc, nil
}

// query keeps the region of an S3 location and refuses any other key.
func (l *Location) query(q url.Values) error {
	for k, v := range q {
		if k != "region" || l.Scheme != SchemeS3 || len(v) != 1 || !regionRE.MatchString(v[0]) {
			return fmt.Errorf("only an S3 location takes a query, and only region=<region>")
		}
		l.Region = v[0]
	}
	return nil
}

// check refuses an unknown scheme, a bad bucket or account and a key that
// is empty or walks out of its container.
func (l Location) check() error {
	if !slices.Contains([]string{SchemeS3, SchemeGCS, SchemeAzure}, l.Scheme) {
		return fmt.Errorf("the scheme must be s3, gs or azurerm")
	}
	if !hostRE.MatchString(l.Host) {
		return fmt.Errorf("%q is not a bucket or storage account name", l.Host)
	}
	if l.Scheme == SchemeAzure && !hostRE.MatchString(l.Container) {
		return fmt.Errorf("an azurerm location names <account>/<container>/<key>")
	}
	for _, seg := range strings.Split(l.Key, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsFunc(seg, badKeyRune) {
			return errors.New("the key must be a path with no empty, . or .. segment")
		}
	}
	return nil
}

// badKeyRune is a rune a listed key never holds.
func badKeyRune(r rune) bool { return r < 0x21 || r == 0x7f || r == '\\' || r == '?' }

// String is the canonical form of l, shown wherever the location is.
func (l Location) String() string {
	switch {
	case l.Scheme == SchemeAzure:
		return l.Scheme + "://" + l.Host + "/" + l.Container + "/" + l.Key
	case l.Region != "":
		return l.Scheme + "://" + l.Host + "/" + l.Key + "?region=" + l.Region
	}
	return l.Scheme + "://" + l.Host + "/" + l.Key
}
