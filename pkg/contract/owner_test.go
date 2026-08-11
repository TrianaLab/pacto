package contract

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOwner_IsEmpty(t *testing.T) {
	if !(Owner{}).IsEmpty() {
		t.Fatal("zero Owner should be empty")
	}
	if (Owner{Team: "team/x"}).IsEmpty() {
		t.Fatal("owner with team should not be empty")
	}
	if (Owner{Contacts: []OwnerContact{{Type: "email", Value: "a@b.c"}}}).IsEmpty() {
		t.Fatal("owner with contacts should not be empty")
	}
}

func TestOwner_DisplayString(t *testing.T) {
	if got := (Owner{Team: "team/x", DRI: "ed"}).DisplayString(); got != "team/x" {
		t.Fatalf("want team, got %q", got)
	}
	if got := (Owner{DRI: "ed"}).DisplayString(); got != "ed" {
		t.Fatalf("want dri fallback, got %q", got)
	}
	if got := (Owner{}).DisplayString(); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestOwner_Equal(t *testing.T) {
	a := Owner{Team: "t", DRI: "d", Contacts: []OwnerContact{{Type: "email", Value: "x"}}}
	b := Owner{Team: "t", DRI: "d", Contacts: []OwnerContact{{Type: "email", Value: "x"}}}
	if !a.Equal(b) {
		t.Fatal("identical owners should be equal")
	}
	if a.Equal(Owner{Team: "t"}) {
		t.Fatal("different contacts should not be equal")
	}
	// Same length contacts but different values
	c := Owner{Team: "t", DRI: "d", Contacts: []OwnerContact{{Type: "email", Value: "y"}}}
	if a.Equal(c) {
		t.Fatal("owners with same length but different contact values should not be equal")
	}
}

// Contact list POSITION is not identity. The schema gives the order no meaning,
// so an editor that re-sorts the block, or a generator that emits contacts in map
// order, must not turn one owner into two — which, on a service whose revisions
// then disagree, is a dispute reported between an owner and itself.
func TestOwner_EqualComparesContactsAsASet(t *testing.T) {
	mail := OwnerContact{Type: "email", Value: "sre@acme.com", Purpose: "escalation"}
	chat := OwnerContact{Type: "chat", Value: "#sre", Purpose: "support"}
	a := Owner{Team: "t", Contacts: []OwnerContact{mail, chat}}
	b := Owner{Team: "t", Contacts: []OwnerContact{chat, mail}}
	if !a.Equal(b) {
		t.Fatal("reordering the contact list must not change who the owner is")
	}
	// Contacts-only owners are compared the same way; they have no key to fall back on.
	if !(Owner{Contacts: []OwnerContact{mail, chat}}).Equal(Owner{Contacts: []OwnerContact{chat, mail}}) {
		t.Fatal("a contacts-only owner is its set of contacts, in any order")
	}
	// Set, not multiset-blind: a genuinely different contact is still a different owner.
	if a.Equal(Owner{Team: "t", Contacts: []OwnerContact{mail, {Type: "chat", Value: "#other"}}}) {
		t.Fatal("a different contact point is a different declaration")
	}
	// And a repeated contact is not the same as two distinct ones.
	if (Owner{Contacts: []OwnerContact{mail, mail}}).Equal(Owner{Contacts: []OwnerContact{mail, chat}}) {
		t.Fatal("duplicate contacts must not compare equal to distinct ones")
	}
}

// A set has no multiplicity. Listing one contact point twice names the same one
// contact point twice, so `[ops]` and `[ops, ops]` are one declaration written two
// ways — and a service whose revisions spell it each way is owned by one owner, not
// disputed between an owner and itself.
//
// The schema permits the repetition and says so (the `contacts` array in
// schema/pacto-v2.0.schema.json), the docs call the comparison a set, and this is
// where the three are made to agree.
func TestOwner_EqualNormalizesDuplicateContacts(t *testing.T) {
	mail := OwnerContact{Type: "email", Value: "ops@acme.com"}
	chat := OwnerContact{Type: "chat", Value: "#ops"}

	once := Owner{Contacts: []OwnerContact{mail}}
	twice := Owner{Contacts: []OwnerContact{mail, mail}}
	if !once.Equal(twice) {
		t.Fatal("saying one contact point twice does not create a second owner")
	}
	if !twice.Equal(once) {
		t.Fatal("equality must be symmetric")
	}

	// Repetition anywhere in the list, in any order, is still the same set.
	if !(Owner{Contacts: []OwnerContact{mail, chat, mail}}).Equal(Owner{Contacts: []OwnerContact{chat, mail}}) {
		t.Fatal("a repeated contact point does not add a member to the set")
	}

	// Normalizing duplicates away must not normalize a real difference away.
	if (Owner{Contacts: []OwnerContact{mail, mail}}).Equal(Owner{Contacts: []OwnerContact{mail, chat}}) {
		t.Fatal("a distinct contact point is still a distinct member")
	}
	// Purpose is part of the contact point, so two purposes are two members.
	withPurpose := OwnerContact{Type: "email", Value: "ops@acme.com", Purpose: "escalation"}
	if (Owner{Contacts: []OwnerContact{mail, mail}}).Equal(Owner{Contacts: []OwnerContact{mail, withPurpose}}) {
		t.Fatal("purpose distinguishes two contact points")
	}
}

// THE collision this key exists to close: a team named `alice` and a DRI named
// `alice` are two owners. The old identity was the display label, so they were
// one — one owner page, one ranking row, and a service whose two revisions named
// each of them was reported as consistently owned by a single owner who was
// really two people.
func TestOwnerKey_TeamAndDRINamespacesNeverCollide(t *testing.T) {
	team, ok := (Owner{Team: "alice"}).Key()
	if !ok || team.String() != "team:alice" {
		t.Fatalf("team key = %q/%v", team.String(), ok)
	}
	dri, ok := (Owner{DRI: "alice"}).Key()
	if !ok || dri.String() != "dri:alice" {
		t.Fatalf("dri key = %q/%v", dri.String(), ok)
	}
	if team == dri || team.String() == dri.String() {
		t.Fatal("a team and a DRI with the same name must be two canonical owners")
	}
	// They still share a label, which is exactly why the label cannot be the identity.
	if (Owner{Team: "alice"}).DisplayString() != (Owner{DRI: "alice"}).DisplayString() {
		t.Fatal("the fixture no longer collides, so this proves nothing")
	}
	// Neither answers the other's key, and neither answers the bare name.
	for _, tc := range []struct {
		o    Owner
		key  string
		want bool
	}{
		{Owner{Team: "alice"}, "team:alice", true},
		{Owner{Team: "alice"}, "dri:alice", false},
		{Owner{DRI: "alice"}, "dri:alice", true},
		{Owner{DRI: "alice"}, "team:alice", false},
		{Owner{Team: "alice"}, "alice", false},
		{Owner{DRI: "alice"}, "alice", false},
	} {
		if got := tc.o.IsKey(tc.key); got != tc.want {
			t.Errorf("%+v.IsKey(%q) = %v, want %v", tc.o, tc.key, got, tc.want)
		}
	}
	// Team wins over DRI, so one structured owner has one identity however the rest
	// of the block is spelled.
	platform := (Owner{Team: "platform", DRI: "alice"}).KeyString()
	if platform != "team:platform" || (Owner{Team: "platform", DRI: "bob"}).KeyString() != platform {
		t.Fatalf("same team, different DRI must be one canonical owner; got %q", platform)
	}
}

// The encoding carries values the Owner schema permits, including ones that look
// like the encoding itself. A casual delimiter would re-open the collision on the
// first team whose name contains a colon.
func TestOwnerKey_EncodingIsInjectiveAndRoundTrips(t *testing.T) {
	values := []string{
		"platform", "team/payments", "external/sendgrid", "a.b-c_d",
		"dri:alice", "team:alice", "a:b:c", ":leading", "trailing:",
		"with space", "  ", "ünïcode", `quote"and\slash`, "#hash", "?q=1&r=2",
	}
	seen := map[string]string{}
	for _, v := range values {
		for _, kind := range []OwnerKind{OwnerKindTeam, OwnerKindDRI} {
			k := OwnerKey{Kind: kind, Value: v}
			wire := k.String()
			back, err := ParseOwnerKey(wire)
			if err != nil {
				t.Errorf("ParseOwnerKey(%q): %v", wire, err)
				continue
			}
			if back != k {
				t.Errorf("%q round-tripped to %+v, want %+v", wire, back, k)
			}
			if prev, dup := seen[wire]; dup {
				t.Errorf("encoding is not injective: %+v and %s both encode to %q", k, prev, wire)
			}
			seen[wire] = string(kind) + "/" + v
			// And an owner declaring that value produces exactly this key.
			o := Owner{Team: v}
			if kind == OwnerKindDRI {
				o = Owner{DRI: v}
			}
			if got := o.KeyString(); got != wire {
				t.Errorf("Owner%+v.KeyString() = %q, want %q", o, got, wire)
			}
		}
	}
}

// Parsing fails closed. `alice` names a team and a DRI equally well; resolving it
// to either would silently show one owner somebody else's estate, and resolving
// it to both would merge them.
func TestParseOwnerKey_RejectsAnythingAmbiguous(t *testing.T) {
	for _, bad := range []string{"", "alice", "team", "team:", "dri:", "Team:alice", "owner:alice", "TEAM:alice", ":alice", "  :alice"} {
		if k, err := ParseOwnerKey(bad); err == nil {
			t.Errorf("ParseOwnerKey(%q) resolved to %+v, want a refusal", bad, k)
		}
	}
	// A contacts-only owner has no identity at all, and the zero key matches nobody.
	paged := Owner{Contacts: []OwnerContact{{Type: "chat", Value: "#sre"}}}
	if k, ok := paged.Key(); ok || !k.IsZero() || k.String() != "" {
		t.Fatalf("contacts-only owner produced key %+v/%v", k, ok)
	}
	if paged.IsKey("") || paged.IsKey("#sre") || (Owner{}).IsKey("") {
		t.Fatal("an owner with no canonical identity must match no key, including the empty one")
	}
}

// The label is presentation and says which namespace it came from, because two
// owners can print the same name.
func TestOwnerKey_LabelNamesTheNamespace(t *testing.T) {
	if got := (OwnerKey{Kind: OwnerKindTeam, Value: "alice"}).Label(); got != "alice (Team)" {
		t.Errorf("team label = %q", got)
	}
	if got := (OwnerKey{Kind: OwnerKindDRI, Value: "alice"}).Label(); got != "alice (DRI)" {
		t.Errorf("dri label = %q", got)
	}
	if got := (OwnerKey{}).Label(); got != "" {
		t.Errorf("zero label = %q", got)
	}
	if got := (OwnerKey{}).KindLabel(); got != "Team" {
		t.Errorf("zero kind label = %q", got)
	}
}

func TestOwner_MatchesFilter(t *testing.T) {
	o := Owner{Team: "Payments", DRI: "Eduardo", Contacts: []OwnerContact{{Type: "chat", Value: "#pay"}}}
	for _, q := range []string{"pay", "eduardo", "#pay"} {
		if !o.MatchesFilter(q) {
			t.Fatalf("expected match for %q", q)
		}
	}
	if o.MatchesFilter("nope") {
		t.Fatal("unexpected match")
	}
}

// IsKey and MatchesFilter answer two different questions, and the pair `team-a` /
// `team-a-platform` is where the difference stops being academic: a substring
// query matches both, and only one of them IS `team-a`.
func TestOwner_IsKeyIsExactCanonicalIdentity(t *testing.T) {
	a := Owner{Team: "team-a"}
	collider := Owner{Team: "team-a-platform"}
	if !a.IsKey("team:team-a") {
		t.Fatal("an owner must be its own canonical key")
	}
	if collider.IsKey("team:team-a") {
		t.Fatal("team-a-platform is a different owner from team-a")
	}
	if !collider.MatchesFilter("team-a") {
		t.Fatal("free-text owner search must still discover the substring collider")
	}
	// Case and contacts belong to search, never to identity: a key is compared as
	// the snapshot spells it, and a contact value is not an owner name.
	if a.IsKey("team:Team-A") {
		t.Fatal("canonical identity is not case-insensitive")
	}
	paged := Owner{Contacts: []OwnerContact{{Type: "chat", Value: "#team-a"}}}
	if paged.IsKey("#team-a") {
		t.Fatal("a contact value is not a canonical owner key")
	}
	if (Owner{}).IsKey("") {
		t.Fatal("an undeclared owner has no canonical key, so it matches none")
	}
	// The DRI is the canonical key when no team is declared — in the DRI namespace,
	// which is what keeps it apart from a team of the same name.
	if !(Owner{DRI: "alice"}).IsKey("dri:alice") {
		t.Fatal("the DRI is the canonical key when no team is declared")
	}
}

func TestOwner_YAMLRoundTrip(t *testing.T) {
	in := []byte("team: team/payments\ndri: eduardo.diaz\ncontacts:\n  - type: email\n    value: pay@acme.com\n    purpose: ownership\n")
	var o Owner
	if err := yaml.Unmarshal(in, &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if o.Team != "team/payments" || o.DRI != "eduardo.diaz" || len(o.Contacts) != 1 {
		t.Fatalf("bad parse: %+v", o)
	}
	out, err := yaml.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt Owner
	if err := yaml.Unmarshal(out, &rt); err != nil || !o.Equal(rt) {
		t.Fatalf("round trip mismatch: %v / %+v", err, rt)
	}
}

func TestOwner_StringRejected(t *testing.T) {
	var o Owner
	err := yaml.Unmarshal([]byte("just-a-string\n"), &o)
	if err == nil {
		t.Fatal("string owner must no longer unmarshal into Owner")
	}
}
