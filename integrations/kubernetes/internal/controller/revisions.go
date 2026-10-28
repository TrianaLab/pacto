/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/google/go-containerregistry/pkg/authn"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
)

func (r *PactoReconciler) ensureRevision(ctx context.Context, pacto *pactov1alpha1.Pacto, loadResult *loader.LoadResult) (string, error) {
	log := logf.FromContext(ctx)

	hash := fmt.Sprintf("%x", sha256.Sum256(loadResult.RawYAML))
	shortHash := hash[:7]

	version := loadResult.Contract.Service.Version
	if version == "" {
		version = "unknown"
	}

	revisionName := revisionNameFor(pacto.Name, version, shortHash)

	existing := &pactov1alpha1.PactoRevision{}
	err := r.Get(ctx, client.ObjectKey{Namespace: pacto.Namespace, Name: revisionName}, existing)
	if err == nil {
		if existing.Status.ContractHash == "" || existing.Status.CreatedAt == nil {
			now := metav1.Now()
			existing.Status.Resolved = true
			existing.Status.ContractHash = hash
			if existing.Status.CreatedAt == nil {
				existing.Status.CreatedAt = &now
			}
			if statusErr := r.Status().Update(ctx, existing); statusErr != nil {
				log.V(1).Info("Failed to backfill revision status", "revision", revisionName, "error", statusErr)
			}
		}
		return revisionName, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to check for existing revision: %w", err)
	}

	source := pactov1alpha1.RevisionSource{}
	if loadResult.ResolvedRef != "" {
		source.OCI = loadResult.ResolvedRef
		source.Digest = loadResult.ResolvedDigest
	} else if pacto.Spec.ContractRef.OCI != "" {
		source.OCI = pacto.Spec.ContractRef.OCI
		source.Digest = loadResult.ResolvedDigest
	} else {
		source.Inline = true
	}

	now := metav1.Now()
	revision := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      revisionName,
			Namespace: pacto.Namespace,
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       pacto.Name,
				pactov1alpha1.LabelRevisionVersion: versionLabelValue(version),
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:     version,
			Source:      source,
			PactoRef:    pacto.Name,
			ServiceName: loadResult.Contract.Service.Name,
		},
	}

	if err := ctrl.SetControllerReference(pacto, revision, r.Scheme); err != nil {
		return "", fmt.Errorf("failed to set owner reference: %w", err)
	}

	if err := r.Create(ctx, revision); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return revisionName, nil
		}
		return "", fmt.Errorf("failed to create PactoRevision: %w", err)
	}

	revision.Status = pactov1alpha1.PactoRevisionStatus{
		Resolved:     true,
		ContractHash: hash,
		CreatedAt:    &now,
	}
	if statusErr := r.Status().Update(ctx, revision); statusErr != nil {
		log.Error(statusErr, "Failed to update PactoRevision status", "revision", revisionName)
	}

	log.Info("Created PactoRevision", "revision", revisionName, "version", version, "hash", shortHash)
	r.Recorder.Eventf(pacto, corev1.EventTypeNormal, "RevisionCreated", "Created revision %s for contract v%s", revisionName, version)

	return revisionName, nil
}

// revisionNameFor names the PactoRevision of one contract version. The legacy
// form (dots to dashes) is kept whenever it is already a valid object name, so
// no existing revision is renamed; anything else (build metadata, upper case,
// a version too long for the hash to survive) gets a sanitised version segment
// and keeps the content hash at the end, where truncation cannot reach it.
func revisionNameFor(pactoName, version, shortHash string) string {
	legacy := fmt.Sprintf("%s-%s-%s", pactoName, strings.ReplaceAll(version, ".", "-"), shortHash)
	if len(k8svalidation.IsDNS1123Subdomain(legacy)) == 0 {
		return legacy
	}
	suffix := "-" + shortHash
	head := pactoName
	if v := dnsSegment(version); v != "" {
		head += "-" + v
	}
	if maxHead := k8svalidation.DNS1123SubdomainMaxLength - len(suffix); len(head) > maxHead {
		head = strings.TrimRight(head[:maxHead], "-.")
	}
	return head + suffix
}

// dnsSegment lowercases s, maps every rune outside [a-z0-9-] to '-', collapses
// runs of '-' and trims them from both ends.
func dnsSegment(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			c = '-'
		}
		if c == '-' && strings.HasSuffix(b.String(), "-") {
			continue
		}
		b.WriteRune(c)
	}
	return strings.Trim(b.String(), "-")
}

// versionLabelValue makes a contract version usable as a label value: a valid
// version is returned unchanged; otherwise runes outside [A-Za-z0-9._-] become
// '-', and a result over 63 characters is cut and suffixed with a short hash
// of the full version so distinct long versions stay distinct.
func versionLabelValue(version string) string {
	if len(k8svalidation.IsValidLabelValue(version)) == 0 {
		return version
	}
	out := strings.Map(func(c rune) rune {
		if c < utf8.RuneSelf && (c == '.' || c == '_' || c == '-' || unicode.IsLetter(c) || unicode.IsDigit(c)) {
			return c
		}
		return '-'
	}, version)
	if len(out) > k8svalidation.LabelValueMaxLength {
		sum := fmt.Sprintf("%x", sha256.Sum256([]byte(version)))[:8]
		out = out[:k8svalidation.LabelValueMaxLength-len(sum)-1] + "-" + sum
	}
	return strings.Trim(out, "-._")
}

