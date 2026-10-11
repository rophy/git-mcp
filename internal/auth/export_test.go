package auth

import "time"

// SetRefetchInterval overrides the forced JWKS fetch limit in tests.
func (a *Authenticator) SetRefetchInterval(d time.Duration) { a.refetchInterval = d }
