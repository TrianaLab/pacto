package app

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	mmsemver "github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/schemax"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// offeringSubjectPrefix is the declare subject of an offering accepted from
// a producer's inferred offering: offering:<name>.
const offeringSubjectPrefix = "offering:"

// offered is the offering req accepts, and whether req is an acceptance.
func (req DeclareRequest) offered() (string, bool) {
	return strings.CutPrefix(req.Service, offeringSubjectPrefix)
}

// detectedPlaceholder is the --detected a refusal's next command names when
// the person passed none.
const detectedPlaceholder = "k8s:<namespace>/<configmap>"

// sourceDir is the folder of a contribution's provenance files (the source
// object it was read from): never part of the declared offering.
const sourceDir = "source"

// offeringSource writes the folder of an offering acceptance (Decisions 9
// and 10): the contribution req.From names, verified as signed by its
// producer, or with req.Dir the person's own folder, with the person's
// version and owner. A version that is no release, a From that is no
// contribution or no key verified, a bundle offering another name, a
// repository under pacto-inferred and an open configuration schema are
// refused before anything is written.
func (pl *declarePlanner) offeringSource(ctx context.Context) error {
	if err := pl.req.checkOfferingVersion(); err != nil {
		return err
	}
	if repo, _, _ := publish.Layout(pl.req.Registry, pl.offer, ""); inferredRepo(repo) {
		return &DeclareError{Code: codeClaimRepositoryInferred, Message: repo + " is where Pacto publishes inferred contracts: accept the offering into a registry path outside " +
			contract.InferredRepository, CLI: pl.req.command("")}
	}
	b, err := pl.contribution(ctx)
	if err != nil {
		return err
	}
	pl.dir = filepath.Join(pl.scratch, pl.offer)
	src := fs.FS(b.FS)
	if pl.req.Dir != "" {
		src = os.DirFS(pl.req.Dir)
	}
	if err := pl.copyOffering(src); err != nil {
		return err
	}
	if err := pl.offeringFolder(); err != nil {
		return err
	}
	pl.rec = &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: pl.s.buildVersion()}, Fields: []contract.Inferred{}}
	return nil
}

// checkOfferingVersion refuses an acceptance with no version, or one that
// is no release (MAJOR.MINOR.PATCH, no prerelease or build).
func (req DeclareRequest) checkOfferingVersion() error {
	if req.Version == "" {
		return &DeclareError{Code: codeVersionRequired, Message: "accepting an offering publishes the version you set: pass --version", CLI: req.command("")}
	}
	if v, err := mmsemver.StrictNewVersion(req.Version); err != nil || v.Prerelease() != "" || v.Metadata() != "" {
		return &DeclareError{Code: codeVersionConflict, Message: req.Version + " is no release version: pass --version MAJOR.MINOR.PATCH", CLI: req.command("")}
	}
	return nil
}

