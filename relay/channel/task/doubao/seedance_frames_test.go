package doubao

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func frameDataURL(t *testing.T, width, height int) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestTencentSeedanceFrameRatios(t *testing.T) {
	img := frameDataURL(t, 720, 1280)
	for _, version := range []string{"1-5", "1-0"} {
		for _, field := range []string{"omitted", "ratio", "aspect_ratio", "metadata"} {
			for _, ratio := range []interface{}{nil, "", "16:9", "9:16", "adaptive"} {
				input := map[string]interface{}{"model": "seedance-2.5", "prompt": "scene", "image_urls": []string{img}, "generate_audio": false, "seed": 0}
				if field == "metadata" {
					input[field] = map[string]interface{}{"ratio": ratio}
				} else if field != "omitted" {
					input[field] = ratio
				}
				data, err := common.Marshal(input)
				require.NoError(t, err)
				c, _ := seedanceContext(string(data))
				// Mapping is not yet applied when task validation runs.
				upstream := "tencent-seedance-" + version + "-pro"
				c.Set("model_mapping", `{"seedance-2.5":"intermediate","intermediate":"`+upstream+`"}`)
				info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{}}
				a := &TaskAdaptor{baseURL: "https://yestoken.io"}
				require.Nil(t, a.ValidateRequestAndSetAction(c, info), "%s %s %v", version, field, ratio)
				info.IsModelMapped, info.UpstreamModelName = true, upstream
				body, err := a.BuildRequestBody(c, info)
				require.NoError(t, err)
				data, err = io.ReadAll(body)
				require.NoError(t, err)
				var payload requestPayload
				require.NoError(t, common.Unmarshal(data, &payload))
				want := "adaptive"
				if version == "1-0" && field != "omitted" && ratio != nil && ratio != "" {
					want = ratio.(string)
				}
				require.Equal(t, want, payload.Ratio)
				require.Equal(t, upstream, payload.Model)
				require.Len(t, payload.Content, 2)
				require.Equal(t, "first_frame", payload.Content[0].Role)
				require.NotNil(t, payload.GenerateAudio)
				require.False(t, bool(*payload.GenerateAudio))
				require.NotNil(t, payload.Seed)
				require.Zero(t, *payload.Seed)
				service.CleanupFileSources(c)
			}
		}
	}
}

func TestTencentSeedanceFramesValidation(t *testing.T) {
	valid := frameDataURL(t, 720, 1280)
	logo := frameDataURL(t, 600, 200)
	frame := func(url, role string) ContentItem {
		return ContentItem{Type: "image_url", ImageURL: &MediaURL{URL: url}, Role: role}
	}
	for _, tc := range []struct {
		name      string
		frames    []ContentItem
		wantError string
	}{
		{"first-last", []ContentItem{frame(valid, "first_frame"), frame(valid, "last_frame")}, ""},
		{"implicit-pair", []ContentItem{frame(valid, ""), frame(valid, "")}, ""},
		{"first-implicit-last", []ContentItem{frame(valid, "first_frame"), frame(valid, "")}, ""},
		{"logo", []ContentItem{frame(logo, "first_frame")}, "300-6000"},
		{"bad-last", []ContentItem{frame(valid, "first_frame"), frame(logo, "last_frame")}, "last_frame"},
		{"last-only", []ContentItem{frame(valid, "last_frame")}, "requires a first_frame"},
		{"duplicate", []ContentItem{frame(valid, "first_frame"), frame(valid, "first_frame")}, "duplicate"},
		{"too-many", []ContentItem{frame(valid, ""), frame(valid, ""), frame(valid, "")}, "at most two"},
		{"missing-url", []ContentItem{{Type: "image_url", Role: "first_frame"}}, "requires an image URL"},
		{"bad-data", []ContentItem{frame("data:image/png;base64,invalid", "first_frame")}, "cannot read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := common.Marshal(map[string]interface{}{"model": "seedance-2.5", "prompt": "scene", "content": tc.frames, "ratio": "16:9"})
			require.NoError(t, err)
			c, _ := seedanceContext(string(data))
			defer service.CleanupFileSources(c)
			c.Set("model_mapping", `{"seedance-2.5":"tencent-seedance-1-5-pro"}`)
			a := &TaskAdaptor{baseURL: "https://yestoken.io"}
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{}}
			taskErr := a.ValidateRequestAndSetAction(c, info)
			if tc.wantError != "" {
				require.NotNil(t, taskErr)
				require.Equal(t, 400, taskErr.StatusCode)
				require.Contains(t, taskErr.Message, tc.wantError)
				return
			}
			require.Nil(t, taskErr)
			req, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			payload, err := a.convertToRequestPayload(&req)
			require.NoError(t, err)
			require.Equal(t, "adaptive", payload.Ratio)
			require.Len(t, payload.Content, 3)
			require.Equal(t, "first_frame", payload.Content[0].Role)
			require.Equal(t, "last_frame", payload.Content[1].Role)
		})
	}
}

func TestSeedanceFrameDimensionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		w, h  int
		valid bool
	}{
		{300, 300, true}, {6000, 6000, true}, {300, 750, true}, {750, 300, true},
		{299, 300, false}, {6001, 6000, false}, {300, 751, false}, {751, 300, false}, {600, 200, false},
	} {
		err := validateSeedanceFrameSize(tc.w, tc.h)
		if tc.valid {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
}

func TestTencentSeedanceScopeAndText(t *testing.T) {
	for _, tc := range []struct{ name, base, mapping, content string }{
		{"other-provider", "https://other.example", `{"seedance-2.5":"tencent-seedance-1-5-pro"}`, `[{"type":"image_url","image_url":{"url":"https://example.com/not-fetched"}}]`},
		{"other-model", "https://yestoken.io", `{"seedance-2.5":"other-model"}`, `[{"type":"image_url","image_url":{"url":"https://example.com/not-fetched"}}]`},
		{"reference", "https://yestoken.io", `{"seedance-2.5":"tencent-seedance-1-5-pro"}`, `[{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/not-fetched"}}]`},
		{"text", "https://yestoken.io", `{"seedance-2.5":"tencent-seedance-1-5-pro"}`, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := seedanceContext(`{"model":"seedance-2.5","prompt":"scene","ratio":"9:16","content":` + tc.content + `}`)
			c.Set("model_mapping", tc.mapping)
			a := &TaskAdaptor{baseURL: tc.base}
			require.Nil(t, a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}))
			req, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			require.Equal(t, "9:16", req.Metadata["ratio"])
		})
	}
}
