//go:build windows

package config

func acquireUpdateLock(string) (func(), error) {
	return func() {}, nil
}
