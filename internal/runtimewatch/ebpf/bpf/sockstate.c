//go:build ignore

// sockstate: one BTF raw tracepoint on inet_sock_set_state that reports TCP
// listener lifecycle and outbound connect outcomes, keyed by the cgroup v2 id
// of the socket's owner and by its network namespace inode. Arch-neutral (no PT_REGS), so one bpfel object
// serves amd64 and arm64. Needs CONFIG_DEBUG_INFO_BTF=y and Linux >= 5.11
// (BPF memory charged to the cgroup), or >= 5.8 when the agent may lift
// RLIMIT_MEMLOCK (CAP_SYS_RESOURCE). The netns cookie needs >= 5.12 and reads
// as 0 before that.
//
// Never reads payloads: only addresses, ports, the cgroup id, the netns inode
// and cookie and, for process-context events, the pid and comm.
//
// The cgroup comes from the socket (sk_cgrp_data, set when the socket is
// allocated), never from the current task: connect outcomes complete in
// softirq or timer context, where the current task is whoever was interrupted.

// UAPI only (map types, __u32 and friends); kernel-internal types come from the
// CO-RE subset below, never from a committed vmlinux.h.
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

char LICENSE[] SEC("license") = "Dual MIT/GPL";

// Minimal CO-RE subset of the kernel types. Field names, not offsets, are what
// libbpf relocates against the running kernel's BTF; anonymous unions in the
// real layout are matched through.
struct in6_addr {
	__u8 s6_addr[16];
} __attribute__((preserve_access_index));

struct ns_common {
	unsigned int inum;
} __attribute__((preserve_access_index));

// net_cookie (u64, set at netns creation) exists from Linux 5.12; 5.7 to 5.11
// have an atomic64_t of the same name that CO-RE rejects as incompatible, so
// bpf_core_field_exists is false there and the cookie reads as 0.
struct net {
	struct ns_common ns;
	__u64 net_cookie;
} __attribute__((preserve_access_index));

typedef struct {
	struct net *net;
} __attribute__((preserve_access_index)) possible_net_t;

struct sock_common {
	__be32 skc_daddr;
	__be32 skc_rcv_saddr;
	__be16 skc_dport;
	__u16 skc_num;
	unsigned short skc_family;
	possible_net_t skc_net;
	struct in6_addr skc_v6_daddr;
	struct in6_addr skc_v6_rcv_saddr;
} __attribute__((preserve_access_index));

// A cgroup's id is its kernfs node id, which is also the inode of its
// directory in cgroupfs (u64 since Linux 5.5).
struct kernfs_node {
	__u64 id;
} __attribute__((preserve_access_index));

struct cgroup {
	struct kernfs_node *kn;
} __attribute__((preserve_access_index));

// sock_cgroup_data has two layouts. From Linux 5.15 it is a struct holding the
// cgroup pointer. Before, it is a union whose u64 val is the cgroup pointer
// only while bit 0 (is_data) is clear; net_cls or net_prio v1 set it and the
// pointer is lost. CO-RE picks whichever field the running kernel has.
struct sock_cgroup_data___new {
	struct cgroup *cgroup;
} __attribute__((preserve_access_index));

struct sock_cgroup_data___old {
	__u64 val;
} __attribute__((preserve_access_index));

struct sock_cgroup_data {
	int unused;
} __attribute__((preserve_access_index));

struct sock {
	struct sock_common __sk_common;
	__u16 sk_protocol;
	struct sock_cgroup_data sk_cgrp_data;
} __attribute__((preserve_access_index));

// The listener close path releases the port (skc_num = 0) before the LISTEN ->
// CLOSE transition is traced on current kernels; inet_sport still holds it.
struct inet_sock {
	struct sock sk;
	__be16 inet_sport;
} __attribute__((preserve_access_index));

#define AF_INET 2
#define AF_INET6 10
#define IPPROTO_TCP 6

#define TCP_ESTABLISHED 1
#define TCP_SYN_SENT 2
#define TCP_CLOSE 7
#define TCP_LISTEN 10

// Must match runtimewatch.EventKind.
#define EV_LISTEN 1
#define EV_UNLISTEN 2
#define EV_CONNECT_OK 3
#define EV_CONNECT_FAIL 4

// Must match runtimewatch's rawEvent layout (little endian, 88 bytes).
struct event {
	__u8 kind;
	__u8 pad;
	__u16 family;
	__u32 netns;
	__u32 pid;
	__u16 lport; // host byte order
	__u16 dport; // host byte order
	__u8 laddr[16];
	__u8 daddr[16];
	char comm[16];
	// Never reused, unlike the inode: tells a recycled inode from the namespace
	// that held it before. 0 when the kernel has no net_cookie.
	__u64 netns_cookie;
	// bpf_ktime_get_ns (CLOCK_MONOTONIC): lets userspace skip listener events
	// a /proc baseline taken later already reflects.
	__u64 ktime;
	// The cgroup v2 id of the socket's owner; 0 when unknown.
	__u64 cgroup_id;
};

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 256 * 1024);
} events SEC(".maps");

