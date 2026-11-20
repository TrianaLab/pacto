package ci

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// NormalizeRegistry is the --registry value as a repository prefix: no
// oci:// scheme and no trailing slash. A value that names another scheme,
// carries userinfo or a query, or has no host is an error, so a bad flag
// stops the run instead of reading every row unverified (and a credential
// never reaches the plan).
func NormalizeRegistry(flag string) (string, error) {
	reg := strings.TrimRight(contract.TrimOCIScheme(strings.TrimSpace(flag)), "/")
	if host, _, _ := strings.Cut(reg, "/"); flag != "" && host == "" || strings.ContainsAny(reg, "@?#") || strings.Contains(reg, "://") {
		// The value is not echoed: it may hold a credential.
		return "", errors.New("--registry: want a registry host and path with no scheme, credentials or query, for example ghcr.io/acme")
	}
	return reg, nil
}

// ImageRef is an image a service's inferred record names, with the
// folder-qualified "<path>:<line>" it was read from.
type ImageRef struct {
	Image, Source string
}

// dockerLibrary is Docker Hub's official-image namespace: it holds nobody's
// contracts.
const dockerLibrary = "docker.io/library"

// ResolveRegistry picks the registry contracts are published to, with its
// source, plus notes on rungs that could not answer. Precedence: flag, then
// the registry a fresh installation advertisement names (advertised, empty
// when there is none), then GitHub's ghcr.io owner, then GitLab's
// CI_REGISTRY_IMAGE, then the one registry every image names. Nothing found
// is none: unknown is never guessed.
func ResolveRegistry(flag string, advertised Value, ctx Context, images []ImageRef) (Value, []string) {
	if flag != "" {
		return Value{Value: flag, Source: "--registry"}, nil
	}
	if advertised.Value != "" {
		return advertised, nil
	}
	var notes []string
	if ctx.Platform == "github" && ctx.Owner != "" {
		if ctx.ServerURL == "" || ctx.ServerURL == "https://github.com" {
			return Value{Value: "ghcr.io/" + strings.ToLower(ctx.Owner), Source: "GITHUB_REPOSITORY_OWNER"}, nil
		}
		notes = append(notes, "GitHub Enterprise Server: no registry host to assume")
	}
	if ctx.Platform == "gitlab" && ctx.RegistryImage != "" {
		return Value{Value: ctx.RegistryImage, Source: "CI_REGISTRY_IMAGE"}, notes
	}
	v, note := fromImages(images)
	if note != "" {
		notes = append(notes, note)
	}
	return v, notes
}

// fromImages is the registry every image names, or none with a note when
// they name several. Docker Hub's library alone is none.
func fromImages(images []ImageRef) (Value, string) {
	var regs []string
	for _, im := range images {
		if r := registryOf(im.Image); !slices.Contains(regs, r) {
			regs = append(regs, r)
		}
	}
	switch {
	case len(regs) > 1:
		return Value{}, "images name " + strconv.Itoa(len(regs)) + " registries (" + strings.Join(regs, ", ") + "); pass --registry"
	case len(regs) == 0 || regs[0] == dockerLibrary:
		return Value{}, ""
	}
	return Value{Value: regs[0], Source: "image references in " + images[0].Source}, ""
}

// registryOf is an image's host and first path segment.
func registryOf(image string) string {
	repo, _ := discover.ImageRepo(image)
	host, rest, _ := strings.Cut(repo, "/")
	first, _, _ := strings.Cut(rest, "/")
	return host + "/" + first
}
