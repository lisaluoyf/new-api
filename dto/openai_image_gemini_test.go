package dto

import (
	"encoding/json"
	"testing"
)

func TestGeminiFlashImageResolutionPriceRatio(t *testing.T) {
	t.Parallel()
	cases := map[string]float64{
		"":     1.0,
		"0.5K": 1.0,
		"1k":   1.0,
		"2K":   4.0 / 3.0,
		"4k":   2.0,
	}
	for res, want := range cases {
		if got := GeminiFlashImageResolutionPriceRatio(res); got != want {
			t.Fatalf("GeminiFlashImageResolutionPriceRatio(%q) = %v, want %v", res, got, want)
		}
	}
}

func TestImageRequestEffectiveResolutionTier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  ImageRequest
		want string
	}{
		{name: "default", req: ImageRequest{}, want: "1K"},
		{name: "explicit", req: ImageRequest{Resolution: "2k", Size: "1024x1024"}, want: "2K"},
		{name: "extra", req: ImageRequest{Extra: map[string]json.RawMessage{"resolution": json.RawMessage(`"4k"`)}}, want: "4K"},
		{name: "aspect ratio", req: ImageRequest{Size: "16:9"}, want: "1K"},
		{name: "two k pixels", req: ImageRequest{Size: "2048x1152"}, want: "2K"},
		{name: "four k square", req: ImageRequest{Size: "2880x2880"}, want: "4K"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := test.req
			if got := req.EffectiveResolutionTier(); got != test.want {
				t.Fatalf("EffectiveResolutionTier() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestImageRequestGetTokenCountMeta_geminiFlashImage(t *testing.T) {
	t.Parallel()
	req := &ImageRequest{
		Model:      "gemini-3.1-flash-image-preview",
		Prompt:     "test",
		Resolution: "2K",
	}
	meta := req.GetTokenCountMeta()
	if meta.ImagePriceRatio != 4.0/3.0 {
		t.Fatalf("expected 2K ratio 4/3, got %v", meta.ImagePriceRatio)
	}
}