// contribution is the bundle req.From names, read by digest after one of
// the producer's keys verified it, and checked to be a contribution that
// offers the accepted name. It sets the inferredFrom the declaration
// records.
func (pl *declarePlanner) contribution(ctx context.Context) (*contract.Bundle, error) {
	from := contract.TrimOCIScheme(pl.req.From)
	producer, ok := contract.ContributionRef(from)
	if !ok {
		return nil, &DeclareError{Code: codeOfferingSourceInvalid, CLI: pl.req.command(""),
			Message: "accepting an offering starts from the contribution it was inferred as, <registry>/" + contract.InferredRepository + "/<producer>/" +
				contract.ContributionsRepository + ":<tag>: pass it with --from"}
	}
	repo, tag, digest := contract.SplitOCIRef(from)
	if digest == "" {
		r, err := pl.reg.Head(ctx, repo+":"+tag)
		if err != nil || r == nil {
			return nil, &DeclareError{Code: codeRegistryUnreadable, Message: "reading " + from + ": " + cmp.Or(fmt.Sprint(err), "no such tag")}
		}
		digest = r.Digest
	}
	who, err := pl.verifyProducer(ctx, repo, digest, producer)
	if err != nil {
		return nil, err
	}
	b, err := pl.reg.Pull(ctx, repo+"@"+digest)
	if err != nil {
		return nil, &DeclareError{Code: codeRegistryUnreadable, Message: "reading " + repo + "@" + digest + ": " + err.Error()}
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if why := contract.ContributionRefusal(b, rec); err != nil || why != "" {
		return nil, &DeclareError{Code: codeOfferingSourceInvalid, Message: repo + "@" + digest + " is no contribution: " + cmp.Or(why, fmt.Sprint(err))}
	}
	if o := b.Contract.Service.Offering; o != pl.offer {
		return nil, &DeclareError{Code: codeOfferingMismatch, Message: repo + "@" + digest + " offers " + cmp.Or(o, "nothing") + ", not " + pl.offer +
			": accept it as offering:" + cmp.Or(o, "<name>")}
	}
	pl.offeringFrom = contract.OfferingInferredFrom(producer, b.Contract.Service.Name, digest)
	pl.plan.Notes = append(pl.plan.Notes, "accepted from "+repo+"@"+digest+", signed by "+who)
	return b, nil
}

// verifyProducer is who signed repo@digest: the installation key the
// detection at req.Detected advertises for that repository, or a key the
// contribution trust config grants producer. Content no key verified is
// refused, so no inferredFrom names a producer that did not sign it.
func (pl *declarePlanner) verifyProducer(ctx context.Context, repo, digest, producer string) (string, error) {
	keys, why := pl.producerKeys(ctx, repo, producer)
	var errs []error
	for _, k := range keys {
		p, err := signing.PolicyOf(k)
		if err == nil {
			err = publish.VerifyDigest(ctx, pl.reg, publish.PolicyVerifier(p), repo, digest)
		}
		if err == nil {
			return k.Name, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", k.Name, err))
	}
	if len(keys) > 0 {
		why = errors.Join(errs...).Error()
	}
	sub := pl.req
	sub.Detected = cmp.Or(sub.Detected, detectedPlaceholder)
	return "", &DeclareError{Code: codeOfferingSourceUnverified, CLI: sub.command(""),
		Message: "no key of " + producer + " verified " + repo + "@" + digest + " (" + why + "): pass --detected with the detection of the installation that produced it, " +
			"or grant the producer in the contribution trust config"}
}

// producerKeys are the keys that may have signed producer's contributions in
// repo: the installation key of the detection req.Detected names, when repo
// is that installation's own offerings repository, and every key the
// contribution trust config grants producer for repo. With none it says why.
func (pl *declarePlanner) producerKeys(ctx context.Context, repo, producer string) ([]delivery.TrustedKey, string) {
	var keys []delivery.TrustedKey
	why := []string{}
	if pl.req.Detected != "" {
		k, err := pl.installationKey(ctx, repo)
		if err != nil {
			why = append(why, err.Error())
		} else {
			keys = append(keys, *k)
		}
	}
	if pl.req.ContributionTrust != "" {
		k, err := grantedKeys(pl.req.ContributionTrust, repo, producer)
		if err != nil {
			why = append(why, err.Error())
		}
		keys = append(keys, k...)
	}
	if len(why) == 0 && len(keys) == 0 {
		why = append(why, "neither a detection nor a contribution trust config was given")
	}
	return keys, strings.Join(why, "; ")
}

// installationKey is the key the detection at req.Detected (k8s:<ns>/<name>
// read with the caller's cluster client, or a file) advertises, when repo
// is the offerings repository that installation advertises as its own.
func (pl *declarePlanner) installationKey(ctx context.Context, repo string) (*delivery.TrustedKey, error) {
	var src *fleetsrc.DetectionSource
	switch rest, k8s := strings.CutPrefix(pl.req.Detected, "k8s:"); {
	case k8s && pl.req.Dynamic == nil:
		return nil, errors.New("reading the detection " + pl.req.Detected + " needs a Kubernetes client")
	case k8s:
		ns, name, _ := strings.Cut(rest, "/")
		src = fleetsrc.NewDetectionK8sSource("detection", pl.req.Dynamic, ns, name, pl.s.env().now)
	default:
		src = fleetsrc.NewDetectionFileSource("detection", pl.req.Detected, pl.s.env().now)
	}
	res, st := src.Read(ctx)
	if st.Status == fleet.SourceUnavailable {
		return nil, errors.New("the detection " + pl.req.Detected + " was not read")
	}
	if own, ok := res.OfferingsRepository(); !ok || own != repo {
		return nil, errors.New("the detection " + pl.req.Detected + " advertises no offerings repository " + repo)
	}
	k := delivery.InferredSignerFromAdvertisement(res.Installation.SnapshotKey)
	if k == nil {
		return nil, errors.New("the detection " + pl.req.Detected + " advertises no installation key")
	}
	k.Name = "the installation key of cluster " + res.Cluster
	return k, nil
}

// grantedKeys are the keys the contribution trust config at path (509-S11's
// grants) binds to producer with an allowedContractRepos entry granting
// repo.
func grantedKeys(path, repo, producer string) ([]delivery.TrustedKey, error) {
	ts, err := loadStructuredTrustStore(path)
	if err != nil {
		return nil, err
	}
	var out []delivery.TrustedKey
	for _, id := range slices.Sorted(maps.Keys(ts)) {
		e := ts[id]
		granted := slices.ContainsFunc(e.ContractRepos, func(prefix string) bool {
			r, ok, _ := contract.ContributionGrant(prefix, producer)
			return ok && r == repo
		})
		if e.ProducerID != producer || !granted {
			continue
		}
		pem, _ := delivery.PublicKeyPEM(e.PublicKey) // an Ed25519 key always marshals
		out = append(out, delivery.TrustedKey{Name: "trust-config key " + id + " of " + producer, PublicKey: pem})
	}
	if len(out) == 0 {
		return nil, errors.New("the contribution trust config grants " + producer + " no key for " + repo)
	}
	return out, nil
}

// copyOffering writes every file of src into the offering folder but its
// inferred record, manifests and the source/ folder of provenance files.
func (pl *declarePlanner) copyOffering(src fs.FS) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && path == sourceDir:
			return fs.SkipDir
		case d.IsDir() || path == manifestsFile || path == contract.InferredRecordFile:
			return nil
		}
		data, err := fs.ReadFile(src, path)
		if err == nil {
			err = pl.writeFile(path, data)
		}
		return err
	})
}

