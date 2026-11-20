package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// PublisherTrust is which publishers the services of stages accept, as the caller
// reads them: the installation's publisher trust config (publishers.json in
// the PolicyConfigMap), the pending trust operations among ops and each
// service's pins, read from the streams repository and verified with the
// config's pin signer. stages and ops are what the caller listed, so only
// their services are named and only their pins are read. Anything that
// could not be read is named in unreadable and makes the answer partial.
func (d *Delivery) PublisherTrust(ctx context.Context, stages []delivery.StageObject, ops []delivery.OperationObject) *delivery.TrustView {
	t := &trustReads{}
	cfg, cfgErr := d.publisherConfig(ctx)
	if cfgErr != nil {
		t.add("the publisher trust config: " + cfgErr.Error())
	}
	pins := d.readPins(ctx, cfg, trustKeys(stages), t)
	v := delivery.TrustOf(cfg, stages, ops, func(ns string, s delivery.ServiceRef) []delivery.IdentityPin {
		switch {
		case cfgErr != nil:
			return nil // unknown: the config that names where pins live was not read
		case cfg.StreamsRepository == "":
			return []delivery.IdentityPin{} // no recorder, so no pin exists
		}
		return pins[pinKey{ns, s}]
	})
	if cfgErr != nil {
		v.Rule, v.Policy, v.RuleSource = nil, nil, delivery.PublisherSourceUnknown
		v.NoRule = "the publisher trust config could not be read, so the rule is unknown: " + cfgErr.Error()
	}
	v.AsOf, v.Completeness, v.Unreadable = d.Now().UTC().Format(time.RFC3339), "complete", append(v.Unreadable, t.unreadable...)
	if len(v.Unreadable) > 0 {
		v.Completeness = "partial"
	}
	return &v
}

// pinKey is where a service's pins are kept: per namespace of its Stages.
type pinKey struct {
	ns  string
	svc delivery.ServiceRef
}

// trustKeys are the services of the registry Stages and their namespaces.
func trustKeys(stages []delivery.StageObject) []pinKey {
	var out []pinKey
	for _, svc := range delivery.TrustServices(stages) {
		for _, ns := range delivery.TrustNamespaces(stages, svc) {
			out = append(out, pinKey{ns, svc})
		}
	}
	return out
}

// PinsFunc reads the pins a service may use, each naming the namespace
// whose Stages use it. cut says only part of the listing was read; dropped
// names a newest pin not used because a Stage already used a newer one.
type PinsFunc func(ctx context.Context, s delivery.ServiceRef) (pins []delivery.IdentityPin, cut, dropped string, err error)

// Installation is what the installation trusts, as fleet provenance judges
// with it: the publisher trust config, a reader of a service's usable pins
// (nil when the config names no streams repository, so no pin exists) and
// the promoter's policy high-water mark: the newest policy a Stage's watch
// judges with. Unguarded says why the Stages could not be listed, so neither
// of the promoter's no-rollback guards (policyAt, pinAt) was applied.
type Installation struct {
	Config                 delivery.PublisherConfig
	Pins                   PinsFunc
	PolicyDigest, PolicyAt string
	Unguarded              string
}

