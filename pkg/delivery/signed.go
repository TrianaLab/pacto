package delivery

// SignedMove says e is a signed move: a line read from an operation record
// on a history whose signatures were checked. A record id alone is not a
// signature: an unverified history, a line read from an Operation's status
// and an unrecordable record are never signed moves.
func SignedMove(h History, e HistoryEntry) bool {
	return recordMove(e) && h.Verified
}
