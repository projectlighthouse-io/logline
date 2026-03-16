package auth

import (
	"context"
	"net/http"
	"strings"
)

type authContextKey string

const ApiKeyContextKey authContextKey = "api_key"

func Middleware(store *ApiKeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := extractBearerToken(r)
			if !ok {
				http.Error(w, `{"error":"missing or malformed authorization header"}`, http.StatusUnauthorized)
				return
			}

			key, err := store.Validate(r.Context(), token)
			if err != nil {
				http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
				return
			}

			if key.RevokedAt != nil {
				http.Error(w, `{"error":"api key has been revoked"}`, http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), ApiKeyContextKey, key)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}

	return token, true
}

func KeyFromContext(ctx context.Context) *ApiKey {
	key, _ := ctx.Value(ApiKeyContextKey).(*ApiKey)
	return key
}
