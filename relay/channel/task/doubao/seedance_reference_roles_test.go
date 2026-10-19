package doubao

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// Public image_urls must carry an explicit reference role to the upstream.
func TestSeedanceOrdinaryReferencesHaveUpstreamRoles(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		t.Run(name, func(t *testing.T) {
			c, _ := seedanceContext(`{"model":"` + name + `","prompt":"scene","duration":4,"resolution":"480p","aspect_ratio":"16:9","image_urls":["https://example.com/one.png","https://example.com/two.png"]}`)
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, OriginModelName: name,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 272, ChannelBaseUrl: "https://ark.cn-beijing.volces.com"}}
			a := &TaskAdaptor{}
			a.Init(info)
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			raw, err := io.ReadAll(body)
			require.NoError(t, err)
			var payload requestPayload
			require.NoError(t, common.Unmarshal(raw, &payload))
			images := 0
			for _, item := range payload.Content {
				if item.Type == "image_url" {
					images++
					require.Equal(t, "reference_image", item.Role)
				}
			}
			require.Equal(t, 2, images)

		})
	}
}

func TestSeedanceNativeImageRoleDefaultsAndExplicitFrames(t *testing.T) {
	for _, role := range []string{"", "reference_image", "first_frame", "last_frame"} {
		c, _ := seedanceContext(`{"model":"seedance-2.0","prompt":"scene","duration":4,"resolution":"480p","aspect_ratio":"adaptive","content":[{"type":"image_url","role":"` + role + `","image_url":{"url":"https://example.com/image.png"}}]}`)
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, OriginModelName: "seedance-2.0", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 292, ChannelBaseUrl: "https://api.xingchiyun.com"}}
		a := &TaskAdaptor{}
		a.Init(info)
		require.Nil(t, a.ValidateRequestAndSetAction(c, info))
		body, err := a.BuildRequestBody(c, info)
		require.NoError(t, err)
		raw, err := io.ReadAll(body)
		require.NoError(t, err)
		var payload requestPayload
		require.NoError(t, common.Unmarshal(raw, &payload))
		want := role
		if want == "" {
			want = "reference_image"
		}
		require.Equal(t, want, payload.Content[0].Role)
	}
}
