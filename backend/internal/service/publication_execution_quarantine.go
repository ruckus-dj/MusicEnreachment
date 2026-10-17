package service

import "sync"

// quarantinedPublicationScopes are deliberately process-rooted. An execution
// with unknown quiescence must retain its locks, stage cleanup callback, and
// descriptor owners even if every ordinary reference to its scope is dropped.
var quarantinedPublicationScopes = struct {
	sync.Mutex
	scopes map[*PublicationExecutionScope]struct{}
}{scopes: make(map[*PublicationExecutionScope]struct{})}

func (scope *PublicationExecutionScope) quarantineLocked() {
	quarantinedPublicationScopes.Lock()
	quarantinedPublicationScopes.scopes[scope] = struct{}{}
	quarantinedPublicationScopes.Unlock()
}
