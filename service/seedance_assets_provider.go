package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

const seedanceLibraryPath = "/v1/seedance2/private-avatar"

type SeedanceAPIError struct {
	Status  int
	Message string
	Code    string
}

func (e *SeedanceAPIError) Error() string { return e.Message }

func seedanceError(status int, message string, code ...string) error {
	value := "media_library_error"
	if len(code) > 0 {
		value = code[0]
	}
	return &SeedanceAPIError{Status: status, Message: message, Code: value}
}

func IsSeedance20Variant(name string) bool {
	return name == "seedance-2.0-fast" || name == "seedance-2.0-mini"
}

func IsSeedanceLibraryModel(name string) bool {
	return IsSeedance20Variant(name) || name == "seedance-2.5" || name == "seedance-2.0" || name == "doubao-seedance-2.0"
}

func SeedanceKeyFingerprint(key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(key))))
}

func SeedanceResourceKey(ch *model.Channel, fingerprint string) (string, int, error) {
	for index, key := range ch.GetKeys() {
		if SeedanceKeyFingerprint(key) == fingerprint {
			if status, ok := ch.ChannelInfo.MultiKeyStatusList[index]; ok && status != common.ChannelStatusEnabled {
				return "", 0, seedanceError(503, "Media library is temporarily unavailable")
			}
			return key, index, nil
		}
	}
	return "", 0, seedanceError(503, "Media library is temporarily unavailable")
}

func ValidateSeedanceModelAccess(c *gin.Context, name string) error {
	if !IsSeedanceLibraryModel(name) {
		return seedanceError(400, "model must be a supported Seedance model")
	}
	if IsFreeTrialGroup(common.GetContextKeyString(c, constant.ContextKeyTokenGroup)) {
		return seedanceError(403, "This key cannot access the media library")
	}
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		limits, _ := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		allowed, _ := limits.(map[string]bool)
		if _, ok := allowed[ratio_setting.FormatMatchingModelName(name)]; !ok {
			return seedanceError(403, "This key cannot access the requested model")
		}
	}
	return nil
}

func seedanceChannelAllowed(c *gin.Context, ch *model.Channel, name string) bool {
	if ch == nil || ch.Status != common.ChannelStatusEnabled {
		return false
	}
	if err := ValidateChannelClientPolicy(c, ch, name); err != nil {
		return false
	}
	group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	if group == "" {
		group = AutoCheapestGroup
	}
	if group == "auto" {
		for _, candidate := range GetUserAutoGroup(common.GetContextKeyString(c, constant.ContextKeyUserGroup)) {
			if model.IsChannelEnabledForGroupModel(candidate, name, ch.Id) {
				return true
			}
		}
		return false
	}
	return model.IsChannelEnabledForGroupModel(group, name, ch.Id)
}

func selectSeedanceLibrary(c *gin.Context, name string) (*model.SeedanceResource, error) {
	return selectSeedanceLibraryWithFilter(c, name, nil)
}

func selectSeedanceLibraryWithFilter(c *gin.Context, name string, capability func(*model.Channel) bool) (*model.SeedanceResource, error) {
	if err := ValidateSeedanceModelAccess(c, name); err != nil {
		return nil, err
	}
	filter := func(ch *model.Channel) bool {
		if ch == nil || !ProviderChannelAllowed(ProviderPolicyForModel(c, name), ch.Id) {
			return false
		}
		parsed, e := url.Parse(ch.GetBaseURL())
		if e != nil {
			return false
		}
		host := strings.ToLower(parsed.Hostname())
		if capability != nil && !capability(ch) {
			return false
		}
		if isBytePlusSeedanceChannel(ch) {
			credentials, err := loadBytePlusAssetCredentials(ch.Id)
			_, pinned := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
			autoEnabled := credentials.AutoRouteEnabled == nil || *credentials.AutoRouteEnabled
			return err == nil && (pinned || autoEnabled) && seedanceChannelAllowed(c, ch, name)
		}
		provider := host == "apimart.ai" || strings.HasSuffix(host, ".apimart.ai") || host == "apib.ai" || strings.HasSuffix(host, ".apib.ai") || ch.Id == constant.VideoFeeSeedanceChannelID
		return provider && seedanceChannelAllowed(c, ch, name)
	}
	var ch *model.Channel
	var err error
	if specified, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok {
		text, _ := specified.(string)
		id, _ := strconv.Atoi(text)
		ch, err = model.GetChannelById(id, true)
		if err == nil && ch != nil && isBytePlusSeedanceChannel(ch) && (capability == nil || capability(ch)) && seedanceChannelAllowed(c, ch, name) {
			if _, credentialErr := loadBytePlusAssetCredentials(ch.Id); credentialErr != nil {
				return nil, credentialErr
			}
		}
		if err == nil && !filter(ch) {
			ch = nil
		}
	} else {
		ch, err = SelectCheapestEnabledChannelInScope(name, nil, filter, ProviderPolicyForModel(c, name))
	}
	if err != nil || ch == nil {
		return nil, seedanceError(503, "Media library is temporarily unavailable")
	}
	key, _, keyErr := ch.GetNextEnabledKey()
	if keyErr != nil {
		return nil, seedanceError(503, "Media library is temporarily unavailable")
	}
	resource := &model.SeedanceResource{UserID: c.GetInt("id"), ChannelID: ch.Id, KeyFingerprint: SeedanceKeyFingerprint(key), Model: name}
	if isBytePlusSeedanceChannel(ch) {
		credentials, e := loadBytePlusAssetCredentials(ch.Id)
		if e != nil {
			return nil, e
		}
		resource.ProjectName, resource.CredentialFingerprint = credentials.ProjectName, seedanceControlFingerprint(credentials)
	}
	return resource, nil
}

