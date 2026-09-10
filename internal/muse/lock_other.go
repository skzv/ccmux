//go:build !darwin && !linux

package muse

import "fmt"

func lockSession(string) (func(), error) {
	return nil, fmt.Errorf("Muse session deletion requires macOS or Linux")
}
