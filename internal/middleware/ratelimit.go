package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"logline/internal/auth"

	"golang.org/x/time/rate"
)

type RateLimiterStore struct {
	limiters sync.Map
	rate     rate.Limit
	burst    int
}

func NewRateLimiterStore(r float64, burst int, cleanupInterval time.Duration) *RateLimiterStore {
	s := &RateLimiterStore{
		rate:  rate.Limit(r),
		burst: burst,
	}

	go s.cleanup(cleanupInterval)

	return s
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func (s *RateLimiterStore) getLimiter(key string) *rate.Limiter {
	now := time.Now()

	if v, ok := s.limiters.Load(key); ok {
		e := v.(*entry)
		e.lastSeen = now
		return e.limiter
	}

	e := &entry{
		limiter:  rate.NewLimiter(s.rate, s.burst),
		lastSeen: now,
	}

	actual, _ := s.limiters.LoadOrStore(key, e)
	return actual.(*entry).limiter
}

// evict limiters not seen in the last 10 minutes
func (s *RateLimiterStore) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		s.limiters.Range(func(key, value any) bool {
			e := value.(*entry)
			if e.lastSeen.Before(cutoff) {
				s.limiters.Delete(key)
			}
			return true
		})
	}
}

func RateLimit(store *RateLimiterStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := auth.KeyFromContext(r.Context())
			if apiKey == nil {
				http.Error(w, `{"error":"missing api key"}`, http.StatusUnauthorized)
				return
			}

			limiter := store.getLimiter(apiKey.KeyPrefix)

			if !limiter.Allow() {
				retryAfter := int(1.0 / float64(store.rate))
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
