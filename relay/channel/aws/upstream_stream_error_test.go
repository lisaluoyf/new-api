package aws

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSDKEventStreamExceptionIsNotReportedAsSuccessfulCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Header().Set("X-Amzn-Bedrock-Content-Type", "application/json")
		w.Header().Set("X-Amzn-Requestid", "opaque-stream-sdk-request")
		err := eventstream.NewEncoder().Encode(w, eventstream.Message{
			Headers: eventstream.Headers{{Name: ":message-type", Value: eventstream.StringValue("exception")}, {Name: ":exception-type", Value: eventstream.StringValue("modelStreamErrorException")}, {Name: ":content-type", Value: eventstream.StringValue("application/json")}},
			Payload: []byte(`{"message":"HiddenRelay on Bedrock Runtime stream failed","originalMessage":"HiddenRelay expected image/jpeg at image[0]; secret-part","originalStatusCode":422}`),
		})
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Set("channel_name", "HiddenRelay")
	c.Set("original_model", "public-model")
	info := &relaycommon.RelayInfo{StartTime: time.Now(), IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "access-part|secret-part|us-east-1", UpstreamModelName: "public-model"}}
	client := bedrockruntime.New(bedrockruntime.Options{Region: "us-east-1", BaseEndpoint: sdkaws.String(server.URL), Credentials: sdkaws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("access-part", "secret-part", "")), RetryMaxAttempts: 1, HTTPClient: service.SDKErrorHTTPClient(c, server.Client(), "access-part", "secret-part", "AWS", "BedrockRuntime")})
	a := &Adaptor{AwsClient: client, AwsReq: &bedrockruntime.InvokeModelWithResponseStreamInput{ModelId: sdkaws.String("public-model"), ContentType: sdkaws.String("application/json"), Body: []byte(`{}`)}}
	failure, usage := awsStreamHandler(c, info, a)
	require.NotNil(t, failure)
	require.Nil(t, usage)
	require.NotNil(t, failure.UpstreamError)
	require.Equal(t, 200, failure.UpstreamError.StatusCode, "initial HTTP handshake succeeded; failure was in the event stream")
	require.Equal(t, "opaque-stream-sdk-request", failure.UpstreamError.RequestID)
	wire, _ := common.Marshal(service.PublicRelayErrorView(c, failure).ToOpenAIError())
	require.Contains(t, string(wire), "expected image/jpeg")
	require.Contains(t, string(wire), "image[0]")
	require.Contains(t, string(wire), `"original_status_code":422`)
	require.NotContains(t, string(wire), "HiddenRelay")
	require.NotContains(t, string(wire), "Bedrock Runtime")
	require.NotContains(t, string(wire), "secret-part")
	require.NotContains(t, recorder.Body.String(), "[DONE]")
}
