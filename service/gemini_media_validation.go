package service

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

type MarkdownDataImagePart struct {
	Text string
	MIME string
	Data string
}

// ValidateInlineBase64 validates bytes without allocating another decoded image.
// Do not include image contents or surrounding user text in validation errors.
func ValidateInlineBase64(data string) error {
	if data == "" {
		return fmt.Errorf("image Base64 data is empty")
	}
	if n, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))); err != nil || n == 0 {
		return fmt.Errorf("image Base64 data is invalid or truncated")
	}
	return nil
}

// ParseMarkdownDataImages preserves all surrounding text and rejects incomplete
// image destinations instead of treating a later prose ')' as the image end.
func ParseMarkdownDataImages(text string) ([]MarkdownDataImagePart, error) {
	var parts []MarkdownDataImagePart
	remaining, search := text, 0
	for search < len(remaining) {
		i := strings.Index(remaining[search:], "![")
		if i < 0 {
			break
		}
		i += search
		labelEnd := strings.IndexAny(remaining[i+2:], "[]\r\n")
		if labelEnd < 0 {
			break
		}
		labelEnd += i + 2
		if remaining[labelEnd] != ']' || !strings.HasPrefix(remaining[labelEnd:], "](") {
			search = labelEnd + 1
			if remaining[labelEnd] == '[' && labelEnd > 0 && remaining[labelEnd-1] == '!' {
				search = labelEnd - 1
			}
			continue
		}
		dataStart := labelEnd + 2
		if !strings.HasPrefix(remaining[dataStart:], "data:") {
			search = dataStart
			continue
		}
		end := strings.IndexByte(remaining[dataStart:], ')')
		if end < 0 {
			return nil, fmt.Errorf("markdown image data URI is missing its closing parenthesis")
		}
		end += dataStart
		uri := remaining[dataStart:end]
		if strings.ContainsAny(uri, "\r\n") {
			return nil, fmt.Errorf("markdown image data URI must close before the end of the line")
		}
		header, data, ok := strings.Cut(uri, ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return nil, fmt.Errorf("markdown image requires a Base64 data URI")
		}
		if err := ValidateInlineBase64(data); err != nil {
			return nil, err
		}
		mime := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		if mime == "" {
			return nil, fmt.Errorf("markdown image MIME type is missing")
		}
		if i > 0 {
			parts = append(parts, MarkdownDataImagePart{Text: remaining[:i]})
		}
		parts = append(parts, MarkdownDataImagePart{MIME: mime, Data: data})
		remaining, search = remaining[end+1:], 0
	}
	if remaining != "" {
		parts = append(parts, MarkdownDataImagePart{Text: remaining})
	}
	return parts, nil
}

// Run before any Gemini-compatible upstream, including OpenAI passthrough.
func ValidateGeminiRequestMedia(request *dto.GeneralOpenAIRequest) error {
	for mi, message := range request.Messages {
		for pi, part := range message.ParseContent() {
			if part.Type == dto.ContentTypeText {
				if _, err := ParseMarkdownDataImages(part.Text); err != nil {
					return fmt.Errorf("messages[%d].content[%d]: %w", mi, pi, err)
				}
			} else if part.Type == dto.ContentTypeImageURL {
				image := part.GetImageMedia()
				if image == nil || !strings.HasPrefix(image.Url, "data:") {
					continue
				}
				header, data, ok := strings.Cut(image.Url, ",")
				if !ok || !strings.HasSuffix(header, ";base64") {
					return fmt.Errorf("messages[%d].content[%d]: image requires a Base64 data URI", mi, pi)
				}
				if err := ValidateInlineBase64(data); err != nil {
					return fmt.Errorf("messages[%d].content[%d]: %w", mi, pi, err)
				}
			}
		}
	}
	return nil
}

func ValidateGeminiNativeMedia(request *dto.GeminiChatRequest) error {
	for ci, content := range request.Contents {
		for pi, part := range content.Parts {
			if part.InlineData != nil {
				if err := ValidateInlineBase64(part.InlineData.Data); err != nil {
					return fmt.Errorf("contents[%d].parts[%d].inline_data.data: %w", ci, pi, err)
				}
			}
		}
	}
	return nil
}
