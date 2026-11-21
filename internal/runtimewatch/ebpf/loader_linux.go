//go:build linux

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package ebpf is the kernel side of `pacto agent`: it loads the sockstate
// program, attaches it to tp_btf/inet_sock_set_state and fentry/skb_consume_udp
// and streams their events.
// Kernel-touching, so it is covered by `make test-ebpf` (a privileged
// container) and the kind shards rather than the unit coverage gate.
package ebpf

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"

	"github.com/trianalab/pacto/v4/internal/runtimewatch"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// sockstate_bpfel.go and sockstate_bpfel.o are generated from bpf/sockstate.c by
// `make bpf-generate` (bpf2go in a pinned Linux image) and committed.

// Reasons the agent publishes while the kernel program is not attached.
const (
	reasonLoadFailed   = "LoadFailed"
	reasonAttachFailed = "AttachFailed"
	reasonReaderFailed = "RingBufferFailed"
)

// Reasons the DNS program is not attached while socket collection runs.
const (
	reasonDNSLoadFailed   = "DNSLoadFailed"
	reasonDNSAttachFailed = "DNSAttachFailed"
)

// socketObjects is the socket half of the generated object. cilium/ebpf loads
// only what the struct names, so a kernel that refuses the DNS program never
// takes socket collection down with it.
type socketObjects struct {
	InetSockSetState *ebpf.Program `ebpf:"inet_sock_set_state"`
	Events           *ebpf.Map     `ebpf:"events"`
	Drops            *ebpf.Map     `ebpf:"drops"`
}

// dnsObjects is the DNS half, loaded after the socket half.
type dnsObjects struct {
	DnsAnswer *ebpf.Program `ebpf:"dns_answer"`
	DnsEvents *ebpf.Map     `ebpf:"dns_events"`
	DnsDrops  *ebpf.Map     `ebpf:"dns_drops"`
}

// probe is the attached program: everything the kernel holds for us. The DNS
// pieces are nil when the DNS step failed; dnsReason and dnsErr say why.
type probe struct {
	objs   socketObjects
	link   link.Link
	reader *ringbuf.Reader

	dns       dnsObjects
	dnsLink   link.Link
	dnsReader *ringbuf.Reader
	dnsReason string
	dnsErr    error
}

// attachOptions are test seams of attach.
type attachOptions struct {
	// skipDNS leaves the DNS program unloaded, as a kernel that refuses it would.
	skipDNS bool
}

// attach loads the object, attaches the tp_btf program and opens the ring
// buffer, then does the same for the DNS program. On a socket error everything
// already acquired is released; a DNS error releases only the DNS pieces and
// is reported on the probe.
func attach(ringbufSize uint32, opts attachOptions) (*probe, string, error) {
	// Kernels >= 5.11 account BPF memory to the cgroup and ignore RLIMIT_MEMLOCK
	// (RemoveMemlock detects that and succeeds); older ones need it lifted, which
	// takes CAP_SYS_RESOURCE.
	memlockErr := rlimit.RemoveMemlock()

	spec, err := loadSockstate()
	if err != nil {
		return nil, reasonLoadFailed, fmt.Errorf("load object: %w", err)
	}
	if ringbufSize > 0 {
		spec.Maps[sockstateMapEvents].MaxEntries = ringbufSize
		spec.Maps[sockstateMapDnsEvents].MaxEntries = ringbufSize
	}
	p := &probe{}
	if err := spec.LoadAndAssign(&p.objs, nil); err != nil {
		var verr *ebpf.VerifierError
		if errors.As(err, &verr) {
			return nil, reasonLoadFailed, fmt.Errorf("verifier rejected the program: %+v", verr)
		}
		return nil, reasonLoadFailed, loadError(err, memlockErr)
	}
	p.link, err = link.AttachTracing(link.TracingOptions{
		Program:    p.objs.InetSockSetState,
		AttachType: ebpf.AttachTraceRawTp,
	})
	if err != nil {
		p.close()
		return nil, reasonAttachFailed, fmt.Errorf("attach tp_btf/inet_sock_set_state: %w", err)
	}
	p.reader, err = ringbuf.NewReader(p.objs.Events)
	if err != nil {
		p.close()
		return nil, reasonReaderFailed, fmt.Errorf("open ring buffer: %w", err)
	}
	if opts.skipDNS {
		p.dnsReason, p.dnsErr = reasonDNSLoadFailed, errors.New("the DNS program was not loaded")
		return p, "", nil
	}
	p.dnsReason, p.dnsErr = p.attachDNS(spec)
	if p.dnsErr != nil {
		p.closeDNS()
	}
	return p, "", nil
}