// Events lost because the ring buffer was full. Userspace sums the CPUs.
struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u64);
} drops SEC(".maps");

// sk_cgroup_id returns the cgroup v2 id the socket was created in, or 0 when
// the kernel lost it (pre-5.15 net_cls/net_prio tagging) or has neither
// layout. On a cgroup v1-only host every socket reads the v2 root's id, which
// names no workload; userspace then attributes by network namespace.
static __always_inline __u64 sk_cgroup_id(struct sock *sk)
{
	void *scd = &sk->sk_cgrp_data;
	struct cgroup *cg = 0;

	if (bpf_core_field_exists(struct sock_cgroup_data___new, cgroup)) {
		cg = BPF_CORE_READ((struct sock_cgroup_data___new *)scd, cgroup);
	} else if (bpf_core_field_exists(struct sock_cgroup_data___old, val)) {
		__u64 v = BPF_CORE_READ((struct sock_cgroup_data___old *)scd, val);
		if (v & 1)
			return 0;
		cg = (struct cgroup *)v;
	}
	if (!cg)
		return 0;
	return BPF_CORE_READ(cg, kn, id);
}

static __always_inline int classify(int oldstate, int newstate)
{
	if (newstate == TCP_LISTEN)
		return EV_LISTEN;
	if (oldstate == TCP_LISTEN && newstate == TCP_CLOSE)
		return EV_UNLISTEN;
	if (oldstate == TCP_SYN_SENT && newstate == TCP_ESTABLISHED)
		return EV_CONNECT_OK;
	if (oldstate == TCP_SYN_SENT && newstate == TCP_CLOSE)
		return EV_CONNECT_FAIL;
	return 0;
}

SEC("tp_btf/inet_sock_set_state")
int BPF_PROG(inet_sock_set_state, struct sock *sk, int oldstate, int newstate)
{
	int kind = classify(oldstate, newstate);
	if (!kind)
		return 0;
	if (sk->sk_protocol != IPPROTO_TCP)
		return 0;

	__u16 family = sk->__sk_common.skc_family;
	if (family != AF_INET && family != AF_INET6)
		return 0;

	__u16 lport = sk->__sk_common.skc_num;
	if (!lport) {
		// Past struct sock, so a direct load is rejected; probe-read it instead.
		__be16 sport = 0;
		bpf_core_read(&sport, sizeof(sport), &((struct inet_sock *)sk)->inet_sport);
		lport = __builtin_bswap16(sport);
	}
	// A listener with no port is not one anybody can reach (and the API
	// rejects port 0); never report it.
	if (!lport && (kind == EV_LISTEN || kind == EV_UNLISTEN))
		return 0;

	struct event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
	if (!e) {
		__u32 zero = 0;
		__u64 *n = bpf_map_lookup_elem(&drops, &zero);
		if (n)
			*n += 1;
		return 0;
	}

	e->kind = kind;
	e->pad = 0;
	e->family = family;
	struct net *net = sk->__sk_common.skc_net.net;
	e->netns = net->ns.inum;
	e->netns_cookie = 0;
	if (bpf_core_field_exists(net->net_cookie))
		bpf_core_read(&e->netns_cookie, sizeof(e->netns_cookie), &net->net_cookie);
	e->ktime = bpf_ktime_get_ns();
	e->cgroup_id = sk_cgroup_id(sk);
	e->lport = lport;
	e->dport = __builtin_bswap16(sk->__sk_common.skc_dport);
	__builtin_memset(e->laddr, 0, sizeof(e->laddr));
	__builtin_memset(e->daddr, 0, sizeof(e->daddr));
	if (family == AF_INET) {
		bpf_core_read(e->laddr, 4, &sk->__sk_common.skc_rcv_saddr);
		bpf_core_read(e->daddr, 4, &sk->__sk_common.skc_daddr);
	} else {
		bpf_core_read(e->laddr, 16, &sk->__sk_common.skc_v6_rcv_saddr);
		bpf_core_read(e->daddr, 16, &sk->__sk_common.skc_v6_daddr);
	}
	// pid is in the initial pid namespace and meaningless for CONNECT_* (often
	// softirq context); userspace only keeps comm, and only for LISTEN.
	e->pid = bpf_get_current_pid_tgid() >> 32;
	bpf_get_current_comm(e->comm, sizeof(e->comm));

	bpf_ringbuf_submit(e, 0);
	return 0;
}
