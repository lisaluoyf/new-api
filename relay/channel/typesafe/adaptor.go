package typesafe

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Shared transport/header machinery only; Jev uses its native request/response.
type Adaptor struct{ openai.Adaptor }

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayFormat != types.RelayFormatTypeSafe {
		return "", fmt.Errorf("TypeSafe requires POST /v1/systemone")
	}
	return strings.TrimRight(info.ChannelBaseUrl, "/") + "/v1/systemone", nil
}
func (a *Adaptor) SetupRequestHeader(c *gin.Context, h *http.Header, info *relaycommon.RelayInfo) error {
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}
func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, body)
}
func (a *Adaptor) GetModelList() []string { return []string{"jev-latest", "jev-1.13.0", "jev-preview"} }
func (a *Adaptor) GetChannelName() string { return "typesafe" }

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil || len(data) > 16<<20 {
		return nil, badResponse("unable to read TypeSafe response")
	}
	usage, err := ParseResponse(data, info.Request)
	if err != nil {
		return nil, badResponse(err.Error())
	}
	info.SetFirstResponseTime()
	c.Data(http.StatusOK, "application/json", data)
	return usage, nil
}

func badResponse(message string) *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf("%s", message), types.ErrorCodeBadResponse, http.StatusBadGateway)
}

func ParseResponse(data []byte, request dto.Request) (*dto.Usage, error) {
	return dto.ParseTypeSafeResponse(data, request)
}
