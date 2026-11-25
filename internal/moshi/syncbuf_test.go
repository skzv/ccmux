package moshi

import (
	"fmt"
	"sync"
	"testing"
)

func TestSyncBuffer_ConcurrentWrites(t *testing.T) {
	var buf syncBuffer
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = fmt.Fprintf(&buf, "g%d-%d\n", id, i)
			}
		}(g)
	}
	wg.Wait()
	if got := len(buf.String()); got == 0 {
		t.Fatal("expected non-empty output")
	}
}