func seedanceProviderRequest(ctx context.Context, resource *model.SeedanceResource, method, path string, payload any) (map[string]any, error) {
	ch, err := model.GetChannelById(resource.ChannelID, true)
	if err != nil || ch == nil || ch.Status != common.ChannelStatusEnabled {
		return nil, seedanceError(503, "Media library is temporarily unavailable")
	}
	key, _, err := SeedanceResourceKey(ch, resource.KeyFingerprint)
	if err != nil {
		return nil, err
	}
	if isBytePlusSeedanceChannel(ch) {
		return bytePlusLibraryRequest(ctx, resource, method, path, payload)
	}
	if ch.Id == constant.VideoFeeSeedanceChannelID {
		return videoFeeLibraryRequest(ctx, resource, ch, key, method, path, payload)
	}
	return seedanceProviderRawRequest(ctx, resource, ch, key, method, path, payload)
}

func seedanceProviderRawRequest(ctx context.Context, resource *model.SeedanceResource, ch *model.Channel, key, method, path string, payload any) (map[string]any, error) {
	var body io.Reader
	contentType := "application/json"
	if upload, ok := payload.(seedanceMediaUpload); ok {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename="%s"`, upload.filename)}, "Content-Type": {http.DetectContentType(upload.data)}})
		if err != nil {
			return nil, err
		}
		if _, err = part.Write(upload.data); err != nil {
			return nil, err
		}
		if err = writer.Close(); err != nil {
			return nil, err
		}
		contentType = writer.FormDataContentType()
		body = &buffer
	} else if payload != nil {
		encoded, e := common.Marshal(payload)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(ch.GetBaseURL(), "/")+path, body)
	if err != nil {
		return nil, seedanceError(502, "Unable to contact media library")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", contentType)
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, seedanceError(502, "Media library request failed; retry queries before submitting again")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	var envelope map[string]any
	if err != nil || len(raw) > 4<<20 || common.Unmarshal(raw, &envelope) != nil {
		return nil, seedanceError(502, "Invalid media library response")
	}
	metadata, _ := envelope["ResponseMetadata"].(map[string]any)
	_, providerError := metadata["Error"]
	_, topError := envelope["error"]
	if response.StatusCode < 200 || response.StatusCode >= 300 || providerError || topError {
		status := response.StatusCode
		if status < 400 || status == 401 || status == 403 {
			status = 502
		}
		message := taskErrorMessage(envelope, 0)
		if providerError {
			detail, _ := metadata["Error"].(map[string]any)
			message, _ = detail["Message"].(string)
		}
		if message == "" {
			message = "Media library request failed"
		}
		sensitive := []string{key, ch.Key, ch.Name, resource.UpstreamID}
		if fields, ok := payload.(map[string]any); ok {
			if groupID, ok := fields["group_id"].(string); ok {
				sensitive = append(sensitive, groupID)
			}
		}
		message = sanitizeTaskFailure(message, sensitive...)
		return nil, seedanceError(status, message)
	}
	return envelope, nil
}

func seedanceResult(envelope map[string]any) map[string]any {
	if result, ok := envelope["Result"].(map[string]any); ok {
		return result
	}
	if data, ok := envelope["data"].(map[string]any); ok {
		return data
	}
	return envelope
}

func seedanceString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := value[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func seedanceID(kind string) string {
	return kind + "_" + strings.TrimPrefix(model.GenerateTaskID(), "task_")
}