// attachDNS loads and attaches dns_answer and opens its ring buffer.
func (p *probe) attachDNS(spec *ebpf.CollectionSpec) (string, error) {
	if err := spec.LoadAndAssign(&p.dns, nil); err != nil {
		return reasonDNSLoadFailed, fmt.Errorf("load the DNS program: %w", err)
	}
	var err error
	p.dnsLink, err = link.AttachTracing(link.TracingOptions{
		Program:    p.dns.DnsAnswer,
		AttachType: ebpf.AttachTraceFEntry,
	})
	if err != nil {
		return reasonDNSAttachFailed, fmt.Errorf("attach fentry/skb_consume_udp: %w", err)
	}
	p.dnsReader, err = ringbuf.NewReader(p.dns.DnsEvents)
	if err != nil {
		return reasonDNSAttachFailed, fmt.Errorf("open the DNS ring buffer: %w", err)
	}
	return "", nil
}

// loadError explains the usual cause of a permission or memory failure: a
// kernel older than 5.11 that still enforces RLIMIT_MEMLOCK on BPF memory,
// with no CAP_SYS_RESOURCE to lift it.
func loadError(err, memlockErr error) error {
	if memlockErr != nil && (errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOMEM)) {
		return fmt.Errorf("load into kernel: %w (RLIMIT_MEMLOCK could not be lifted: %v; "+
			"the node agent needs Linux >= 5.11, where BPF memory is charged to the cgroup, "+
			"or Linux >= 5.8 with CAP_SYS_RESOURCE)", err, memlockErr)
	}
	return fmt.Errorf("load into kernel: %w", err)
}

// monotonicNs reads CLOCK_MONOTONIC, the clock bpf_ktime_get_ns stamps events
// with (a container in its own time namespace would see an offset; the agent
// runs in the host's). 0 when unreadable, which disables the comparison.
func monotonicNs() uint64 {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts) != nil {
		return 0
	}
	return uint64(ts.Nano())
}

func (p *probe) close() {
	p.closeDNS()
	if p.reader != nil {
		_ = p.reader.Close()
	}
	if p.link != nil {
		_ = p.link.Close()
	}
	closeAll(p.objs.InetSockSetState, p.objs.Events, p.objs.Drops)
}

// closeDNS releases only the DNS pieces.
func (p *probe) closeDNS() {
	if p.dnsReader != nil {
		_ = p.dnsReader.Close()
	}
	if p.dnsLink != nil {
		_ = p.dnsLink.Close()
	}
	closeAll(p.dns.DnsAnswer, p.dns.DnsEvents, p.dns.DnsDrops)
	p.dns, p.dnsLink, p.dnsReader = dnsObjects{}, nil, nil
}

// closeReaders unblocks both read loops.
func (p *probe) closeReaders() {
	_ = p.reader.Close()
	if p.dnsReader != nil {
		_ = p.dnsReader.Close()
	}
}

// closeAll closes kernel objects; cilium/ebpf's Close accepts a nil receiver.
func closeAll(objs ...interface{ Close() error }) {
	for _, o := range objs {
		_ = o.Close()
	}
}

// drops sums the per-CPU drop counter of the socket ring buffer.
func (p *probe) drops() (uint64, error) {
	return sumDrops(p.objs.Drops)
}

// dnsDrops sums the DNS ring buffer's drop counter; 0 without the DNS program.
func (p *probe) dnsDrops() (uint64, error) {
	if p.dns.DnsDrops == nil {
		return 0, nil
	}
	return sumDrops(p.dns.DnsDrops)
}

func sumDrops(m *ebpf.Map) (uint64, error) {
	var perCPU []uint64
	if err := m.Lookup(uint32(0), &perCPU); err != nil {
		return 0, err
	}
	var total uint64
	for _, n := range perCPU {
		total += n
	}
	return total, nil
}

// read forwards decoded events from both ring buffers until a reader is
// closed or fails; then it closes the other and returns the first error.
func (p *probe) read(ctx context.Context, out chan<- runtimewatch.Event) error {
	if p.dnsReader == nil {
		return drain(ctx, p.reader, runtimewatch.DecodeEvent, out)
	}
	errs := make(chan error, 2)
	go func() { errs <- drain(ctx, p.dnsReader, runtimewatch.DecodeDNS, out) }()
	go func() { errs <- drain(ctx, p.reader, runtimewatch.DecodeEvent, out) }()
	first := <-errs
	p.closeReaders()
	return errors.Join(first, <-errs)
}

// drain forwards one reader's decoded records until it is closed.
func drain(ctx context.Context, r *ringbuf.Reader, decode func([]byte) (runtimewatch.Event, error), out chan<- runtimewatch.Event) error {
	log := logging.LoggerFromContext(ctx)
	var rec ringbuf.Record
	for {
		if err := r.ReadInto(&rec); err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return nil
			}
			return err
		}
		ev, err := decode(rec.RawSample)
		if err != nil {
			log.Debug("Skipping undecodable event", "error", err.Error())
			continue
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return nil
		}
	}
}

