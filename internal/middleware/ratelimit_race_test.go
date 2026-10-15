package middleware

import (
	"sync"
	"testing"
	"time"
)

// run with -race: many requests for the same key touch the same entry concurrently
func TestGetLimiter_ConcurrentSameKey(t *testing.T) {
	limiterStore := NewRateLimiterStore(1000, 1000, time.Hour)

	var waitGroup sync.WaitGroup
	for i := 0; i < 50; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			limiterStore.getLimiter("lgl_samekey")
		}()
	}
	waitGroup.Wait()
}
