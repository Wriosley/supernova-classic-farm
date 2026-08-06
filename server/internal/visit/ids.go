// Package visit implements Phase 3's friend-farm visit sessions: the
// visitor-side orchestration Service (FriendSvr check + Owner Zone routing),
// the in-memory owner-side VisitRegistry, and OwnerService, which combines
// them with the public farm view epoch lifecycle and presence pushes.
package visit

import "crypto/rand"

// newRandomID mints one random 16-byte identity (visit_id or
// farm_view_epoch), matching the friend package's newRelationID convention.
func newRandomID() ([]byte, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	return id, nil
}
