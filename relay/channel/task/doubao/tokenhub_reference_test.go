package doubao

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestTokenHubThreePublicImagesHaveReferenceRolesAndNativeModeration(t *testing.T) {
	c, _ := seedanceContext(`{"model":"seedance-2.5","prompt":"Two synthetic people beside a cabin","duration":10,"resolution":"720p","aspect_ratio":"9:16","generate_audio":true,"image_urls":["https://example.com/person-a.png","https://example.com/person-b.png","https://example.com/cabin.png"],"omni_reference_task_type":"reference","Moderation":{"Strategy":"Skip"}}`)
	a := &TaskAdaptor{baseURL: "https://ai-tokenhub.ai"}
	info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{IsModelMapped: true, UpstreamModelName: "dreamina-seedance-2-5-260628"}}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(raw, &payload))
	require.Len(t, payload.Content, 4)
	for i, url := range []string{"https://example.com/person-a.png", "https://example.com/person-b.png", "https://example.com/cabin.png"} {
		require.Equal(t, "reference_image", payload.Content[i].Role)
		require.Equal(t, url, payload.Content[i].ImageURL.URL)
	}
	require.Equal(t, "dreamina-seedance-2-5-260628", payload.Model)
	require.Equal(t, "9:16", payload.Ratio)
	require.EqualValues(t, 10, *payload.Duration)
	require.True(t, bool(*payload.GenerateAudio))
	require.Equal(t, "Skip", payload.Moderation.Strategy)
	require.Equal(t, "reference", *payload.OmniReferenceTaskType)
}

func TestTokenHubExplicitFirstFrameRoleIsPreserved(t *testing.T) {
	c, _ := seedanceContext(`{"model":"seedance-2.5","prompt":"Cabin","aspect_ratio":"adaptive","image_with_roles":[{"url":"https://example.com/first.png","role":"first_frame"}]}`)
	a := &TaskAdaptor{baseURL: "https://ai-tokenhub.ai"}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{}}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	raw, _ := io.ReadAll(body)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(raw, &payload))
	require.Equal(t, "first_frame", payload.Content[0].Role)
	require.Nil(t, payload.Moderation)
}
