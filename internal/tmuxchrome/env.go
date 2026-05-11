package tmuxchrome

import "os"

func getenvImpl(name string) string { return os.Getenv(name) }
