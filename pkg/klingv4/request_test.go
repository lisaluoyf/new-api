package klingv4

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestKlingV4ModesAndMeasuredBilling(t *testing.T) {
	for _, tc := range []struct {
		name, body, mode      string
		duration, input, bill int
		omitted               bool
	}{
		{"text", `{"model":"kling-v4","prompt":"paper boat"}`, "text", 5, 0, 5, false},
		{"frames", `{"prompt":"Animate the image","image_urls":["https://example.com/a.png","https://example.com/b.png"]}`, "image", 5, 0, 5, false},
		{"roles", `{"prompt":"Animate the frames","image_with_roles":[{"url":"https://example.com/a.png","role":"first_frame"},{"url":"https://example.com/b.png","role":"middle_frame"}]}`, "image", 5, 0, 5, false},
		{"reference sums before ceil", `{"prompt":"test","video_urls":["https://example.com/a.mp4","https://example.com/b.mp4"],"duration":5}`, "reference", 5, 7, 12, false},
		{"edit inherited duration", `{"prompt":"test","omni_reference_task_type":"edit","video_url":"https://example.com/a.mp4"}`, "edit", 4, 4, 8, true},
		{"edit explicit output", `{"prompt":"test","omni_reference_task_type":"edit","video_url":"https://example.com/a.mp4","duration":5}`, "edit", 5, 4, 10, false},
		{"extend excludes original", `{"prompt":"test","omni_reference_task_type":"extend","video_url":"https://example.com/a.mp4","duration":3}`, "extend", 3, 0, 3, false},
		{"audio reference", `{"prompt":"test","audio_urls":["https://example.com/a.mp3"]}`, "reference", 5, 0, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &f))
			s, e := Normalize(context.Background(), f, func(context.Context, string) (float64, error) { return 3.04, nil })
			require.NoError(t, e)
			require.Equal(t, tc.mode, s.Mode)
			require.Equal(t, tc.duration, s.Duration)
			require.Equal(t, tc.input, s.InputSeconds)
			require.Equal(t, tc.bill, s.BillableSeconds)
			if tc.omitted {
				require.NotContains(t, f, "duration")
			}
			if tc.mode == "extend" || tc.mode == "image" || tc.mode == "edit" {
				require.NotContains(t, f, "aspect_ratio")
			}
		})
	}
}
func TestKlingV4RejectInvalidBeforeSubmission(t *testing.T) {
	for _, extra := range []string{`,"duration":0`, `,"duration":"3"`, `,"duration":31`, `,"duration":3.5`, `,"resolution":"4k"`, `,"mode":"std"`, `,"audio":false`, `,"audio":"true"`, `,"watermark":true`, `,"negative_prompt":"none"`, `,"multi_shot":true`, `,"image_urls":["file:///etc/passwd"]`, `,"video_url":"https://example.com/a.mp4"`, `,"aspect_ratio":"auto"`, `,"audio_setting":"origin"`, `,"image_with_roles":[{"url":"https://example.com/a.png","role":"last_frame"}]`, `,"duration":5,"seconds":3`, `,"omni_reference_task_type":"extend","video_url":"https://example.com/a.mp4","aspect_ratio":"16:9"`, `,"omni_reference_task_type":"edit"`, `,"video_urls":["https://example.com/a.mp4"],"audio_setting":"off"`} {
		t.Run(extra, func(t *testing.T) {
			var f map[string]any
			require.NoError(t, common.UnmarshalJsonStr(`{"model":"kling-v4","prompt":"test"`+extra+`}`, &f))
			_, e := Normalize(context.Background(), f, nil)
			require.Error(t, e)
		})
	}
}
func TestKlingV4ProbeFailureAndInputCap(t *testing.T) {
	for _, value := range []float64{2.99, 0} {
		var f map[string]any
		require.NoError(t, common.UnmarshalJsonStr(`{"prompt":"test","omni_reference_task_type":"edit","video_url":"https://example.com/a.mp4"}`, &f))
		_, e := Normalize(context.Background(), f, func(context.Context, string) (float64, error) { return value, nil })
		require.Error(t, e)
	}
	f := map[string]any{"prompt": "test", "omni_reference_task_type": "edit", "video_url": "https://example.com/a.mp4"}
	s, e := Normalize(context.Background(), f, func(context.Context, string) (float64, error) { return 45.1, nil })
	require.NoError(t, e)
	require.Equal(t, 60, s.BillableSeconds)
	_, e = Normalize(context.Background(), f, func(context.Context, string) (float64, error) { return 0, fmt.Errorf("network") })
	require.Error(t, e)
}
