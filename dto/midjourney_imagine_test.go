package dto

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestImagineModelMappingAndNativeParameters(t *testing.T) {
	for _, model := range []string{"midjourney-v8.2", "midjourney-niji-7"} {
		body, _ := common.Marshal(map[string]any{"model": model, "prompt": "teapot --ar 16:9 --seed 0 --fast", "size": "3:4", "raw": false, "tile": false, "stylize": 0})
		req, err := ParseImagineRequest(body)
		require.NoError(t, err)
		require.Equal(t, "fast", req.Speed)
		require.Equal(t, "3:4", req.Payload["size"])
		require.Equal(t, float64(0), req.Payload["seed"])
		require.Equal(t, false, req.Payload["raw"])
		require.Equal(t, false, req.Payload["tile"])
		if model == "midjourney-v8.2" {
			require.Equal(t, "8.2", req.Payload["version"])
			require.NotContains(t, req.Payload, "niji")
		} else {
			require.Equal(t, "7", req.Payload["version"])
			require.Equal(t, true, req.Payload["niji"])
		}
	}
}

func TestImagineRejectsInvalidAndBypassedParameters(t *testing.T) {
	cases := []map[string]any{
		{"prompt": ""}, {"model": "midjourney-v8.2-fast"}, {"version": "7"}, {"niji": false}, {"speed": "slow"}, {"speed": nil}, {"speed": 4},
		{"size": "1024x1024"}, {"size": "0:1"}, {"seed": -1}, {"seed": 4294967296}, {"seed": 1.1}, {"stylize": 1001}, {"chaos": 101}, {"weird": 3001},
		{"repeat": 1}, {"repeat": 41}, {"quality": 2}, {"raw": "true"}, {"iw": 4}, {"iw": 1.5}, {"sw": 50}, {"cw": 10}, {"dw": 20},
		{"image_urls": []string{}}, {"image_urls": []string{"http://127.0.0.1/a.png"}}, {"image_urls": "https://example.com/a.png"},
		{"webhook": "https://example.com"}, {"unknown": true}, {"stop": 50}, {"extra": "--v 7"}, {"extra": "--repeat 99"}, {"prompt": "teapot --niji 7"},
		{"extra": "--unknown yes"}, {"metadata": []string{"x"}}, {"model": "midjourney-niji-7", "hd": true},
	}
	for _, invalid := range cases {
		body := map[string]any{"model": "midjourney-v8.2", "prompt": "teapot"}
		for k, v := range invalid {
			body[k] = v
		}
		encoded, _ := common.Marshal(body)
		_, err := ParseImagineRequest(encoded)
		require.Error(t, err, string(encoded))
	}
}

func TestImagineDefaultAndRepeat(t *testing.T) {
	req, err := ParseImagineRequest([]byte(`{"model":"midjourney-v8.2","prompt":"teapot","extra":"--repeat 4 --seed 42"}`))
	require.NoError(t, err)
	require.Equal(t, "relax", req.Speed)
	require.Equal(t, 4, req.Repeat)
	require.NotContains(t, req.Payload, "extra")
}
