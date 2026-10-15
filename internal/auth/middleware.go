package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"logline/internal/domain"
)

type authContextKey string

const ApiKeyContextKey authContextKey = "api_key"

func Middleware(apiKeyStore *ApiKeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := extractBearerToken(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing or malformed authorization header")
				return
			}

			key, err := apiKeyStore.Validate(r.Context(), token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid api key")
				return
			}

			if key.RevokedAt != nil {
				writeError(w, http.StatusForbidden, "api key has been revoked")
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

// writeError sends a JSON error body with the same shape as every handler's errors.
// http.Error would force Content-Type: text/plain.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(domain.ErrorResponse{Error: message})
}
