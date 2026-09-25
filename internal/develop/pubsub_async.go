//go:build async
// +build async

package develop

const AsyncEnabled = true

func SyncOrAsyncBool(_ bool) bool {
	return true
}
