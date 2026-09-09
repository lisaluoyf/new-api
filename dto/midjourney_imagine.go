package dto

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

func IsMidjourneyImagineModel(name string) bool {
	return name == "midjourney-v8.2" || name == "midjourney-niji-7"
}

type ImagineRequest struct {
	Model   string
	Speed   string
	Repeat  int
	Payload map[string]any
}

// Normalize structured and native MJ parameters through the same whitelist.
func ParseImagineRequest(body []byte) (*ImagineRequest, error) {
	var raw map[string]json.RawMessage
	if err := common.Unmarshal(body, &raw); err != nil || raw == nil {
		return nil, fmt.Errorf("request must be a JSON object")
	}
	var name string
	if common.Unmarshal(raw["model"], &name) != nil || !IsMidjourneyImagineModel(name) {
		return nil, fmt.Errorf("model must be midjourney-v8.2 or midjourney-niji-7")
	}
	if _, ok := raw["version"]; ok {
		return nil, fmt.Errorf("version is determined by model")
	}
	if _, ok := raw["niji"]; ok {
		return nil, fmt.Errorf("niji is determined by model")
	}
	var prompt string
	if common.Unmarshal(raw["prompt"], &prompt) != nil || strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt must be a non-empty string")
	}
	payload := map[string]any{}
	cleanPrompt, err := parseImagineFlags(prompt, payload, false)
	if err != nil {
		return nil, err
	}
	if extra, ok := raw["extra"]; ok {
		var s string
		if common.Unmarshal(extra, &s) != nil || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("extra must be a non-empty string of supported MJ parameters")
		}
		if _, err := parseImagineFlags(s, payload, true); err != nil {
			return nil, err
		}
	}
	for field, value := range raw {
		if field == "model" || field == "prompt" || field == "extra" {
			continue
		}
		var v any
		if err := common.Unmarshal(value, &v); err != nil || v == nil {
			return nil, fmt.Errorf("%s must not be null", field)
		}
		payload[field] = v
	}
	payload["prompt"] = strings.TrimSpace(cleanPrompt)
	if payload["prompt"] == "" {
		return nil, fmt.Errorf("prompt must contain a description")
	}
	if _, ok := payload["speed"]; !ok {
		payload["speed"] = "relax"
	}
	if _, ok := payload["size"]; !ok {
		payload["size"] = "1:1"
	}
	for field, v := range payload {
		if err := validateImagineField(name, field, v); err != nil {
			return nil, err
		}
	}
	for weight, ref := range map[string]string{"iw": "image_urls", "cw": "cref", "sw": "sref", "dw": "dref"} {
		if _, exists := payload[weight]; exists {
			if _, ok := payload[ref]; !ok {
				return nil, fmt.Errorf("%s requires %s", weight, ref)
			}
		}
	}
	if payload["draft"] == true && payload["hd"] == true {
		return nil, fmt.Errorf("draft and hd cannot both be enabled")
	}
	if payload["style"] == "raw" && payload["raw"] == false {
		return nil, fmt.Errorf("style=raw conflicts with raw=false")
	}
	// Modern versions accept --raw; APIMart's --style raw fails for these versions.
	if payload["style"] == "raw" {
		payload["raw"] = true
		delete(payload, "style")
	}
	if ref, ok := payload["dref"].(string); ok {
		payload["dref"] = []any{ref}
	}
	payload["version"] = "8.2"
	if name == "midjourney-niji-7" {
		payload["version"] = "7"
		payload["niji"] = true
	}
	repeat := 1
	if v, ok := payload["repeat"].(float64); ok {
		repeat = int(v)
	}
	return &ImagineRequest{Model: name, Speed: payload["speed"].(string), Repeat: repeat, Payload: payload}, nil
}