// shortDigest returns the first 12 characters of a digest for display, or the
// whole string when it is shorter. A digest can be empty or malformed (e.g. a
// registry that omits it), so slicing [:12] unconditionally would panic.
func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// revisionsBySourceRef indexes this Pacto's revisions by spec.source.oci. A force-push
// leaves TWO revisions on the same ref, so the newest wins: comparing the registry against
// the superseded one would re-raise TagOverwritten on every reconcile.
// ponytail: creationTimestamp ordering, ties go to List order. Revisions are created
// seconds apart, so a tie needs a same-second double force-push to even be observable.
func (r *PactoReconciler) revisionsBySourceRef(ctx context.Context, pacto *pactov1alpha1.Pacto) (map[string]*pactov1alpha1.PactoRevision, error) {
	revList := &pactov1alpha1.PactoRevisionList{}
	if err := r.List(ctx, revList,
		client.InNamespace(pacto.Namespace),
		client.MatchingLabels{pactov1alpha1.LabelPactoName: pacto.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list revisions: %w", err)
	}

	byRef := make(map[string]*pactov1alpha1.PactoRevision, len(revList.Items))
	for i := range revList.Items {
		rev := &revList.Items[i]
		// An inline revision has an empty ref; taggedRef never is, so it is simply never hit.
		ref := rev.Spec.Source.OCI
		if cur, ok := byRef[ref]; ok && rev.CreationTimestamp.Before(&cur.CreationTimestamp) {
			continue
		}
		byRef[ref] = rev
	}
	return byRef, nil
}

// syncAllRevisions mirrors every registry tag of baseRef into a PactoRevision and
// raises TagOverwritten when a tag's digest moved. Called by RevisionMirror, never
// from Reconcile.
//
// A per-tag failure does not abort the pass -- one unreadable tag must not stop the
// rest being mirrored -- but it is no longer swallowed into a V(1) line either: the
// failures are joined into the returned error, and RevisionMirror turns that into
// the same event vocabulary the main load path uses.
func (r *PactoReconciler) syncAllRevisions(ctx context.Context, pacto *pactov1alpha1.Pacto, baseRef string, ociAuth *authn.AuthConfig) error {
	log := logf.FromContext(ctx)

	tags, err := r.Loader.ListTags(ctx, baseRef, ociAuth)
	if err != nil {
		return fmt.Errorf("failed to list tags: %w", err)
	}

	// Index the existing revisions by the ref ensureRevision actually WRITES
	// (spec.source.oci). Keying on the version label instead compared a registry tag
	// against service.version from inside the contract, so the everyday "v1.2.0" tag on
	// a "1.2.0" version never matched and the force-push check below never ran.
	byRef, err := r.revisionsBySourceRef(ctx, pacto)
	if err != nil {
		return err
	}

	var failures []error
	for _, tag := range tags {
		taggedRef := strings.TrimPrefix(baseRef, "oci://")
		if idx := strings.LastIndex(taggedRef, ":"); idx > strings.LastIndex(taggedRef, "/") {
			taggedRef = taggedRef[:idx]
		}
		taggedRef = taggedRef + ":" + tag

		// If a revision exists for this tag, check whether the digest still matches.
		// A mismatch means the tag was force-pushed (overwritten) on the registry.
		if existing, ok := byRef[taggedRef]; ok {
			storedDigest := existing.Spec.Source.Digest
			if storedDigest == "" {
				// Revision predates digest tracking — skip drift check.
				continue
			}

			loadResult, loadErr := r.Loader.Load(ctx, taggedRef, "", ociAuth)
			if loadErr != nil {
				failures = append(failures, fmt.Errorf("digest check for tag %s: %w", tag, loadErr))
				continue
			}

			if loadResult.ResolvedDigest == storedDigest {
				continue // Digest matches — tag was not overwritten.
			}

			log.Info("Detected force-push: OCI digest changed for tag",
				"tag", tag,
				"oldDigest", storedDigest,
				"newDigest", loadResult.ResolvedDigest,
				"oldRevision", existing.Name)
			r.Recorder.Eventf(pacto, corev1.EventTypeWarning, "TagOverwritten",
				"Tag %s was force-pushed (digest changed from %s to %s)", tag, shortDigest(storedDigest), shortDigest(loadResult.ResolvedDigest))

			revName, revErr := r.ensureRevision(ctx, pacto, loadResult)
			if revErr != nil {
				failures = append(failures, fmt.Errorf("revision for force-pushed tag %s: %w", tag, revErr))
				continue
			}
			log.Info("Created new revision for force-pushed tag", "tag", tag, "revision", revName)
			continue
		}

		loadResult, loadErr := r.Loader.Load(ctx, taggedRef, "", ociAuth)
		if loadErr != nil {
			failures = append(failures, fmt.Errorf("tag %s: %w", tag, loadErr))
			continue
		}

		revName, revErr := r.ensureRevision(ctx, pacto, loadResult)
		if revErr != nil {
			failures = append(failures, fmt.Errorf("revision for tag %s: %w", tag, revErr))
			continue
		}
		log.V(1).Info("Synced revision for tag", "tag", tag, "revision", revName)
	}

	return errors.Join(failures...)
}
