// Package klingv4 defines the public Kling V4 contract and measured billing units.
package klingv4

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

const Model = "kling-v4"

type Spec struct {
	Mode            string
	Resolution      string
	Duration        int
	InputSeconds    int
	BillableSeconds int
	Variant         string
	Images          []string
}

type Probe func(context.Context, string) (float64, error)

// Normalize operates on a private decoded request. Defaults depend on the mode;
// in particular edit must preserve omitted duration and extend omitted aspect.
func Normalize(ctx context.Context, f map[string]any, probe Probe) (Spec, error) {
	s := Spec{Resolution: "720p", Duration: 5}
	allowed := strings.Fields("model prompt duration seconds resolution size aspect_ratio ratio image_urls image_with_roles first_frame_image last_frame_image video_urls video_url audio_urls omni_reference_task_type audio_setting audio watermark webhook")
	for k := range f {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
				break
			}
		}
		if !found {
			return s, fmt.Errorf("unsupported Kling V4 field %s", k)
		}
	}
	for _, k := range []string{"audio", "watermark"} {
		if v, ok := f[k]; ok {
			b, valid := v.(bool)
			if !valid || k == "audio" && !b || k == "watermark" && b {
				return s, fmt.Errorf("Kling V4 requires audio and does not support watermark")
			}
		}
	}
	text := func(k string) (string, error) {
		v, ok := f[k]
		if !ok {
			return "", nil
		}
		t, ok := v.(string)
		if !ok || strings.TrimSpace(t) == "" {
			return "", fmt.Errorf("%s must be a nonempty string", k)
		}
		return strings.TrimSpace(t), nil
	}
	validURL := func(v string) bool {
		u, e := url.ParseRequestURI(v)
		return e == nil && u.Hostname() != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http")
	}
	list := func(k string, max int) ([]string, error) {
		v, ok := f[k]
		if !ok {
			return nil, nil
		}
		a, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an array", k)
		}
		if len(a) > max {
			return nil, fmt.Errorf("%s supports at most %d files", k, max)
		}
		out := []string{}
		for _, v := range a {
			t, ok := v.(string)
			if !ok || !validURL(t) {
				return nil, fmt.Errorf("%s must contain public HTTP(S) URLs", k)
			}
			out = append(out, t)
		}
		return out, nil
	}
	images, e := list("image_urls", 10)
	if e != nil {
		return s, e
	}
	videos, e := list("video_urls", 5)
	if e != nil {
		return s, e
	}
	audios, e := list("audio_urls", 3)
	if e != nil {
		return s, e
	}
	first, last, mid, refs := 0, 0, 0, 0
	if v, ok := f["image_with_roles"]; ok {
		a, ok := v.([]any)
		if !ok {
			return s, fmt.Errorf("image_with_roles must be an array")
		}
		for _, v := range a {
			m, ok := v.(map[string]any)
			if !ok {
				return s, fmt.Errorf("image_with_roles items must be objects")
			}
			u, ok := m["url"].(string)
			if !ok || !validURL(u) {
				return s, fmt.Errorf("image_with_roles.url must be a public HTTP(S) URL")
			}
			r, _ := m["role"].(string)
			switch r {
			case "first_frame":
				first++
			case "last_frame":
				last++
			case "keyframe", "middle_frame":
				mid++
			case "reference_image":
				refs++
			default:
				return s, fmt.Errorf("unsupported image role")
			}
			s.Images = append(s.Images, u)
		}
	}
	for _, k := range []string{"first_frame_image", "last_frame_image"} {
		u, e := text(k)
		if e != nil {
			return s, e
		}
		if u != "" {
			if !validURL(u) {
				return s, fmt.Errorf("%s must be a public HTTP(S) URL", k)
			}
			if k == "first_frame_image" {
				first++
			} else {
				last++
			}
			s.Images = append(s.Images, u)
		}
	}
	task, e := text("omni_reference_task_type")
	if e != nil {
		return s, e
	}
	if task == "" {
		task = "auto"
	}
	switch task {
	case "auto", "reference", "edit", "extend":
	default:
		return s, fmt.Errorf("omni_reference_task_type must be auto, reference, edit or extend")
	}
	video, e := text("video_url")
	if e != nil {
		return s, e
	}
	if video != "" {
		if !validURL(video) {
			return s, fmt.Errorf("video_url must be a public HTTP(S) URL")
		}
		if task != "edit" && task != "extend" {
			return s, fmt.Errorf("video_url requires edit or extend")
		}
		if len(videos) > 0 {
			return s, fmt.Errorf("use video_url or video_urls, not both")
		}
		videos = []string{video}
	}
	s.Mode = "text"
	if task == "edit" || task == "extend" {
		s.Mode = task
		if len(videos) != 1 {
			return s, fmt.Errorf("%s requires exactly one input video", task)
		}
		if len(audios) > 0 {
			return s, fmt.Errorf("%s does not accept audio_urls", task)
		}
	} else if task == "reference" || len(videos) > 0 || len(audios) > 0 || refs > 0 {
		s.Mode = "reference"
	} else if len(images)+first+last+mid > 0 {
		s.Mode = "image"
	}
	switch s.Mode {
	case "image":
		if len(images) > 0 {
			if first+last+mid > 0 {
				return s, fmt.Errorf("do not mix image_urls and frame fields")
			}
			if len(images) > 2 {
				return s, fmt.Errorf("image_urls supports at most two frames")
			}
			first = 1
			if len(images) == 2 {
				last = 1
			}
		}
		if first != 1 || last > 1 || mid > 8 || first+last+mid > 10 {
			return s, fmt.Errorf("image generation requires exactly one first frame, at most one last frame and eight middle frames (10 total)")
		}
	case "reference":
		if first+last+mid > 0 {
			return s, fmt.Errorf("reference mode requires reference_image roles")
		}
		if len(images)+refs > 10 || len(images)+refs+len(videos)+len(audios) > 15 {
			return s, fmt.Errorf("reference inputs exceed image or total file limits")
		}
		if len(images)+refs+len(videos)+len(audios) == 0 {
			return s, fmt.Errorf("reference generation requires reference media")
		}
	case "edit":
		if first+last+mid > 0 || refs > 0 {
			return s, fmt.Errorf("edit reference images must use image_urls")
		}
	case "extend":
		if len(images)+first+mid+refs > 0 || last > 1 {
			return s, fmt.Errorf("extend supports only an optional target last frame")
		}
	}
	s.Images = append(s.Images, images...)
	prompt := ""
	if v, ok := f["prompt"]; ok {
		var valid bool
		prompt, valid = v.(string)
		if !valid {
			return s, fmt.Errorf("prompt must be a string")
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return s, fmt.Errorf("prompt is required for %s", s.Mode)
	}
	if _, ok := f["size"]; ok {
		v, e := text("size")
		if e != nil {
			return s, e
		}
		if _, ok := f["resolution"]; ok && f["resolution"] != v {
			return s, fmt.Errorf("size and resolution conflict")
		}
		f["resolution"] = v
		delete(f, "size")
	}
	if v, e := text("resolution"); e != nil {
		return s, e
	} else if v != "" {
		s.Resolution = strings.ToLower(v)
	}
	if s.Resolution != "720p" && s.Resolution != "1080p" {
		return s, fmt.Errorf("resolution must be 720p or 1080p")
	}
	f["resolution"] = s.Resolution
	if v, ok := f["ratio"]; ok {
		if ar, ok := f["aspect_ratio"]; ok && ar != v {
			return s, fmt.Errorf("ratio and aspect_ratio conflict")
		}
		f["aspect_ratio"] = v
		delete(f, "ratio")
	}
	aspect, e := text("aspect_ratio")
	if e != nil {
		return s, e
	}
	if aspect != "" {
		if s.Mode == "extend" {
			return s, fmt.Errorf("extend does not support aspect_ratio")
		}
		if aspect != "16:9" && aspect != "9:16" && aspect != "1:1" && aspect != "21:9" && !(aspect == "auto" && s.Mode == "reference") {
			return s, fmt.Errorf("unsupported aspect_ratio")
		}
	}
	if aspect == "" && s.Mode == "text" {
		f["aspect_ratio"] = "16:9"
	}
	if aspect == "" && s.Mode == "reference" {
		f["aspect_ratio"] = "auto"
	}
	setting, e := text("audio_setting")
	if e != nil {
		return s, e
	}
	if setting != "" {
		if setting != "auto" && setting != "origin" {
			return s, fmt.Errorf("audio_setting must be auto or origin")
		}
		if s.Mode != "edit" && !(s.Mode == "reference" && len(videos) > 0) {
			return s, fmt.Errorf("audio_setting requires edit or reference video")
		}
	}
	_, hasDuration := f["duration"]
	if hasDuration {
		if _, ok := f["duration"].(string); ok {
			return s, fmt.Errorf("duration must be a JSON integer")
		}
	}
	if v, ok := f["seconds"]; ok {
		if hasDuration && fmt.Sprint(f["duration"]) != fmt.Sprint(v) {
			return s, fmt.Errorf("duration and seconds conflict")
		}
		f["duration"] = v
		hasDuration = true
		delete(f, "seconds")
	}
	if hasDuration {
		n, e := strconv.Atoi(fmt.Sprint(f["duration"]))
		if e != nil || n < 3 || n > 30 {
			return s, fmt.Errorf("duration must be an integer between 3 and 30")
		}
		s.Duration = n
		f["duration"] = n
	}
	if (s.Mode == "edit" || s.Mode == "reference") && len(videos) > 0 {
		sum := 0.0
		for _, u := range videos {
			if probe == nil {
				return s, fmt.Errorf("input video probing unavailable")
			}
			v, e := probe(ctx, u)
			if e != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return s, fmt.Errorf("unable to measure input video duration")
			}
			if s.Mode == "edit" && v < 3 {
				return s, fmt.Errorf("edit requires at least three input seconds")
			}
			sum += v
		}
		s.InputSeconds = int(math.Min(30, math.Ceil(sum)))
		if s.Mode == "edit" && !hasDuration {
			s.Duration = s.InputSeconds
		}
	}
	if !hasDuration && s.Mode != "edit" {
		f["duration"] = s.Duration
	}
	s.BillableSeconds = s.Duration
	s.Variant = s.Resolution
	if (s.Mode == "edit" || s.Mode == "reference") && len(videos) > 0 {
		s.BillableSeconds += max(s.Duration, s.InputSeconds)
		s.Variant += "-input"
	}
	return s, nil
}