// loader keeps the probe attached, retrying with backoff, and reports its
// health. A load or attach failure never stops the agent: it keeps publishing
// collection.state=Failed so a reader reports COLLECTION_FAILED instead of a
// silent host.
type loader struct {
	ringbufSize uint32
	// attached receives after every successful attach (buffer 1, never blocks).
	attached chan struct{}

	mu sync.Mutex
	// dropBase and dnsDropBase carry the drop counts of earlier attachments,
	// so the published totals stay cumulative across re-attach.
	dropBase    uint64
	dnsDropBase uint64
	current     *probe
	status      runtimewatch.CollectorStatus
}

// New returns the eBPF collector. Nothing touches the kernel until Run.
func New(ringbufSize uint32) (runtimewatch.Collector, error) {
	return newLoader(ringbufSize), nil
}

func newLoader(ringbufSize uint32) *loader {
	return &loader{
		ringbufSize: ringbufSize,
		attached:    make(chan struct{}, 1),
		// Until the first attach nothing is observed: never report Running.
		status: runtimewatch.CollectorStatus{
			Failed: true, Reason: ReasonAttaching, Message: "the eBPF program is not attached yet",
			DNSState: runtimefacts.StateFailed, DNSReason: runtimewatch.ReasonDNSNotAttached,
		},
	}
}

// Attached implements runtimewatch.Collector.
func (l *loader) Attached() <-chan struct{} { return l.attached }

// Mono implements runtimewatch.Collector.
func (l *loader) Mono() uint64 { return monotonicNs() }

// Status implements runtimewatch.Collector.
func (l *loader) Status() runtimewatch.CollectorStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.status
	st.Dropped, st.DNSDropped = l.dropBase, l.dnsDropBase
	if l.current != nil {
		if n, err := l.current.drops(); err == nil {
			st.Dropped += n
		}
		if n, err := l.current.dnsDrops(); err == nil {
			st.DNSDropped += n
		}
	}
	return st
}

func (l *loader) set(p *probe, reason string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current != nil && p == nil {
		if n, derr := l.current.drops(); derr == nil {
			l.dropBase += n
		}
		if n, derr := l.current.dnsDrops(); derr == nil {
			l.dnsDropBase += n
		}
	}
	if reason == reasonReaderFailed {
		// Events the kernel produced while nothing read them are lost without
		// being counted: count at least one, so the gap shows as a drop.
		l.dropBase++
	}
	l.current = p
	// The DNS fields outlive a detach: Snapshot reports DNS failed whenever the
	// collector is, and the next attach sets them from its probe.
	dnsState, dnsReason := l.status.DNSState, l.status.DNSReason
	l.status = runtimewatch.CollectorStatus{DNSState: dnsState, DNSReason: dnsReason}
	if err != nil {
		l.status.Failed, l.status.Reason, l.status.Message = true, reason, err.Error()
	}
	if p != nil {
		l.status.DNSState, l.status.DNSReason = runtimefacts.StateRunning, ""
		if p.dnsErr != nil {
			l.status.DNSState, l.status.DNSReason = runtimefacts.StateFailed, p.dnsReason
		}
		l.status.AttachedAt = time.Now()
		select {
		case l.attached <- struct{}{}:
		default:
		}
	}
}

// Run attaches, reads and re-attaches until ctx ends, then releases the kernel
// objects.
func (l *loader) Run(ctx context.Context, out chan<- runtimewatch.Event) {
	log := logging.LoggerFromContext(ctx)
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		p, reason, err := attach(l.ringbufSize, attachOptions{})
		if err != nil {
			l.set(nil, reason, err)
			log.Error("Kernel collection unavailable; retrying", "error", err, "reason", reason, "retryIn", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 5*time.Minute)
			continue
		}
		backoff = 5 * time.Second
		l.set(p, "", nil)
		log.Info("Kernel collection attached", "program", "tp_btf/inet_sock_set_state")
		if p.dnsErr != nil {
			log.Error("DNS answers unavailable; connections carry no names", "error", p.dnsErr, "reason", p.dnsReason)
		} else {
			log.Info("DNS capture attached", "program", "fentry/skb_consume_udp")
		}

		stop := context.AfterFunc(ctx, p.closeReaders)
		err = p.read(ctx, out)
		stop()
		if err != nil {
			l.set(nil, reasonReaderFailed, err)
			log.Error("Ring buffer read failed; re-attaching", "error", err)
		} else {
			l.set(nil, "", nil)
		}
		p.close()
	}
}
