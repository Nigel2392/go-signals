//go:build !async && !sync
// +build !async,!sync

package develop

// AsyncEnabled reports if the app must be run in asynchronous publisher mode.
const AsyncEnabled = false

// SyncEnabled reports if the app must be run in synchronous publisher mode.
const SyncEnabled = false
