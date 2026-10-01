package router

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceLibraryRoutesRequireAuthentication(t *testing.T) {
	router := gin.New()
	SetRelayRouter(router)
	SetVideoRouter(router)
	for _, route := range []struct{ method, path string }{
		{"POST", "/v1/uploads/images"}, {"POST", "/v1/seedance2/private-avatar/assets"}, {"POST", "/v1/seedance2/private-avatar"},
		{"GET", "/v1/seedance2/private-avatar/assets"}, {"GET", "/v1/seedance2/private-avatar/assets/asset_x"}, {"PATCH", "/v1/seedance2/private-avatar/assets/asset_x"}, {"DELETE", "/v1/seedance2/private-avatar/assets/asset_x"},
		{"POST", "/v1/seedance2/private-avatar/groups"}, {"GET", "/v1/seedance2/private-avatar/groups"}, {"GET", "/v1/seedance2/private-avatar/groups/group_x"}, {"PATCH", "/v1/seedance2/private-avatar/groups/group_x"}, {"DELETE", "/v1/seedance2/private-avatar/groups/group_x"},
		{"GET", "/v1/tasks/asset_task_x"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code, route.method+" "+route.path)
	}
}
