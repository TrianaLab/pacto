//go:build linux

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package ebpf is the kernel side of `pacto agent`: it loads the sockstate
// program, attaches it to tp_btf/inet_sock_set_state and streams its events.
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
)

// sockstate_bpfel.go and sockstate_bpfel.o are generated from bpf/sockstate.c by
// `make bpf-generate` (bpf2go in a pinned Linux image) and committed.

// Reasons the agent publishes while the kernel program is not attached.
const (
	reasonLoadFailed   = "LoadFailed"
	reasonAttachFailed = "AttachFailed"
	reasonReaderFailed = "RingBufferFailed"
)

// probe is the attached program: everything the kernel holds for us.
type probe struct {
	objs   sockstateObjects
	link   link.Link
	reader *ringbuf.Reader
}

// attach loads the object, attaches the tp_btf program and opens the ring
// buffer. On error everything already acquired is released.
func attach(ringbufSize uint32) (*probe, string, error) {
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
	return p, "", nil
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
	if p.reader != nil {
		_ = p.reader.Close()
	}
	if p.link != nil {
		_ = p.link.Close()
	}
	_ = p.objs.Close()
}

// drops sums the per-CPU drop counter.
func (p *probe) drops() (uint64, error) {
	var perCPU []uint64
	if err := p.objs.Drops.Lookup(uint32(0), &perCPU); err != nil {
		return 0, err
	}
	var total uint64
	for _, n := range perCPU {
		total += n
	}
	return total, nil
}

// read forwards decoded events until the reader is closed.
func (p *probe) read(ctx context.Context, out chan<- runtimewatch.Event) error {
	log := logging.LoggerFromContext(ctx)
	var rec ringbuf.Record
	for {
		if err := p.reader.ReadInto(&rec); err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return nil
			}
			return err
		}
		ev, err := runtimewatch.DecodeEvent(rec.RawSample)
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
	// dropBase carries the drop count of earlier attachments, so the published
	// total stays cumulative across re-attach.
	dropBase uint64
	current  *probe
	status   runtimewatch.CollectorStatus
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
	st.Dropped = l.dropBase
	if l.current != nil {
		if n, err := l.current.drops(); err == nil {
			st.Dropped += n
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
	}
	if reason == reasonReaderFailed {
		// Events the kernel produced while nothing read them are lost without
		// being counted: count at least one, so the gap shows as a drop.
		l.dropBase++
	}
	l.current = p
	l.status = runtimewatch.CollectorStatus{}
	if err != nil {
		l.status = runtimewatch.CollectorStatus{Failed: true, Reason: reason, Message: err.Error()}
	}
	if p != nil {
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
		p, reason, err := attach(l.ringbufSize)
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

		stop := context.AfterFunc(ctx, func() { _ = p.reader.Close() })
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
