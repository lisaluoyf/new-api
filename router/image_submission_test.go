package router

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageSubmissionRecoveryRoutesRequireAuthentication(t *testing.T) {
	r := gin.New()
	r.Use(sessions.Sessions("test", cookie.NewStore([]byte("test-only-secret"))))
	SetRelayRouter(r)
	for _, path := range []string{"/v1/image-submissions/by-key", "/v1/image-submissions/by-key/response"} {
		w := httptest.NewRecorder()
		q := httptest.NewRequest("GET", path, nil)
		q.Header.Set("Idempotency-Key", "missing-auth-key")
		r.ServeHTTP(w, q)
		require.Equal(t, 401, w.Code, path)
	}
	// Keys never bypass ordinary authentication on any execution API.
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits/async", "/v1/midjourney/generations", "/v1/chat/completions", "/v1beta/models/future-image-model:generateContent", "/mj/submit/imagine"} {
		w := httptest.NewRecorder()
		q := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"future-image"}`))
		q.Header.Set("Idempotency-Key", "missing-auth-key")
		q.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, q)
		require.Equal(t, 401, w.Code, path)
	}
}
