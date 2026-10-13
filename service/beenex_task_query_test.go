package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type beeNexRecoveryFixtureAdaptor struct {
	response string
	model    string
	fail     bool
	calls    *int
}

func (a beeNexRecoveryFixtureAdaptor) Init(*relaycommon.RelayInfo) {}
func (a beeNexRecoveryFixtureAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	panic("must use frozen Seedance billing")
}
func (a beeNexRecoveryFixtureAdaptor) FetchTask(_ string, key string, body map[string]any, _ string) (*http.Response, error) {
	*a.calls++
	if a.fail {
		return nil, errors.New("BeeNex query HTTP 502")
	}
	if body["origin_model_name"] != a.model || key != "own-stored-key" {
		return nil, errors.New("wrong channel task context")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(a.response))}, nil
}
func (a beeNexRecoveryFixtureAdaptor) ParseTaskResult(raw []byte) (*relaycommon.TaskInfo, error) {
	var p struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		Progress    int    `json:"progress"`
		CompletedAt int64  `json:"completed_at"`
		Metadata    struct {
			URL string `json:"url"`
		} `json:"metadata"`
	}
	if err := common.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.Status == "processing" {
		return &relaycommon.TaskInfo{TaskID: p.ID, Status: "IN_PROGRESS", Progress: fmt.Sprintf("%d%%", p.Progress)}, nil
	}
	return &relaycommon.TaskInfo{TaskID: p.ID, Status: "SUCCESS", Progress: "100%", CompletedAt: p.CompletedAt, Url: p.Metadata.URL}, nil
}