func validateImagineField(model, field string, v any) error {
	bad := func(reason string) error { return fmt.Errorf("%s %s", field, reason) }
	if field == "cref" || field == "cw" {
		return bad("is not supported by Midjourney 8.2 or Niji 7")
	}
	if field == "draft" && v == true {
		return bad("is not supported by these models on the current image service")
	}
	if model == "midjourney-niji-7" && (field == "quality" || field == "tile" && v == true) {
		return bad("is not supported by Niji 7")
	}
	ranges := map[string][2]float64{"seed": {0, 4294967295}, "stylize": {0, 1000}, "chaos": {0, 100}, "weird": {0, 3000}, "iw": {0, 3}, "cw": {0, 100}, "sw": {0, 1000}, "dw": {0, 100}, "repeat": {2, 40}}
	if bounds, ok := ranges[field]; ok {
		n, ok := v.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < bounds[0] || n > bounds[1] {
			return bad(fmt.Sprintf("must be a number between %g and %g", bounds[0], bounds[1]))
		}
		if field != "iw" && field != "dw" && n != math.Trunc(n) {
			return bad("must be an integer")
		}
		return nil
	}
	switch field {
	case "prompt", "negative_prompt":
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" || len(s) > 16000 {
			return bad("must be a non-empty string of at most 16000 bytes")
		}
	case "speed":
		if v != "relax" && v != "fast" && v != "turbo" {
			return bad("must be relax, fast, or turbo")
		}
	case "quality":
		if v != "0.25" && v != "0.5" && v != "1" && v != "2" {
			return bad(`must be "0.25", "0.5", "1", or "2"`)
		}
	case "style":
		if v != "raw" {
			return bad("must be raw for these models")
		}
	case "size":
		s, ok := v.(string)
		if !ok {
			return bad("must be an aspect ratio such as 16:9")
		}
		parts := strings.Split(s, ":")
		if len(parts) != 2 {
			return bad("must be an aspect ratio such as 16:9")
		}
		for _, part := range parts {
			n, err := strconv.ParseUint(part, 10, 32)
			if err != nil || n == 0 {
				return bad("must contain two positive integers")
			}
		}
	case "tile", "raw", "draft", "hd", "nsfw_check":
		b, ok := v.(bool)
		if !ok {
			return bad("must be a boolean")
		}
		if field == "hd" && b && model == "midjourney-niji-7" {
			return bad("is not supported by Niji 7")
		}
	case "dref":
		if items, ok := v.([]any); ok {
			if len(items) == 0 || len(items) > 20 {
				return bad("must contain 1 to 20 image URLs")
			}
			for _, item := range items {
				s, ok := item.(string)
				if !ok || !validImagineURL(s) {
					return bad("must contain public HTTP(S) image URLs")
				}
			}
			return nil
		}
		s, ok := v.(string)
		if !ok || !validImagineURL(s) {
			return bad("must be an image URL or array of image URLs")
		}
	case "cref", "sref":
		s, ok := v.(string)
		if !ok || !validImagineURL(s) {
			return bad("must be a public HTTP(S) image URL")
		}
	case "image_urls":
		items, ok := v.([]any)
		if !ok || len(items) == 0 || len(items) > 20 {
			return bad("must contain 1 to 20 image URLs")
		}
		for _, item := range items {
			s, ok := item.(string)
			if !ok || (!validImagineURL(s) && !validImagineBase64(s)) {
				return bad("must contain public HTTP(S) image URLs")
			}
		}
	case "metadata":
		if _, ok := v.(map[string]any); !ok {
			return bad("must be an object")
		}
	case "stop":
		return bad("is not supported by Midjourney 8.2 or Niji 7")
	default:
		return bad("is not a supported Imagine parameter")
	}
	return nil
}

func validImagineBase64(s string) bool {
	if len(s) > 12<<20 {
		return false
	}
	if strings.HasPrefix(s, "data:image/") {
		parts := strings.SplitN(s, ",", 2)
		if len(parts) != 2 || !strings.HasSuffix(parts[0], ";base64") {
			return false
		}
		s = parts[1]
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(decoded) < 12 {
		return false
	}
	return strings.HasPrefix(string(decoded), "\x89PNG\r\n\x1a\n") || strings.HasPrefix(string(decoded), "\xff\xd8\xff") || strings.HasPrefix(string(decoded), "GIF8") || string(decoded[:4]) == "RIFF" && string(decoded[8:12]) == "WEBP"
}

func validImagineURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast()) {
		return false
	}
	return true
}

func parseImagineFlags(s string, fields map[string]any, onlyFlags bool) (string, error) {
	parts := strings.Split(s, "--")
	if onlyFlags && strings.TrimSpace(parts[0]) != "" {
		return "", fmt.Errorf("extra accepts supported MJ parameters only")
	}
	aliases := map[string]string{"ar": "size", "aspect": "size", "q": "quality", "s": "stylize", "c": "chaos", "w": "weird", "no": "negative_prompt", "r": "repeat"}
	for _, part := range parts[1:] {
		words := strings.Fields(part)
		if len(words) == 0 {
			return "", fmt.Errorf("empty MJ parameter")
		}
		flag := words[0]
		if flag == "v" || flag == "version" || flag == "niji" {
			return "", fmt.Errorf("model version cannot be overridden in prompt or extra")
		}
		if flag == "relax" || flag == "fast" || flag == "turbo" {
			if len(words) != 1 {
				return "", fmt.Errorf("invalid speed parameter")
			}
			fields["speed"] = flag
			continue
		}
		if alias, ok := aliases[flag]; ok {
			flag = alias
		}
		value := strings.Join(words[1:], " ")
		switch flag {
		case "raw", "tile", "draft", "hd":
			if value != "" {
				return "", fmt.Errorf("--%s takes no value", flag)
			}
			fields[flag] = true
		case "seed", "stylize", "chaos", "weird", "iw", "cw", "sw", "dw", "repeat", "stop":
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return "", fmt.Errorf("--%s requires a number", flag)
			}
			fields[flag] = n
		case "size", "quality", "style", "negative_prompt", "cref", "sref", "dref":
			fields[flag] = value
		default:
			return "", fmt.Errorf("unsupported MJ parameter --%s", words[0])
		}
	}
	return parts[0], nil
}
