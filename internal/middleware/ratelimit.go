package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"logline/internal/auth"
	"logline/internal/domain"

	"golang.org/x/time/rate"
)

type RateLimiterStore struct {
	limiters sync.Map
	rate     rate.Limit
	burst    int
}

func NewRateLimiterStore(r float64, burst int, cleanupInterval time.Duration) *RateLimiterStore {
	limiterStore := &RateLimiterStore{
		rate:  rate.Limit(r),
		burst: burst,
	}

	go limiterStore.cleanup(cleanupInterval)

	return limiterStore
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64 // unix nanoseconds; written by every request, read by cleanup
}

func (s *RateLimiterStore) getLimiter(key string) *rate.Limiter {
	now := time.Now()

	if cached, ok := s.limiters.Load(key); ok {
		existing := cached.(*entry)
		existing.lastSeen.Store(now.UnixNano())
		return existing.limiter
	}

	created := &entry{limiter: rate.NewLimiter(s.rate, s.burst)}
	created.lastSeen.Store(now.UnixNano())

	actual, _ := s.limiters.LoadOrStore(key, created)
	return actual.(*entry).limiter
}

// evict limiters not seen in the last 10 minutes
func (s *RateLimiterStore) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute).UnixNano()
		s.limiters.Range(func(key, value any) bool {
			current := value.(*entry)
			if current.lastSeen.Load() < cutoff {
				s.limiters.Delete(key)
			}
			return true
		})
	}
}

func RateLimit(limiterStore *RateLimiterStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := auth.KeyFromContext(r.Context())
			if apiKey == nil {
				writeError(w, http.StatusUnauthorized, "missing api key")
				return
			}

			limiter := limiterStore.getLimiter(apiKey.KeyPrefix)

			if !limiter.Allow() {
				retryAfter := int(1.0 / float64(limiterStore.rate))
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeError sends a JSON error body with the same shape as every handler's errors.
// http.Error would force Content-Type: text/plain.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(domain.ErrorResponse{Error: message})
}
