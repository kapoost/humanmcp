package auth

import (
	"net/http"
	"strings"
)

type Auth struct {
	editToken string
}

func New(editToken string) *Auth {
	return &Auth{editToken: editToken}
}

// IsOwner checks if request carries the edit token
func (a *Auth) IsOwner(r *http.Request) bool {
	if a.editToken == "" {
		return false
	}
	// Check Authorization: Bearer <token>
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == a.editToken {
			return true
		}
	}
	// Check X-Edit-Token header
	if r.Header.Get("X-Edit-Token") == a.editToken {
		return true
	}
	// Check cookie (for web UI)
	cookie, err := r.Cookie("edit_token")
	if err == nil && cookie.Value == a.editToken {
		return true
	}
	return false
}

// RequireOwner is middleware that 401s if not owner
func (a *Auth) RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.IsOwner(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized — edit token required"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
