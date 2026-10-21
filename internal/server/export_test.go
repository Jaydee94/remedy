package server

// SetAuthMode sets the mode AuthMode answers, for the tests: today the runner has no other mode than subscription, and
// cycle 7b will have it report one.
func (r *RunnerStatus) SetAuthMode(mode string) {
	r.mu.Lock()
	r.authMode = mode
	r.mu.Unlock()
}
