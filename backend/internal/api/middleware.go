package api

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const userIDKey contextKey = "userID"

func UserIDFromContext(r *http.Request) (string, bool) {
	id, ok := r.Context().Value(userIDKey).(string)
	return id, ok
}

func JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "missing_token", "message": "authorization header required"},
			})
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(os.Getenv("CODEATLAS_JWT_SECRET")), nil
		}, jwt.WithValidMethods([]string{"HS256"}))

		if err != nil || !tok.Valid {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "invalid_token", "message": "token is invalid or expired"},
			})
			return
		}

		claims, ok := tok.Claims.(jwt.MapClaims)
		if !ok {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "invalid_token", "message": "token is invalid or expired"},
			})
			return
		}

		userID, ok := claims["sub"].(string)
		if !ok || userID == "" {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "invalid_token", "message": "token is invalid or expired"},
			})
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
