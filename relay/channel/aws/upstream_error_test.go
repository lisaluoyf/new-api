package aws

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAWSInvokeRetainsRawErrorDetailsBeforeSDKConversion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-Requestid", "opaque-sdk-request")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"__type":"ValidationException","message":"HiddenRelay expected image/jpeg at image[0] access-part secret-part","provider":"HiddenRelay","details":{"actual":"video/mp4","field":"image[0]","secret_key":"secret-part"}}`)
	}))
	defer server.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Set("channel_name", "HiddenRelay")
	c.Set("original_model", "public-model")
	c.Set("public_error_private_model", "private-deployment")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "access-part|secret-part|us-east-1", UpstreamModelName: "private-deployment"}}
	client := bedrockruntime.New(bedrockruntime.Options{Region: "us-east-1", BaseEndpoint: sdkaws.String(server.URL), Credentials: sdkaws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("access-part", "secret-part", "")), RetryMaxAttempts: 1, HTTPClient: service.SDKErrorHTTPClient(c, server.Client(), "access-part", "secret-part", "AWS", "BedrockRuntime")})
	a := &Adaptor{AwsClient: client, AwsReq: &bedrockruntime.InvokeModelInput{ModelId: sdkaws.String("private-deployment"), ContentType: sdkaws.String("application/json"), Body: []byte(`{}`)}}
	failure, _ := awsHandler(c, info, a)
	require.NotNil(t, failure)
	require.NotNil(t, failure.UpstreamError)
	require.Equal(t, 400, failure.UpstreamError.StatusCode)
	require.Equal(t, "opaque-sdk-request", failure.UpstreamError.RequestID)
	wire, err := common.Marshal(service.PublicRelayErrorView(c, failure).ToOpenAIError())
	require.NoError(t, err)
	for _, hidden := range []string{"access-part", "secret-part", "HiddenRelay", "BedrockRuntime", "Bedrock Runtime", "private-deployment"} {
		require.NotContains(t, string(wire), hidden)
	}
	require.Contains(t, string(wire), "video/mp4")
	require.Contains(t, string(wire), "image[0]")
	require.Equal(t, types.ErrorCodeAwsInvokeError, failure.GetErrorCode())
}
