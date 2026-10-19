package apimartvideo

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestChannel153SeedanceOptionalModerationDefault(t *testing.T) {
	for _, name := range []string{ModelSeedance25, ModelSeedance20, ModelSeedance20Fast, ModelSeedance20Mini} {
		for _, path := range []string{"/v1/videos", "/v1/video/generations", "/v1/videos/generations"} {
			for _, flag := range []any{nil, false, true} {
				fields := map[string]any{"model": name, "prompt": "scene", "duration": 4, "resolution": "480p"}
				if flag != nil {
					fields["nsfw_check"] = flag
				}
				c := generationContext(path, common.MapToJsonStr(fields))
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 153, UpstreamModelName: name}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
				a := &TaskAdaptor{}
				require.Nil(t, a.ValidateRequestAndSetAction(c, info))
				reader, err := a.BuildRequestBody(c, info)
				require.NoError(t, err)
				raw, err := io.ReadAll(reader)
				require.NoError(t, err)
				var sent map[string]any
				require.NoError(t, common.Unmarshal(raw, &sent))
				want := flag
				if want == nil {
					want = false
				}
				require.Equal(t, want, sent["nsfw_check"], "%s %s", name, path)
			}
		}
	}
}
