package dto

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNormalizeGrokImage20(t *testing.T) {
	for _, tc := range []struct {
		body, variant string
		invalid       bool
	}{
		{`{"prompt":"test"}`, "1K low", false},
		{`{"prompt":"test","resolution":"2K","quality":"auto","image_urls":["https://example.test/ref.png"],"size":"1:1"}`, "2K medium", false},
		{`{"prompt":"test","quality":"low","image_urls":["https://example.test/ref.png"]}`, "1K low", false},
		{`{"prompt":"test","quality":"high"}`, "", true},
		{`{"prompt":"test","resolution":"4k"}`, "", true},
		{`{"prompt":"test","n":0}`, "", true},
		{`{"prompt":"test","n":11}`, "", true},
		{`{"prompt":"test","image_urls":[""]}`, "", true},
		{`{"prompt":"test","size":"1024x1024"}`, "", true},
		{`{"prompt":"test","size":"1:1","aspect_ratio":"16:9"}`, "", true},
	} {
		var req ImageRequest
		require.NoError(t, common.Unmarshal([]byte(tc.body), &req))
		req.Model = GrokImage20Model
		err := req.NormalizeGrokImage20()
		if tc.invalid {
			require.Error(t, err, tc.body)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.variant, req.EffectiveResolutionTier())
		body, err := common.Marshal(req)
		require.NoError(t, err)
		var wire map[string]interface{}
		require.NoError(t, common.Unmarshal(body, &wire))
		require.NotContains(t, wire, "size")
		require.NotEqual(t, "auto", wire["quality"])
		if len(req.ImageUrls) > 0 && req.Resolution == "2k" {
			require.Equal(t, "1:1", wire["aspect_ratio"])
		}
	}
}
