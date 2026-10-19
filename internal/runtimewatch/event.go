/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package runtimewatch is the userspace logic of `pacto agent`: it maps
// workloads (pods, containers, systemd units, sessions) to cgroups and network
// namespaces from /proc and the host cgroupfs, aggregates the kernel's socket
// events per workload within fixed bounds and hands the result to a Sink as a
// runtimefacts.Snapshot. Everything that touches the kernel lives in
// internal/runtimewatch/ebpf; this package is pure and fully unit tested.
package runtimewatch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"
)

// EventKind is the socket state transition the eBPF program reported. The values
// match the EV_* constants in ebpf/bpf/sockstate.c.
type EventKind uint8

const (
	// EventListen is a socket entering LISTEN.
	EventListen EventKind = 1
	// EventUnlisten is a listening socket closing.
	EventUnlisten EventKind = 2
	// EventConnectOK is an outbound handshake reaching ESTABLISHED.
	EventConnectOK EventKind = 3
	// EventConnectFail is an outbound handshake going from SYN_SENT to CLOSE.
	EventConnectFail EventKind = 4
)

// Address families as the kernel reports them.
const (
	afInet  = 2
	afInet6 = 10
)

// Event is one decoded kernel socket event.
type Event struct {
	Kind EventKind
	// IPv6 is true for AF_INET6 sockets (including dual-stack listeners).
	IPv6 bool
	// CgroupID is the cgroup v2 id (the kernfs inode) of the cgroup that created
	// the socket: the primary attribution key. 0 when unknown (a pre-5.15 socket
	// tagged by net_cls or net_prio v1).
	CgroupID uint64
	// Netns is the inode of the socket's network namespace: the attribution key
	// when the cgroup names no workload.
	Netns uint32
	// NetnsCookie identifies the namespace for the life of the boot: inodes are
	// reused after a namespace dies, cookies never are. 0 on kernels without
	// net_cookie (< 5.12).
	NetnsCookie uint64
	// Mono is the kernel's CLOCK_MONOTONIC at the transition, in nanoseconds.
	Mono       uint64
	LocalAddr  netip.Addr
	LocalPort  uint16
	RemoteAddr netip.Addr
	RemotePort uint16
	// PID and Comm are only meaningful for LISTEN and UNLISTEN, which run in
	// process context. Connect outcomes often complete in softirq context.
	PID  uint32
	Comm string
}

// rawEvent mirrors struct event in sockstate.c byte for byte.
type rawEvent struct {
	Kind   uint8
	Pad    uint8
	Family uint16
	Netns  uint32
	PID    uint32
	LPort  uint16
	DPort  uint16
	LAddr  [16]byte
	DAddr  [16]byte
	Comm   [16]byte
	Cookie uint64
	Mono   uint64
	Cgroup uint64
}

// RawEventSize is the size of one ring buffer record.
const RawEventSize = 88

// DecodeEvent parses one ring buffer record (little endian, the bpfel target).
func DecodeEvent(b []byte) (Event, error) {
	// A record of another size comes from an object built for another layout.
	if len(b) != RawEventSize {
		return Event{}, fmt.Errorf("decode event: %d bytes, want %d", len(b), RawEventSize)
	}
	var r rawEvent
	// A fixed-size struct from a buffer of exactly its size cannot fail.
	_, _ = binary.Decode(b, binary.LittleEndian, &r)
	if r.Kind < uint8(EventListen) || r.Kind > uint8(EventConnectFail) {
		return Event{}, fmt.Errorf("decode event: unknown kind %d", r.Kind)
	}
	ev := Event{
		Kind:        EventKind(r.Kind),
		CgroupID:    r.Cgroup,
		Netns:       r.Netns,
		NetnsCookie: r.Cookie,
		Mono:        r.Mono,
		LocalPort:   r.LPort,
		RemotePort:  r.DPort,
		PID:         r.PID,
		Comm:        sanitizeComm(r.Comm[:]),
	}
	switch r.Family {
	case afInet:
		ev.LocalAddr = netip.AddrFrom4([4]byte(r.LAddr[:4]))
		ev.RemoteAddr = netip.AddrFrom4([4]byte(r.DAddr[:4]))
	case afInet6:
		ev.IPv6 = true
		ev.LocalAddr = netip.AddrFrom16(r.LAddr)
		ev.RemoteAddr = netip.AddrFrom16(r.DAddr)
	default:
		return Event{}, fmt.Errorf("decode event: unknown family %d", r.Family)
	}
	return ev, nil
}

// maxCommLen is the kernel's TASK_COMM_LEN without the terminating NUL.
const maxCommLen = 15

// sanitizeComm returns the comm up to its first NUL, capped at maxCommLen bytes,
// with every byte that is not printable ASCII, or that encoding/json escapes
// ("\<>&), replaced by '?'. Any process can set its comm (prctl PR_SET_NAME),
// so this keeps it readable and exactly one JSON byte per byte, which the
// published object's size bound counts on.
func sanitizeComm(b []byte) string {
	b, _, _ = bytes.Cut(b, []byte{0})
	b = b[:min(len(b), maxCommLen)]
	out := make([]byte, len(b))
	for i, c := range b {
		if c < 0x20 || c > 0x7e || strings.IndexByte(`"\<>&`, c) >= 0 {
			c = '?'
		}
		out[i] = c
	}
	return string(out)
}
