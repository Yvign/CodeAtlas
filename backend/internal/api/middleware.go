package api

import (
	"context"
	"net/http"
	"os"

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
		cookie, err := r.Cookie("codeatlas_jwt")
		if err != nil {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "missing_token", "message": "not authenticated"},
			})
			return
		}

		tok, err := jwt.Parse(cookie.Value, func(t *jwt.Token) (any, error) {
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