func TestBeeNexExistingTaskRecoverySettlesOncePreservesContext(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		t.Run(name, func(t *testing.T) { testBeeNexExistingTaskRecovery(t, name) })
	}
}
func testBeeNexExistingTaskRecovery(t *testing.T, name string) {
	truncate(t)
	setupTaskWebhookTest(t)
	seedUser(t, 91274, 2_000_000)
	seedToken(t, 91274, 91274, "customer-test-key", 2_000_000)
	seedChannel(t, 274)
	oldFactory := GetTaskAdaptorFunc
	calls := 0
	failed := true
	GetTaskAdaptorFunc = func(platform constant.TaskPlatform) TaskPollingAdaptor {
		require.EqualValues(t, constant.TaskPlatformBeeNex, platform)
		return beeNexRecoveryFixtureAdaptor{model: name, response: fmt.Sprintf(`{"id":"supplier-existing","status":"completed","completed_at":%d,"metadata":{"url":"https://example.com/video.mp4"}}`, time.Now().Unix()-60), fail: failed, calls: &calls}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = oldFactory })
	task := makeTask(91274, 274, 215880, 91274, BillingSourceWallet, 0)
	task.TaskID = "task_existing_paid"
	task.Platform = constant.TaskPlatformBeeNex
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix() - 240
	task.Properties.OriginModelName = name
	task.PrivateData.Key = "own-stored-key"
	task.PrivateData.UpstreamTaskID = "supplier-existing"
	task.PrivateData.SeedanceRequest = map[string]any{"duration": 4, "tariff_unit_rate": .1028, "seed": float64(0), "output_format": "mp4"}
	task.PrivateData.BillingContext.SeedanceTariff = true
	task.PrivateData.BillingContext.OriginModelName = name
	task.PrivateData.BillingContext.GroupRatio = 1.05
	task.PrivateData.BillingContext.ModelPrice = .1028
	task.PrivateData.BillingContext.OtherRatios = map[string]float64{"size": 1, "seconds": 4}
	seconds := 4.041667
	task.PrivateData.SeedanceOutput = &model.SeedanceOutputDuration{ActualSeconds: &seconds, BillableSeconds: 4, Source: "apimaster_probe"}
	task.PrivateData.SeedanceBillingReceiptEnabled = true
	require.NoError(t, model.DB.Create(task).Error)
	details, _ := common.Marshal(model.SeedanceBillingDetails{TaskID: task.TaskID, SettlementStatus: "pending", InitialQuotaUnits: 215880, ReservedAmount: "0.431760", Currency: "USD", AccountUnitUSD: "0.000002"})
	require.NoError(t, model.DB.Create(&model.SeedanceBillingReceipt{ID: "receipt-existing", TaskID: task.TaskID, UserID: task.UserId, Status: "pending", InitialQuota: 215880, Details: details}).Error)
	contextBefore, _ := common.Marshal(task.PrivateData.BillingContext)
	userBefore := getUserQuota(t, task.UserId)
	require.Error(t, RefreshBeeNexSeedanceTask(context.Background(), task))
	require.Equal(t, "50%", task.Progress)
	require.EqualValues(t, "IN_PROGRESS", task.Status)
	require.Equal(t, userBefore, getUserQuota(t, task.UserId))
	failed = false
	require.NoError(t, RefreshBeeNexSeedanceTask(context.Background(), task))
	require.EqualValues(t, "SUCCESS", task.Status)
	require.Equal(t, "100%", task.Progress)
	require.Equal(t, "task_existing_paid", task.TaskID)
	require.Equal(t, 274, task.ChannelId)
	require.Equal(t, "supplier-existing", task.GetUpstreamTaskID())
	require.Equal(t, 215880, task.Quota)
	require.Less(t, task.FinishTime, time.Now().Unix()-30)
	contextAfter, _ := common.Marshal(task.PrivateData.BillingContext)
	require.Equal(t, contextBefore, contextAfter)
	require.Equal(t, float64(0), task.PrivateData.SeedanceRequest["seed"])
	receipt, err := model.GetSeedanceBillingReceipt(task.UserId, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "settled", receipt.Status)
	var count int64
	require.NoError(t, model.DB.Model(&model.VideoVerificationRun{}).Where("task_id = ?", task.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	quotaAfter := getUserQuota(t, task.UserId)
	callsAfter := calls
	for i := 0; i < 3; i++ {
		require.NoError(t, RefreshBeeNexSeedanceTask(context.Background(), task))
	}
	require.Equal(t, callsAfter, calls)
	require.Equal(t, quotaAfter, getUserQuota(t, task.UserId))
	// Other channels never enter this recovery path.
	other := *task
	other.ChannelId = 273
	other.Status = model.TaskStatusInProgress
	require.NoError(t, RefreshBeeNexSeedanceTask(context.Background(), &other))
	require.Equal(t, callsAfter, calls)
}

func TestBeeNexQueryProtocolConfiguration(t *testing.T) {
	ch := &model.Channel{Id: 274}
	protocol, err := BeeNexTaskQueryProtocol(ch, "seedance-2.0")
	require.NoError(t, err)
	require.Equal(t, "video_generations", protocol)
	setting := `{"beenex_query_protocols":{"seedance-2.0":"sora","seedance-2.5":"video_generations"}}`
	ch.Setting = &setting
	require.NoError(t, ch.ValidateSettings())
	protocol, err = BeeNexTaskQueryProtocol(ch, "seedance-2.0")
	require.NoError(t, err)
	require.Equal(t, "sora", protocol)
	protocol, err = BeeNexTaskQueryProtocol(ch, "seedance-2.5")
	require.NoError(t, err)
	require.Equal(t, "video_generations", protocol)
	setting = `{"beenex_query_protocols":{"seedance-2.0":"https://unsafe.example"}}`
	require.Error(t, ch.ValidateSettings())
	_, err = BeeNexTaskQueryProtocol(ch, "seedance-2.0")
	require.Error(t, err)
	ch.Id = 273
	require.Error(t, ch.ValidateSettings())
	_, err = BeeNexTaskQueryProtocol(ch, "seedance-2.0")
	require.Error(t, err)
	require.NotContains(t, BeeNexTaskQueryFailureSummary(errors.New("https://signed.example?secret=credential")), "credential")
}

func TestBeeNexPendingQueryPersistenceFailureIsReported(t *testing.T) {
	truncate(t)
	setupTaskWebhookTest(t)
	seedChannel(t, 274)
	oldFactory := GetTaskAdaptorFunc
	t.Cleanup(func() { GetTaskAdaptorFunc = oldFactory })
	calls := 0
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return beeNexRecoveryFixtureAdaptor{model: "seedance-2.0", response: `{"id":"supplier-existing","status":"processing","progress":37}`, calls: &calls}
	}
	task := &model.Task{TaskID: "task_pending", ChannelId: 274, Status: model.TaskStatusInProgress, Progress: "50%", Properties: model.Properties{OriginModelName: "seedance-2.0"}, PrivateData: model.TaskPrivateData{Key: "own-stored-key", UpstreamTaskID: "supplier-existing"}}
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("beenex_fail_state_update", func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			tx.AddError(errors.New("task state write failed"))
		}
	}))
	t.Cleanup(func() { model.DB.Callback().Update().Remove("beenex_fail_state_update") })
	require.ErrorContains(t, RefreshBeeNexSeedanceTask(context.Background(), task), "task state write failed")
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	require.Equal(t, "50%", persisted.Progress)
	require.Equal(t, model.TaskStatusInProgress, persisted.Status)
	require.Equal(t, 1, calls)
}