// InstallationTrust is what the installation trusts, read as PublisherTrust
// reads it, with the high-water marks the registry Stages' status.publisher
// keeps. A pin reader that cannot be built answers its reason on every read.
// It is ProvenanceChecker.Trust, so fleet provenance judges with the rule,
// policy and pins the promoter uses: the newest pin of each namespace, unless
// a Stage of that namespace already used a newer one.
func (d *Delivery) InstallationTrust(ctx context.Context) (Installation, error) {
	cfg, err := d.publisherConfig(ctx)
	if err != nil {
		return Installation{Config: cfg}, err
	}
	in := Installation{Config: cfg}
	pinAt := map[pinKey]time.Time{}
	stages, serr := d.Stages(ctx, "")
	if serr != nil {
		in.Unguarded = "the Stages could not be listed (" + serr.Error() + "), so the promoter's no-rollback guards were not applied"
	}
	for _, st := range stages {
		p := st.Status.Publisher
		if st.Spec.Candidates.Registry == nil || p == nil {
			continue
		}
		if p.Source == delivery.PublisherSourcePolicy && p.PolicyDigest != "" && rfcTime(p.PolicyAt).After(rfcTime(in.PolicyAt)) {
			in.PolicyDigest, in.PolicyAt = p.PolicyDigest, p.PolicyAt
		}
		k := pinKey{st.Metadata.Namespace, st.Spec.Service}
		if at := rfcTime(p.PinAt); at.After(pinAt[k]) {
			pinAt[k] = at
		}
	}
	read, err := d.pinsReader(ctx, cfg)
	switch {
	case err != nil:
		in.Pins = func(context.Context, delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
			return nil, "", "", err
		}
		return in, nil
	case read == nil:
		return in, nil
	}
	in.Pins = func(ctx context.Context, s delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
		l, err := read(ctx, "", s) // a pin it cannot use is simply not used
		var out []delivery.IdentityPin
		var seen, notes []string
		for _, p := range l.Pins { // newest first: the first of each namespace is the one it uses
			if slices.Contains(seen, p.Pin.Namespace) {
				continue
			}
			seen = append(seen, p.Pin.Namespace)
			if rfcTime(p.Pin.At).Before(pinAt[pinKey{p.Pin.Namespace, s}]) {
				notes = append(notes, "the newest pin of "+s.Name+" in "+p.Pin.Namespace+" is gone; no older pin is used")
				continue
			}
			out = append(out, p.Pin)
		}
		return out, cutNote(s, l.Cut), strings.Join(notes, "; "), err
	}
	return in, nil
}

// rfcTime is the RFC 3339 time s, zero when s is not one.
func rfcTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// cutNote says that only MaxPins pins of s were read.
func cutNote(s delivery.ServiceRef, cut bool) string {
	if !cut {
		return ""
	}
	return fmt.Sprintf("more than %d pins of %s are attached and only %d were read", deliveryrecord.MaxPins, s.Name, deliveryrecord.MaxPins)
}

// trustReads collects what a trust answer could not read.
type trustReads struct {
	mu         sync.Mutex
	unreadable []string
}

func (t *trustReads) add(why string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.unreadable = append(t.unreadable, why)
}

