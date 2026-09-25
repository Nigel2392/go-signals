//go:build !async && !sync
// +build !async,!sync

package develop

const AsyncEnabled = false

func SyncOrAsyncBool(b bool) bool {
	return b
}
