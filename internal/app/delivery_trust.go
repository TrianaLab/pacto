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
// as the promoter reads them: each verified with cfg's pin signer and each
// citing an approval record that verifies. No streams repository means no
// recorder, so no pin exists to read.
func (d *Delivery) readPins(ctx context.Context, cfg delivery.PublisherConfig, services []delivery.ServiceRef, t *trustReads) map[delivery.ServiceRef][]delivery.IdentityPin {
	if cfg.StreamsRepository == "" || len(services) == 0 {
		return nil
	}
	verify, err := d.pinVerifier(cfg)
	if err != nil {
		t.add("pins: " + err.Error())
		return nil
	}
	person := d.personApprovals(ctx)
	reader := &deliveryrecord.Reader{}
	out := make(map[delivery.ServiceRef][]delivery.IdentityPin, len(services))
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(delivery.DescribeParallel)
	for _, svc := range services {
		g.Go(func() error {
			pins := d.pinsOf(ctx, reader, cfg.StreamsRepository, svc, verify, person, t)
			mu.Lock()
			out[svc] = pins
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // each read names its own failure
	return out
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

// pinsOf reads svc's usable pins from the streams repository, naming every
// pin it could not use and a listing it could not read.
func (d *Delivery) pinsOf(ctx context.Context, reader *deliveryrecord.Reader, streams string, svc delivery.ServiceRef, verify delivery.Verifier,
	person func([]byte) (delivery.ApprovalStatement, string, error), t *trustReads) []delivery.IdentityPin {
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
		t.add("pins of " + svc.Name + ": " + err.Error())
		return nil
	}
	for _, u := range l.Unreadable {
		t.add("pins of " + svc.Name + ": " + u)
	}
	pins := make([]delivery.IdentityPin, 0, len(l.Pins))
	for _, p := range l.Pins {
		pins = append(pins, p.Pin)
	}
	return pins
}
