package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "middleware-test-secret"

// makeToken signs a JWT with the given claims using testJWTSecret.
func makeToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("makeToken: %v", err)
	}
	return signed
}

// echoUserID is a terminal handler that writes the userID from context to the body.
var echoUserID = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	id, ok := UserIDFromContext(r)
	if !ok {
		http.Error(w, "no userID in context", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(id))
})

func TestJWTMiddleware_MissingHeader(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)

	req := httptest.NewRequest(http.MethodGet, "/repos", nil)
	w := httptest.NewRecorder()

	JWTMiddleware(echoUserID).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTMiddleware_MalformedHeader(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)

	req := httptest.NewRequest(http.MethodGet, "/repos", nil)
	req.Header.Set("Authorization", "Token somethingelse")
	w := httptest.NewRecorder()

	JWTMiddleware(echoUserID).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTMiddleware_TamperedToken(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)

	valid := makeToken(t, jwt.MapClaims{
		"sub": "user-123",
		"exp": jwt.NewNumericDate(time.Now().Add(time.Hour)),
		"iat": jwt.NewNumericDate(time.Now()),
	})
	tampered := valid[:len(valid)-4] + "XXXX"

	req := httptest.NewRequest(http.MethodGet, "/repos", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	w := httptest.NewRecorder()

	JWTMiddleware(echoUserID).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTMiddleware_ExpiredToken(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)

	expired := makeToken(t, jwt.MapClaims{
		"sub": "user-123",
		"exp": jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		"iat": jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
	})

	req := httptest.NewRequest(http.MethodGet, "/repos", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()

	JWTMiddleware(echoUserID).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJWTMiddleware_ValidToken(t *testing.T) {
	t.Setenv("CODEATLAS_JWT_SECRET", testJWTSecret)

	const wantUserID = "user-abc-123"
	token := makeToken(t, jwt.MapClaims{
		"sub": wantUserID,
		"exp": jwt.NewNumericDate(time.Now().Add(time.Hour)),
		"iat": jwt.NewNumericDate(time.Now()),
	})

	req := httptest.NewRequest(http.MethodGet, "/repos", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	JWTMiddleware(echoUserID).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != wantUserID {
		t.Errorf("expected userID %q in body, got %q", wantUserID, got)
	}
}