// offeringFolder names the accepted offering in the folder's pacto.yaml,
// sets the person's owner and refuses a folder that offers another name or
// whose configuration schemas leave a path open (D5).
func (pl *declarePlanner) offeringFolder() error {
	b, err := readLocalBundle(pl.dir)
	if err != nil {
		return err
	}
	if o := b.Contract.Service.Offering; o != pl.offer {
		return &DeclareError{Code: codeOfferingMismatch, Message: "the folder offers " + cmp.Or(o, "nothing") + ", not " + pl.offer}
	}
	raw := setServiceKeys(b.RawYAML, map[string]*yaml.Node{"name": {Kind: yaml.ScalarNode, Value: pl.offer}})
	if pl.req.Owner != "" {
		// ponytail: raw re-encoded a parsed contract, so service is a mapping.
		raw, _ = contract.SetServiceOwner(raw, contract.Owner{Team: pl.req.Owner})
	}
	if err := pl.writeFile(DefaultContractPath, raw); err != nil {
		return err
	}
	var open []string
	for _, c := range b.Contract.Configurations {
		for _, p := range configurationOpenPaths(pl.dir, c) {
			open = append(open, c.Name+": "+p)
		}
	}
	if len(open) > 0 {
		return &DeclareError{Code: codeOfferingSchemaOpen, CLI: pl.req.command(""),
			Message: "the offering's parameters are not a closed space, open at " + strings.Join(open, ", ") +
				": close each path in a folder (additionalProperties: false, a type, a class enum) and accept it with --dir"}
	}
	return nil
}

// configurationOpenPaths are the open paths of c's schema file in dir; a
// configuration with no schema, or one that cannot be read, is open whole.
func configurationOpenPaths(dir string, c contract.Configuration) []string {
	if c.Schema == "" {
		return []string{"no schema"}
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Schema)))
	var doc any
	if err == nil {
		err = yaml.Unmarshal(raw, &doc)
	}
	if err != nil {
		return []string{"schema unreadable"}
	}
	return schemax.OpenPaths(doc)
}

