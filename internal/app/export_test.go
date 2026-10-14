package app

import "github.com/Jaydee94/remedy/internal/store"

// PickHost is pickHost for the tests of this package.
var PickHost = func(entries []store.HostEntry, name string) (*store.HostEntry, error) { return pickHost(entries, name) }
