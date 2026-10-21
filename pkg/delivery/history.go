package delivery

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// StoredRecord is one record read back from the registry: its id (the sha256
// of its signed payload), the referrer manifest that carries it and what it
// says.
type StoredRecord struct {
	ID        string    `json:"id"`
	Manifest  string    `json:"manifest"`
	Statement Statement `json:"statement"`
}

// Fold is a stream's records in seq order, its head and everything wrong
// with it: missing seqs, two records for one seq, a record that does not chain
// from the one before, a gap record and a change that started from a version
// the previous record did not leave. It reports them and resolves none.
type Fold struct {
	Records  []StoredRecord
	Head     string
	Problems []string
}

// FoldStream sorts a stream's records and checks the chain.
func FoldStream(recs []StoredRecord) Fold {
	f := Fold{Records: slices.Clone(recs)}
	slices.SortStableFunc(f.Records, func(a, b StoredRecord) int {
		return cmp.Or(cmp.Compare(a.Statement.Predicate.Chain.Seq, b.Statement.Predicate.Chain.Seq), strings.Compare(a.ID, b.ID))
	})
	var prev *StoredRecord
	lastAfter, lastSeq := "", int64(0)
	for i := range f.Records {
		r := &f.Records[i]
		p := r.Statement.Predicate
		seq := p.Chain.Seq
		switch {
		case prev != nil && seq == lastSeq:
			f.Problems = append(f.Problems, fmt.Sprintf("seq %d has two records, %s and %s: the stream forked", seq, prev.ID, r.ID))
			continue
		case seq > lastSeq+1:
			f.Problems = append(f.Problems, fmt.Sprintf("%s missing: the registry holds no record for them", seqRange(lastSeq+1, seq-1)))
		case prev != nil && p.Chain.Parent != prev.ID:
			f.Problems = append(f.Problems, fmt.Sprintf("the record at seq %d does not chain from %s at seq %d", seq, prev.ID, lastSeq))
		case prev == nil && seq == 1 && p.Chain.Parent != "":
			f.Problems = append(f.Problems, fmt.Sprintf("the record at seq 1 names a parent, %s, so older history is missing", p.Chain.Parent))
		}
		if p.Gap != nil {
			f.Problems = append(f.Problems, fmt.Sprintf("seq %d is a gap: the operation with local seq %d was gone before it was recorded", seq, p.Gap.Seq))
		}
		if o := p.Outcome; o != nil && o.Pointer.After != "" {
			if lastAfter != "" && o.Pointer.Before != lastAfter {
				f.Problems = append(f.Problems, fmt.Sprintf("seq %d moved the Stage from %s, but the change before it left %s: "+
					"something moved it with no record", seq, o.Pointer.Before, lastAfter))
			}
			lastAfter = o.Pointer.After
		}
		prev, lastSeq, f.Head = r, seq, r.ID
	}
	return f
}

func seqRange(from, to int64) string {
	if from == to {
		return fmt.Sprintf("seq %d is", from)
	}
	return fmt.Sprintf("seqs %d to %d are", from, to)
}

// RecordEntry is a record as a history line.
func RecordEntry(r StoredRecord) HistoryEntry {
	p := r.Statement.Predicate
	e := HistoryEntry{Seq: p.Chain.Seq, Operation: p.Intent.Name, Kind: p.Kind, Record: r.ID, Finished: p.Time.Finished,
		Phase: PhaseConcluded}
	if p.Type != nil {
		e.Type = p.Type.ID
	}
	if p.Intent.Params != nil {
		e.From, e.To = p.Intent.Params.From, p.Intent.Params.To
	}
	if p.Actor != nil {
		e.Actor, e.ActorClass = p.Actor.User, p.Actor.Class
	}
	if o := p.Outcome; o != nil {
		e.Outcome, e.Verification, e.Reason = o.Result, o.Verification, o.Reason
		if o.Pointer.After != "" {
			ptr := o.Pointer
			e.Pointer = &ptr
		}
	}
	switch {
	case p.Unrecordable != nil:
		e.Reason = "Unrecordable: " + p.Unrecordable.Check + ": " + p.Unrecordable.Message
	case p.Gap != nil:
		e.Reason = fmt.Sprintf("Gap: the operation with local seq %d was gone before it was recorded", p.Gap.Seq)
	}
	return e
}