// setServiceKeys is raw, a parsed contract, with each service key set to its
// node, re-encoded.
func setServiceKeys(raw []byte, keys map[string]*yaml.Node) []byte {
	var doc yaml.Node
	_ = yaml.Unmarshal(raw, &doc) // raw parsed as a contract, so service is a mapping
	svc := mappingValue(doc.Content[0], "service")
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if i := keyIndex(svc, k); i >= 0 {
			svc.Content[i+1] = keys[k]
			continue
		}
		svc.Content = append(svc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, keys[k])
	}
	out, _ := yaml.Marshal(&doc) // a decoded document always encodes
	return out
}

// keyIndex is the index of key's key node in the mapping m, -1 when absent.
func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// mappingValue is key's value in the mapping m, nil when absent.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if i := keyIndex(m, key); i >= 0 {
		return m.Content[i+1]
	}
	return nil
}

// reoffer keeps, in a re-acceptance from the contribution, what the person
// set on the baseline: its summary and service lifecycle, and its owner when
// no --owner is given (Decision 11). With req.Dir the person's folder is the
// declaration and is kept as it is.
// ponytail: tunable tier marks are not carried yet; they need the shared tier
// reader (pkg/schemax), which a later change brings.
func (pl *declarePlanner) reoffer(base *contract.Bundle) {
	if pl.req.Dir != "" {
		return
	}
	var doc yaml.Node
	_ = yaml.Unmarshal(base.RawYAML, &doc) // the baseline parsed as a contract
	keys, carried := map[string]*yaml.Node{}, []string{}
	for _, k := range []string{"summary", "lifecycle"} {
		if n := mappingValue(mappingValue(doc.Content[0], "service"), k); n != nil {
			keys[k], carried = n, append(carried, k)
		}
	}
	raw := setServiceKeys(pl.raw, keys)
	if pl.req.Owner == "" && !base.Contract.Service.Owner.IsEmpty() {
		raw, _ = contract.SetServiceOwner(raw, base.Contract.Service.Owner)
		carried = append(carried, "owner")
	}
	// ponytail: raw sets nodes of a parsed baseline in a parsed contract, so
	// it parses.
	c, _ := contract.Parse(bytes.NewReader(raw))
	pl.raw, pl.folder = raw, c
	if len(carried) > 0 {
		pl.plan.Notes = append(pl.plan.Notes, "carried from "+pl.plan.Baseline.Version+": "+strings.Join(carried, ", "))
	}
}

// checkOfferingBaseline refuses an acceptance whose repository holds a
// service that offers another name, or none.
func (pl *declarePlanner) checkOfferingBaseline(base *contract.Bundle) error {
	if !pl.offering || base.Contract.Service.Offering == pl.offer {
		return nil
	}
	svc := base.Contract.Service
	return &DeclareError{Code: codeOfferingMismatch, Message: fmt.Sprintf("%s already holds %s %s, which offers %s, not %s: accept into another registry path",
		pl.repo(), svc.Name, svc.Version, cmp.Or(svc.Offering, "nothing"), pl.offer)}
}

// offeringVersion is the person's version, refused when it is not above the
// newest version the repository holds.
func (pl *declarePlanner) offeringVersion() (string, error) {
	if newest := semver.Latest(pl.tags); newest != "" && semver.Compare(pl.req.Version, newest) <= 0 {
		return "", &DeclareError{Code: codeVersionConflict, Message: pl.repo() + " already holds " + newest + ": pass a --version above it", CLI: pl.req.command("")}
	}
	return pl.req.Version, nil
}

// offeringCommand is the pacto declare that replays an offering acceptance.
func (req DeclareRequest) offeringCommand(intent string) string {
	parts := []string{"pacto declare", req.Service}
	add := func(flag, v string) {
		if v != "" {
			parts = append(parts, flag, v)
		}
	}
	add("--from", cmp.Or(req.From, "<contribution>"))
	add("--detected", req.Detected)
	add("--dir", req.Dir)
	add("--registry", cmp.Or(req.Registry, "<registry>"))
	add("--version", cmp.Or(req.Version, "<version>"))
	add("--owner", req.Owner)
	add("--from-draft", req.FromDraft)
	add("--intent", intent)
	return strings.Join(parts, " ")
}
