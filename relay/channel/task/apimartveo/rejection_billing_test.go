package apimartveo

import (
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestVeoExplicitRejectionIsConfirmedUncharged(t *testing.T) {
	a, c, info := fixture(t, Fast, "veo3.1-fast", "")
	_, _, err := a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"success":false,"message":"unsupported option"}`))}, info)
	require.NotNil(t, err)
	require.Equal(t, service.UpstreamChargeConfirmedNot, service.ClassifyUpstreamChargeConfidence(service.TaskErrorToNewAPIError(err)))
}
