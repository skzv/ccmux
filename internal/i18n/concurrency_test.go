package i18n

import (
	"sync"
	"testing"
)

func TestConcurrentLanguageSwitch(t *testing.T) {
	defer SetLanguage("en")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			SetLanguage("zh")
			SetLanguage("en")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			_ = T("Sessions")
			_ = Current()
		}
	}()
	wg.Wait()
}
