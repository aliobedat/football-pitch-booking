package handlers

// PART 6 consent-management tests: the opt-out endpoint records a user's
// withdrawal of consent. Identity comes from the JWT-set context key, so the
// tests inject it directly rather than minting tokens.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// fakeOptOutStore records SetOptOut calls and can be made to fail.
type fakeOptOutStore struct {
	mu    sync.Mutex
	calls []optOutCall
	err   error
}

type optOutCall struct {
	userID int
	optOut bool
}

func (f *fakeOptOutStore) SetOptOut(_ context.Context, userID int, optOut bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, optOutCall{userID, optOut})
	return nil
}

// newOptOutRouter wires the handler behind a tiny middleware that injects the
// given user id (0 means "no authenticated user").
func newOptOutRouter(store OptOutStore, userID int) *gin.Engine {
	h := NewNotificationHandler(store)
	r := gin.New()
	r.POST("/notifications/opt-out", func(c *gin.Context) {
		if userID != 0 {
			c.Set(middleware.ContextKeyUserID, userID)
		}
		h.OptOut(c)
	})
	return r
}

func TestOptOut_Success(t *testing.T) {
	store := &fakeOptOutStore{}
	r := newOptOutRouter(store, 42)

	req := httptest.NewRequest(http.MethodPost, "/notifications/opt-out", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(store.calls) != 1 || store.calls[0] != (optOutCall{42, true}) {
		t.Fatalf("SetOptOut calls = %+v, want one call (userID=42, optOut=true)", store.calls)
	}
}

func TestOptOut_Unauthenticated(t *testing.T) {
	store := &fakeOptOutStore{}
	r := newOptOutRouter(store, 0) // no user id set

	req := httptest.NewRequest(http.MethodPost, "/notifications/opt-out", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(store.calls) != 0 {
		t.Errorf("store called %d times, want 0 when unauthenticated", len(store.calls))
	}
}

func TestOptOut_UserNotFound(t *testing.T) {
	store := &fakeOptOutStore{err: repository.ErrUserNotFound}
	r := newOptOutRouter(store, 99)

	req := httptest.NewRequest(http.MethodPost, "/notifications/opt-out", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestOptOut_StoreError(t *testing.T) {
	store := &fakeOptOutStore{err: errAlwaysFail}
	r := newOptOutRouter(store, 7)

	req := httptest.NewRequest(http.MethodPost, "/notifications/opt-out", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
