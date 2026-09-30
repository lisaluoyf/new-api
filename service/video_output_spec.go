package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/abema/go-mp4"
	"github.com/tidwall/gjson"
)

var videoSpecProbeSlots = make(chan struct{}, 2)

type videoOutputDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func probeVideoOutputDimensions(ctx context.Context, rawURL string) (videoOutputDimensions, error) {
	setting := system_setting.GetFetchSetting()
	validate := func(url string) error {
		return common.ValidateURLWithFetchSetting(url, setting.EnableSSRFProtection, setting.AllowPrivateIp, setting.DomainFilterMode, setting.IpFilterMode, setting.DomainList, setting.IpList, setting.AllowedPorts, setting.ApplyIPFilterForDomain)
	}
	if err := validate(rawURL); err != nil {
		return videoOutputDimensions{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return videoOutputDimensions{}, err
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many media redirects")
		}
		return validate(req.URL.String())
	}}
	resp, err := client.Do(req)
	if err != nil {
		return videoOutputDimensions{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return videoOutputDimensions{}, fmt.Errorf("media fetch failed")
	}
	f, err := os.CreateTemp("", "video-spec-*.mp4")
	if err != nil {
		return videoOutputDimensions{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, probeVideoMaxBytes+1))
	if err != nil {
		return videoOutputDimensions{}, err
	}
	if n > probeVideoMaxBytes {
		return videoOutputDimensions{}, fmt.Errorf("media exceeds probe limit")
	}
	return readVideoOutputDimensions(f)
}

func readVideoOutputDimensions(reader io.ReadSeeker) (videoOutputDimensions, error) {
	boxes, err := mp4.ExtractBoxWithPayload(reader, nil, mp4.BoxPath{mp4.BoxTypeMoov(), mp4.BoxTypeTrak(), mp4.BoxTypeTkhd()})
	if err != nil {
		return videoOutputDimensions{}, err
	}
	for _, box := range boxes {
		if track, ok := box.Payload.(*mp4.Tkhd); ok && track.Width > 0 && track.Height > 0 {
			return videoOutputDimensions{int(track.Width >> 16), int(track.Height >> 16)}, nil
		}
	}

	return videoOutputDimensions{}, fmt.Errorf("video dimensions unavailable")
}

// Only inspect terminal output. This never participates in billing, and a
// bounded asynchronous probe cannot delay task completion or create a charge.
func backfillVideoOutputSpec(task *model.Task, result *relaycommon.TaskInfo) {
	if task == nil || task.Status != model.TaskStatusSuccess {
		return
	}
	name := strings.ToLower(taskModelName(task))
	if name != "seedance-2.0" && name != "doubao-seedance-2.0" && name != "seedance-2.5" && name != "minimax-h3" {
		return
	}
	width, height := 0, 0
	for _, base := range []string{"data.result", "data", "result", ""} {
		prefix := base
		if prefix != "" {
			prefix += "."
		}
		w, h := int(gjson.GetBytes(task.Data, prefix+"width").Int()), int(gjson.GetBytes(task.Data, prefix+"height").Int())
		if w > 0 && h > 0 {
			width, height = w, h
			break
		}
	}
	store := func(dim videoOutputDimensions, source string) {
		_ = model.UpdateLogResultByTaskID(task.UserId, task.TaskID, 0, map[string]interface{}{"output_spec_status": "verified", "output_spec": map[string]interface{}{"width": dim.Width, "height": dim.Height, "source": source}})
	}
	if width > 0 && height > 0 {
		store(videoOutputDimensions{width, height}, "provider")
		return
	}
	url := task.GetUpstreamVideoURL()
	if url == "" && result != nil {
		url = result.RemoteUrl
		if url == "" {
			url = result.Url
		}
	}
	if url == "" {
		url = task.GetResultURL()
	}
	_ = model.UpdateLogResultByTaskID(task.UserId, task.TaskID, 0, map[string]interface{}{"output_spec_status": "pending"})
	select {
	case videoSpecProbeSlots <- struct{}{}:
	default:
		_ = model.UpdateLogResultByTaskID(task.UserId, task.TaskID, 0, map[string]interface{}{"output_spec_status": "unavailable", "output_spec_reason": "probe_busy"})
		return
	}
	uid, id := task.UserId, task.TaskID
	go func() {
		defer func() { <-videoSpecProbeSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		dim, err := probeVideoOutputDimensions(ctx, url)
		if err != nil {
			_ = model.UpdateLogResultByTaskID(uid, id, 0, map[string]interface{}{"output_spec_status": "unavailable", "output_spec_reason": "media_probe_failed"})
			return
		}
		_ = model.UpdateLogResultByTaskID(uid, id, 0, map[string]interface{}{"output_spec_status": "verified", "output_spec": map[string]interface{}{"width": dim.Width, "height": dim.Height, "source": "mp4_probe"}})
	}()
}
