package app

import (
	"context"
	"errors"
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
	cfg, err := d.publisherConfig(ctx)
	if err != nil {
		t.add("the publisher trust config: " + err.Error())
	}
	pins := d.readPins(ctx, cfg, delivery.TrustServices(stages), t)
	v := delivery.TrustOf(cfg, stages, ops, func(s delivery.ServiceRef) []delivery.IdentityPin { return pins[s] })
	v.AsOf, v.Completeness, v.Unreadable = d.Now().UTC().Format(time.RFC3339), "complete", t.unreadable
	if len(t.unreadable) > 0 {
		v.Completeness = "partial"
	}
	return &v
}

// PinsFunc reads the pins a service may use.
type PinsFunc func(ctx context.Context, s delivery.ServiceRef) ([]delivery.IdentityPin, error)

// InstallationTrust is what the installation trusts, read as PublisherTrust
// reads it: its publisher trust config and a reader of a service's usable
// pins (nil when the config names no streams repository, so no pin exists).
// A pin reader that cannot be built answers its reason on every read. It is
// ProvenanceChecker.Trust, so fleet provenance judges with the rule, policy
// and pins the promoter uses.
func (d *Delivery) InstallationTrust(ctx context.Context) (delivery.PublisherConfig, PinsFunc, error) {
	cfg, err := d.publisherConfig(ctx)
	if err != nil {
		return cfg, nil, err
	}
	read, err := d.pinsReader(ctx, cfg)
	switch {
	case err != nil:
		return cfg, func(context.Context, delivery.ServiceRef) ([]delivery.IdentityPin, error) { return nil, err }, nil
	case read == nil:
		return cfg, nil, nil
	}
	return cfg, func(ctx context.Context, s delivery.ServiceRef) ([]delivery.IdentityPin, error) {
		pins, _, err := read(ctx, s) // a pin it cannot use is simply not used
		return pins, err
	}, nil
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

// readPins reads the pins of services, delivery.DescribeParallel at once,
// naming every listing it could not read and every pin it could not use.
func (d *Delivery) readPins(ctx context.Context, cfg delivery.PublisherConfig, services []delivery.ServiceRef, t *trustReads) map[delivery.ServiceRef][]delivery.IdentityPin {
	if len(services) == 0 {
		return nil
	}
	read, err := d.pinsReader(ctx, cfg)
	if err != nil {
		t.add("pins: " + err.Error())
	}
	if read == nil {
		return nil
	}
	out := make(map[delivery.ServiceRef][]delivery.IdentityPin, len(services))
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(delivery.DescribeParallel)
	for _, svc := range services {
		g.Go(func() error {
			pins, unusable, err := read(ctx, svc)
			if err != nil {
				t.add("pins of " + svc.Name + ": " + err.Error())
			}
			for _, u := range unusable {
				t.add("pins of " + svc.Name + ": " + u)
			}
			mu.Lock()
			out[svc] = pins
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // each read names its own failure
	return out
}

// pinsReader reads a service's pins as the promoter reads them: each
// verified with cfg's pin signer and each citing an approval record that
// verifies. It answers the pins, the ones it could not use and a listing it
// could not read. No streams repository means no recorder, so no pin
// exists to read: nil and no error.
func (d *Delivery) pinsReader(ctx context.Context, cfg delivery.PublisherConfig) (func(context.Context, delivery.ServiceRef) ([]delivery.IdentityPin, []string, error), error) {
	if cfg.StreamsRepository == "" {
		return nil, nil
	}
	verify, err := d.pinVerifier(cfg)
	if err != nil {
		return nil, err
	}
	person := d.personApprovals(ctx)
	reader := &deliveryrecord.Reader{}
	return func(ctx context.Context, svc delivery.ServiceRef) ([]delivery.IdentityPin, []string, error) {
		return d.pinsOf(ctx, reader, cfg.StreamsRepository, svc, verify, person)
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

// pinsOf reads svc's usable pins from the streams repository, with the
// pins it could not use.
func (d *Delivery) pinsOf(ctx context.Context, reader *deliveryrecord.Reader, streams string, svc delivery.ServiceRef, verify delivery.Verifier,
	person func([]byte) (delivery.ApprovalStatement, string, error)) ([]delivery.IdentityPin, []string, error) {
	stream := delivery.PinStream(svc)
	v := func(b []byte) error { return verify(stream, 0, b) }
	open := func(path string) (*remote.Repository, error) { return d.Records(path) }
	repo, err := open(streams)
	var l deliveryrecord.PinListing
	if err == nil {
		bounded, cancel := delivery.WithRegistryTimeout(ctx)
		defer cancel()
		l, err = reader.ListPins(bounded, repo, svc, v, deliveryrecord.PinApproval(open, person, v))
	}
	if err != nil {
		return nil, nil, err
	}
	pins := make([]delivery.IdentityPin, 0, len(l.Pins))
	for _, p := range l.Pins {
		pins = append(pins, p.Pin)
	}
	return pins, l.Unreadable, nil
}
