package daemon

import "sync"

// resetClientCacheForTest clears the process-wide LocalClient/RemoteClient
// caches between tests.
func resetClientCacheForTest() {
	localClientOnce = sync.Once{}
	localClientVal = nil
	localClientErr = nil
	remoteClientCache = sync.Map{}
}
