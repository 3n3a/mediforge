package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BearerAuth wraps handler with Authorization: Bearer <token> check using
// constant-time comparison. Returns 401 on mismatch.
func BearerAuth(token string, next http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		got := []byte(strings.TrimPrefix(h, prefix))
		if subtle.ConstantTimeCompare(got, tokenBytes) != 1 {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
