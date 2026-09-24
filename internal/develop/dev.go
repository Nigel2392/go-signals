package develop

func SyncOrAsyncBool(b bool) bool {
	if AsyncEnabled {
		return true
	}
	if SyncEnabled {
		return false
	}
	return b
}
