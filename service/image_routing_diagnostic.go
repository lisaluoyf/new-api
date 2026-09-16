package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

const imageRoutingDiagnosticKey = "image_routing_diagnostic"
const maxImageRoutingEvents = 128

type imageRoutingDiagnostic struct {
	OriginalModel string                   `json:"original_model"`
	Request       map[string]interface{}   `json:"request"`
	RoutingRetry  int                      `json:"routing_retry"`
	ForcedChannel bool                     `json:"forced_channel"`
	Events        []map[string]interface{} `json:"events"`
	Truncated     bool                     `json:"truncated,omitempty"`
}

// Capture before model normalization and unsupported-parameter removal. Never
// retain the body, user identifier, reference URLs, credentials or prompt.
func InitImageRoutingDiagnostic(c *gin.Context, modelName string) {
	if c == nil || !IsGptImage2Family(modelName) {
		return
	}
	if _, exists := c.Get(imageRoutingDiagnosticKey); exists {
		return
	}
	req := gptImage2CapabilityRequestFromContext(c, modelName)
	_, forced := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
	fields := map[string]interface{}{
		"explicit_official": req.ExplicitOfficial, "async": req.AsyncPath,
		"edits": req.EditsPath, "multipart": req.Multipart, "n": req.N,
		"reference_image_count": req.ImageURLCount, "has_mask": req.HasMaskURL || req.HasUploadedMask,
		"has_uploaded_image": req.HasUploadedImage, "has_stream": req.HasStream,
		"has_partial_images": req.HasPartialImages, "has_user": req.User != "",
		"has_output_compression": req.OutputCompression,
		"effective_resolution":   gptImage2RequestResolutionTier(req),
	}
	for field, value := range map[string]string{
		"size": req.Size, "resolution": req.Resolution, "quality": req.Quality,
		"background": req.Background, "output_format": req.OutputFormat,
		"response_format": req.ResponseFormat, "moderation": req.Moderation,
		"input_fidelity": req.InputFidelity, "style": req.Style,
	} {
		if value != "" {
			fields[field] = safeImageRoutingValue(value)
		}
	}
	c.Set(imageRoutingDiagnosticKey, &imageRoutingDiagnostic{
		OriginalModel: modelName, Request: fields, RoutingRetry: RoutingRetryFromHeader(c),
		ForcedChannel: forced, Events: make([]map[string]interface{}, 0),
	})
}

func safeImageRoutingValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "1k", "2k", "4k", "1024x1024", "1536x1024", "1024x1536",
		"auto", "1:1", "3:2", "2:3", "16:9", "9:16",
		"low", "medium", "high", "standard", "hd", "opaque", "transparent",
		"png", "jpeg", "jpg", "webp", "url", "b64_json", "natural", "vivid":
		return value
	default:
		return "[other]"
	}
}

func imageRoutingTrace(c *gin.Context) *imageRoutingDiagnostic {
	if c == nil {
		return nil
	}
	value, _ := c.Get(imageRoutingDiagnosticKey)
	trace, _ := value.(*imageRoutingDiagnostic)
	return trace
}

func recordImageRoutingEvent(c *gin.Context, event map[string]interface{}) {
	trace := imageRoutingTrace(c)
	if trace == nil {
		return
	}
	if len(trace.Events) >= maxImageRoutingEvents {
		trace.Truncated = true
		return
	}
	trace.Events = append(trace.Events, event)
}

func RecordImageRoutingSelected(c *gin.Context, channelID int) {
	recordImageRoutingEvent(c, map[string]interface{}{"stage": "dispatch", "channel_id": channelID})
}

// Copy the trace so later retries cannot mutate an already-created log entry.
func AppendImageRoutingAdminInfo(c *gin.Context, adminInfo map[string]interface{}) {
	trace := imageRoutingTrace(c)
	if trace == nil || adminInfo == nil {
		return
	}
	raw, err := common.Marshal(trace)
	if err != nil {
		return
	}
	var snapshot map[string]interface{}
	if common.Unmarshal(raw, &snapshot) == nil {
		adminInfo["image_routing"] = snapshot
	}
}

// Also emit on distributor failures, which may never reach a consume log.
func LogImageRoutingDiagnostic(c *gin.Context) {
	if trace := imageRoutingTrace(c); trace != nil {
		if raw, err := common.Marshal(trace); err == nil {
			logger.LogInfo(c, "image-routing: "+string(raw))
		}
	}
}
