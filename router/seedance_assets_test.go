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

func TestSeedanceLibraryRoutesRequireAuthentication(t *testing.T) {
	router := gin.New()
	router.Use(sessions.Sessions("test", cookie.NewStore([]byte("test-only-session-secret"))))
	SetRelayRouter(router)
	SetVideoRouter(router)
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/seedance2/private-avatar/capabilities?model=seedance-2.5"}, {"POST", "/v1/seedance2/private-avatar/uploads"}, {"DELETE", "/v1/seedance2/private-avatar/uploads/1_x"}, {"GET", "/v1/seedance2/private-avatar/video-requests/video_request_x"},
		{"POST", "/v1/seedance2/private-avatar/verifications"}, {"GET", "/v1/seedance2/private-avatar/verifications/verification_x"},
		{"POST", "/v1/uploads/images"}, {"POST", "/v1/seedance2/private-avatar/assets"}, {"POST", "/v1/seedance2/private-avatar"},
		{"GET", "/v1/seedance2/private-avatar/assets"}, {"GET", "/v1/seedance2/private-avatar/assets/asset_x"}, {"PATCH", "/v1/seedance2/private-avatar/assets/asset_x"}, {"DELETE", "/v1/seedance2/private-avatar/assets/asset_x"},
		{"POST", "/v1/seedance2/private-avatar/groups"}, {"GET", "/v1/seedance2/private-avatar/groups"}, {"GET", "/v1/seedance2/private-avatar/groups/group_x"}, {"PATCH", "/v1/seedance2/private-avatar/groups/group_x"}, {"DELETE", "/v1/seedance2/private-avatar/groups/group_x"},
		{"GET", "/v1/tasks/asset_task_x"}, {"GET", "/v1/videos/task_x/last-frame"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code, route.method+" "+route.path)
	}
}
