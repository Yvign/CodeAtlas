package db

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	url := os.Getenv("CODEATLAS_DB_TEST_URL")
	if url == "" {
		t.Skip("CODEATLAS_DB_TEST_URL not set")
	}
	database, err := New(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestFindOrCreateByProvider_IdempotentAfterTokenUpsert(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	users := NewPostgresUserRepository(database)
	tokens := NewPostgresTokenRepository(database)

	providerUserID := uuid.New().String()

	// First call: no oauth_token exists yet → creates a new user.
	userID1, err := users.FindOrCreateByProvider(ctx, "github", providerUserID, "testuser")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if userID1 == "" {
		t.Fatal("first call: expected non-empty userID")
	}
	t.Cleanup(func() {
		database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID1)
	})

	// Simulate the auth callback: upsert the oauth token.
	err = tokens.Upsert(ctx, OAuthToken{
		UserID:           userID1,
		Provider:         "github",
		ProviderUserID:   providerUserID,
		ProviderUsername: "testuser",
		AccessToken:      "enc-token-abc",
	})
	if err != nil {
		t.Fatalf("upsert token: %v", err)
	}

	// Second call: oauth_token now exists → returns the same userID.
	userID2, err := users.FindOrCreateByProvider(ctx, "github", providerUserID, "testuser")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if userID1 != userID2 {
		t.Errorf("expected same userID on second call: got %s and %s", userID1, userID2)
	}
}

func TestTokenRepository_UpsertInsertsAndUpdatesOnConflict(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	users := NewPostgresUserRepository(database)
	tokens := NewPostgresTokenRepository(database)

	providerUserID := uuid.New().String()
	userID, err := users.FindOrCreateByProvider(ctx, "github", providerUserID, "upsertuser")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Initial insert.
	err = tokens.Upsert(ctx, OAuthToken{
		UserID:           userID,
		Provider:         "github",
		ProviderUserID:   providerUserID,
		ProviderUsername: "upsertuser",
		AccessToken:      "first-token",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	got, err := tokens.FindByUserAndProvider(ctx, userID, "github")
	if err != nil || got == nil {
		t.Fatalf("find after insert: err=%v got=%v", err, got)
	}
	if got.AccessToken != "first-token" {
		t.Errorf("access_token after insert: got %q, want %q", got.AccessToken, "first-token")
	}

	// Conflict update.
	err = tokens.Upsert(ctx, OAuthToken{
		UserID:           userID,
		Provider:         "github",
		ProviderUserID:   providerUserID,
		ProviderUsername: "upsertuser",
		AccessToken:      "updated-token",
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err = tokens.FindByUserAndProvider(ctx, userID, "github")
	if err != nil || got == nil {
		t.Fatalf("find after update: err=%v got=%v", err, got)
	}
	if got.AccessToken != "updated-token" {
		t.Errorf("access_token after update: got %q, want %q", got.AccessToken, "updated-token")
	}
}

func TestGraphRepository_FindOrCreate_ReturnsSameIDForDuplicate(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	users := NewPostgresUserRepository(database)
	graphs := NewPostgresGraphRepository(database)

	providerUserID := uuid.New().String()
	userID, err := users.FindOrCreateByProvider(ctx, "github", providerUserID, "graphuser")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	entry := GraphRecord{
		UserID:   userID,
		Provider: "github",
		Owner:    "alice",
		RepoName: "myrepo",
		Branch:   "main",
	}

	rec1, err := graphs.FindOrCreate(ctx, entry)
	if err != nil {
		t.Fatalf("first FindOrCreate: %v", err)
	}
	if rec1.ID == "" {
		t.Fatal("expected non-empty ID on first call")
	}

	rec2, err := graphs.FindOrCreate(ctx, entry)
	if err != nil {
		t.Fatalf("second FindOrCreate: %v", err)
	}
	if rec1.ID != rec2.ID {
		t.Errorf("expected same ID for duplicate: got %s and %s", rec1.ID, rec2.ID)
	}
	if rec2.Status != "processing" {
		t.Errorf("expected status 'processing' after conflict update, got %q", rec2.Status)
	}
}

func TestGraphRepository_UpdateStatus(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	users := NewPostgresUserRepository(database)
	graphs := NewPostgresGraphRepository(database)

	providerUserID := uuid.New().String()
	userID, err := users.FindOrCreateByProvider(ctx, "github", providerUserID, "statususer")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	rec, err := graphs.FindOrCreate(ctx, GraphRecord{
		UserID:   userID,
		Provider: "github",
		Owner:    "alice",
		RepoName: "statusrepo",
		Branch:   "main",
	})
	if err != nil {
		t.Fatalf("FindOrCreate: %v", err)
	}

	// Update to failed with an error message.
	err = graphs.UpdateStatus(ctx, rec.ID, "failed", "build error: exit 1")
	if err != nil {
		t.Fatalf("UpdateStatus to failed: %v", err)
	}

	got, err := graphs.FindByID(ctx, rec.ID)
	if err != nil || got == nil {
		t.Fatalf("FindByID after failed: err=%v got=%v", err, got)
	}
	if got.Status != "failed" {
		t.Errorf("status: got %q, want %q", got.Status, "failed")
	}
	if got.ErrorMessage != "build error: exit 1" {
		t.Errorf("error_message: got %q, want %q", got.ErrorMessage, "build error: exit 1")
	}

	// Update to ready; error message should clear.
	err = graphs.UpdateStatus(ctx, rec.ID, "ready", "")
	if err != nil {
		t.Fatalf("UpdateStatus to ready: %v", err)
	}

	got, err = graphs.FindByID(ctx, rec.ID)
	if err != nil || got == nil {
		t.Fatalf("FindByID after ready: err=%v got=%v", err, got)
	}
	if got.Status != "ready" {
		t.Errorf("status: got %q, want %q", got.Status, "ready")
	}
	if got.ErrorMessage != "" {
		t.Errorf("error_message should be empty after ready update, got %q", got.ErrorMessage)
	}
}