// publisherConfig is the installation's published PublisherConfig: the zero
// config, which derives no rule and reads no pins, when the chart publishes
// none.
func (d *Delivery) publisherConfig(ctx context.Context) (delivery.PublisherConfig, error) {
	if d.PolicyNamespace == "" {
		return delivery.PublisherConfig{}, errors.New("no delivery namespace was given (--delivery-namespace), so it was not read")
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := getInto(ctx, d.Dynamic.Resource(gvrConfigMaps).Namespace(d.PolicyNamespace), delivery.PolicyConfigMap, &cm); err != nil {
		return delivery.PublisherConfig{}, err
	}
	raw, ok := cm.Data[delivery.PublishersKey]
	if !ok {
		return delivery.PublisherConfig{}, nil
	}
	return delivery.ParsePublishers([]byte(raw))
}

// readPins reads the pins of each service in each namespace,
// delivery.DescribeParallel at once, naming every listing it could not read
// or read only in part and every pin it could not use. A listing it could not
// read has no entry: its pins are unknown.
func (d *Delivery) readPins(ctx context.Context, cfg delivery.PublisherConfig, keys []pinKey, t *trustReads) map[pinKey][]delivery.IdentityPin {
	if len(keys) == 0 {
		return nil
	}
	read, err := d.pinsReader(ctx, cfg)
	if err != nil {
		t.add("pins: " + err.Error())
	}
	if read == nil {
		return nil
	}
	out := make(map[pinKey][]delivery.IdentityPin, len(keys))
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(delivery.DescribeParallel)
	for _, k := range keys {
		g.Go(func() error {
			l, err := read(ctx, k.ns, k.svc)
			if err != nil {
				t.add("pins of " + k.svc.Name + " in " + k.ns + ": " + err.Error())
				return nil
			}
			if note := cutNote(k.svc, l.Cut); note != "" {
				t.add(note)
			}
			for _, u := range l.Unreadable {
				t.add("pins of " + k.svc.Name + ": " + u)
			}
			pins := make([]delivery.IdentityPin, 0, len(l.Pins))
			for _, p := range l.Pins {
				pins = append(pins, p.Pin)
			}
			mu.Lock()
			out[k] = pins
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // each read names its own failure
	return out
}

// pinsReader reads a service's pins in a namespace ("" for every one) as the
// promoter reads them: each verified with cfg's pin signer and each citing an
// approval record that verifies. No streams repository means no recorder, so
// no pin exists to read: nil and no error.
func (d *Delivery) pinsReader(ctx context.Context, cfg delivery.PublisherConfig) (func(context.Context, string, delivery.ServiceRef) (deliveryrecord.PinListing, error), error) {
	if cfg.StreamsRepository == "" {
		return nil, nil
	}
	verify, err := d.pinVerifier(cfg)
	if err != nil {
		return nil, err
	}
	person := d.personApprovals(ctx)
	reader := &deliveryrecord.Reader{}
	return func(ctx context.Context, ns string, svc delivery.ServiceRef) (deliveryrecord.PinListing, error) {
		return d.pinsOf(ctx, reader, cfg.StreamsRepository, ns, svc, verify, person)
	}, nil
}

// pinVerifier verifies pins with the pin signer cfg names, this cluster's
// recorder; it says why when pins cannot be read.
func (d *Delivery) pinVerifier(cfg delivery.PublisherConfig) (delivery.Verifier, error) {
	switch {
	case d.Records == nil:
		return nil, errors.New("this client reads no registry, so they are not read")
	case cfg.PinSigner == "":
		return nil, errors.New("the publisher trust config names no pin signer (delivery.recorder.signing.publicKey or keyless recorder signing), so they are not read")
	}
	keys, err := delivery.ParseTrust([]byte(cfg.PinSigner))
	if err != nil {
		return nil, err
	}
	return signing.TrustVerifier(keys)
}

// personApprovals verifies a person's keyless approval record against the
// installation's approver signers and names who decided; an agent's or a CI
// workload's record is a proposal and never confirms a signer.
func (d *Delivery) personApprovals(ctx context.Context) func([]byte) (delivery.ApprovalStatement, string, error) {
	policy, perr := d.classPolicy(ctx)
	return func(bundle []byte) (delivery.ApprovalStatement, string, error) {
		if perr != nil {
			return delivery.ApprovalStatement{}, "", errors.New("the installation's approver signers could not be read: " + perr.Error())
		}
		a, err := signing.OpenPersonApproval(bundle, policy)
		if err == nil && !delivery.ReadsAsDecision(a.Class) {
			err = errors.New("it is a proposal: signed by " + string(a.Class) + " " + a.SAN + ", and only a person's approval decides")
		}
		return a.Statement, a.Identity.User, err
	}
}

// pinsOf reads svc's pins in namespace ns from the streams repository.
func (d *Delivery) pinsOf(ctx context.Context, reader *deliveryrecord.Reader, streams, ns string, svc delivery.ServiceRef, verify delivery.Verifier,
	person func([]byte) (delivery.ApprovalStatement, string, error)) (deliveryrecord.PinListing, error) {
	stream := delivery.PinStream(svc)
	v := func(b []byte) error { return verify(stream, 0, b) }
	open := func(path string) (*remote.Repository, error) { return d.Records(path) }
	repo, err := open(streams)
	if err != nil {
		return deliveryrecord.PinListing{}, err
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	return reader.ListPins(bounded, repo, ns, svc, v, deliveryrecord.PinApproval(open, person, v))
}
