/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
)

func encode(t *testing.T, r rawEvent) []byte {
	t.Helper()
	b, err := binary.Append(nil, binary.LittleEndian, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != RawEventSize {
		t.Fatalf("rawEvent encodes to %d bytes, want %d (struct event in sockstate.c)", len(b), RawEventSize)
	}
	return b
}

func TestDecodeEvent_IPv4Listen(t *testing.T) {
	r := rawEvent{Kind: uint8(EventListen), Family: afInet, Netns: 4026532000, PID: 42, LPort: 8080}
	copy(r.LAddr[:], []byte{10, 0, 0, 7})
	copy(r.Comm[:], "server")
	ev, err := DecodeEvent(encode(t, r))
	if err != nil {
		t.Fatal(err)
	}
	want := Event{
		Kind: EventListen, Netns: 4026532000, LocalAddr: netip.MustParseAddr("10.0.0.7"), LocalPort: 8080,
		RemoteAddr: netip.MustParseAddr("0.0.0.0"), PID: 42, Comm: "server",
	}
	if ev != want {
		t.Fatalf("got %+v, want %+v", ev, want)
	}
}

func TestDecodeEvent_IPv6Connect(t *testing.T) {
	r := rawEvent{Kind: uint8(EventConnectFail), Family: afInet6, Netns: 7, DPort: 5432}
	r.DAddr = netip.MustParseAddr("fd00::1").As16()
	ev, err := DecodeEvent(encode(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if !ev.IPv6 || ev.Kind != EventConnectFail || ev.RemoteAddr != netip.MustParseAddr("fd00::1") || ev.RemotePort != 5432 {
		t.Fatalf("got %+v", ev)
	}
}

func TestDecodeEvent_Rejects(t *testing.T) {
	for name, tc := range map[string]struct {
		b    []byte
		want string
	}{
		"short":          {b: []byte{1, 2, 3}, want: "decode event:"},
		"unknown kind":   {b: encode(t, rawEvent{Kind: 9, Family: afInet}), want: "unknown kind 9"},
		"zero kind":      {b: encode(t, rawEvent{Kind: 0, Family: afInet}), want: "unknown kind 0"},
		"unknown family": {b: encode(t, rawEvent{Kind: 1, Family: 1}), want: "unknown family 1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeEvent(tc.b)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDecodeEvent_CookieMonoCgroupAndComm(t *testing.T) {
	r := rawEvent{Kind: uint8(EventListen), Family: afInet, LPort: 80, Cookie: 4096, Mono: 123456789, Cgroup: 90556}
	copy(r.Comm[:], "evil\n\"<>&\\\x7fé")
	ev, err := DecodeEvent(encode(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if ev.NetnsCookie != 4096 || ev.Mono != 123456789 || ev.CgroupID != 90556 || ev.Comm != "evil?????????" {
		t.Fatalf("got cookie=%d mono=%d cgroup=%d comm=%q", ev.NetnsCookie, ev.Mono, ev.CgroupID, ev.Comm)
	}
}

func TestSanitizeComm(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"nginx\x00garbage":        "nginx",
		"0123456789abcdefXYZ":     "0123456789abcde", // no NUL: capped at 15
		"a b~":                    "a b~",
		"\x01tab\there\x00":       "?tab?here",
		"json\"\\<>&safe\x00\x00": "json?????safe",
	} {
		if got := sanitizeComm([]byte(in)); got != want {
			t.Errorf("sanitizeComm(%q) = %q, want %q", in, got, want)
		}
	}
}
