//go:build sync
// +build sync

package develop

const AsyncEnabled = false

func SyncOrAsyncBool(b bool) bool {
	return false
}
