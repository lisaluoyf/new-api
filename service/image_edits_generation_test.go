package service

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageEditsGenerationCapability(t *testing.T) {
	for _, id := range []int{59, 81} {
		ch := &model.Channel{Id: id}
		caps := &dto.GptImage2Capabilities{Version: 1, Enabled: true, Generations: &dto.GptImage2EndpointCapabilities{Enabled: true, MaxN: 1, MaxImageURLs: 2, OptionalFields: []string{"size", "resolution"}}}
		ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: caps})
		req := gptImage2CapabilityRequest{ModelName: "gpt-image-2", EditsPath: true, Multipart: true, HasUploadedImage: true, UploadedImageCount: 1, N: 1, Resolution: "2k", ResponseFormat: "b64_json"}
		require.Empty(t, gptImage2ChannelRejectionReason(ch, req), "channel %d", id)
		for _, tc := range []struct {
			name   string
			change func(*gptImage2CapabilityRequest)
			reason string
		}{
			{"mask", func(r *gptImage2CapabilityRequest) { r.HasUploadedMask = true }, "uploaded_mask_not_supported"},
			{"too many files", func(r *gptImage2CapabilityRequest) { r.UploadedImageCount = 3 }, "too_many_reference_images"},
			{"mixed references", func(r *gptImage2CapabilityRequest) { r.ImageURLCount = 2 }, "too_many_reference_images"},
			{"no image", func(r *gptImage2CapabilityRequest) { r.UploadedImageCount = 0; r.HasUploadedImage = false }, "reference_image_required"},
			{"output count", func(r *gptImage2CapabilityRequest) { r.N = 2 }, "image_count_out_of_range"},
		} {
			r := req
			tc.change(&r)
			require.Equal(t, tc.reason, gptImage2ChannelRejectionReason(ch, r), tc.name)
		}
		caps.Enabled = false
		ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: caps})
		require.Equal(t, "capabilities_disabled_or_invalid", gptImage2ChannelRejectionReason(ch, req))
	}
	require.False(t, GptImage2EditsViaGenerations(102, "gpt-image-2"))
	require.False(t, GptImage2EditsViaGenerations(81, "gpt-image-2.5-flare"))
}

func TestImageEditsGenerationCountsActualMultipartReferences(t *testing.T) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	require.NoError(t, w.WriteField("model", "gpt-image-2"))
	require.NoError(t, w.WriteField("image_urls", `["https://example.test/a.png"]`))
	for _, key := range []string{"image[]", "image[1]"} {
		f, err := w.CreateFormFile(key, "ref.png")
		require.NoError(t, err)
		_, err = f.Write([]byte("image"))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(b.Bytes()))
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	req := gptImage2CapabilityRequestFromContext(c, "gpt-image-2")
	require.Equal(t, 2, req.UploadedImageCount)
	require.Equal(t, 1, req.ImageURLCount)
	req = gptImage2CapabilityRequestFromJSON("gpt-image-2", []byte(`{"image_urls":["a"],"image":["b","c"],"images":["d"]}`))
	require.Equal(t, 4, req.ImageURLCount)
}

func TestChannel149UsesNativeImageEdits(t *testing.T) {
	require.False(t, GptImage2EditsViaGenerations(149, "gpt-image-2"))
	ch := &model.Channel{Id: 149}
	caps := &dto.GptImage2Capabilities{Version: 1, Enabled: true, Generations: &dto.GptImage2EndpointCapabilities{Enabled: true, MaxN: 1, MaxImageURLs: 16, OptionalFields: []string{"size", "resolution"}}, Edits: &dto.GptImage2EndpointCapabilities{Enabled: false}}
	ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: caps})
	req := gptImage2CapabilityRequest{ModelName: "gpt-image-2", EditsPath: true, Multipart: true, HasUploadedImage: true, UploadedImageCount: 1, N: 1, Size: "1:1", Resolution: "1k"}
	require.Empty(t, gptImage2ChannelRejectionReason(ch, req))
	req.ImageURLCount = 1
	require.Equal(t, "multipart_image_required", gptImage2ChannelRejectionReason(ch, req))
	req.ImageURLCount = 0
	req.HasUploadedMask = true
	require.Equal(t, "uploaded_mask_not_supported", gptImage2ChannelRejectionReason(ch, req))
	req.HasUploadedMask = false
	caps.Enabled = false
	ch.SetOtherSettings(dto.ChannelOtherSettings{GptImage2Capabilities: caps})
	require.Equal(t, "capabilities_disabled_or_invalid", gptImage2ChannelRejectionReason(ch, req))
}
