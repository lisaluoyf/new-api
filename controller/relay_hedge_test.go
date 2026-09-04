package controller

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	modelsetting "github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func TestAwaitNonStreamRaceOutcomeReturnsFirstSuccessfulAttempt(t *testing.T) {
	primary := &hedgeAttempt{role: "primary", done: make(chan struct{})}
	hedge := &hedgeAttempt{role: "hedge", done: make(chan struct{})}
	winnerCh := make(chan *hedgeAttempt, 1)

	winnerCh <- hedge
	winner := awaitNonStreamRaceOutcome(winnerCh, primary, hedge)
	if winner != hedge {
		t.Fatalf("winner = %v, want hedge", winner)
	}
}

func TestAwaitNonStreamRaceOutcomeSkipsFailedPrimary(t *testing.T) {
	primary := &hedgeAttempt{
		role: "primary",
		err:  types.NewError(errors.New("upstream failed"), types.ErrorCodeDoRequestFailed),
		done: make(chan struct{}),
	}
	hedge := &hedgeAttempt{role: "hedge", done: make(chan struct{})}
	winnerCh := make(chan *hedgeAttempt, 1)

	close(primary.done)
	close(hedge.done)
	winnerCh <- hedge

	winner := awaitNonStreamRaceOutcome(winnerCh, primary, hedge)
	if winner != hedge {
		t.Fatalf("winner = %v, want hedge", winner)
	}
}

func TestAwaitNonStreamRaceOutcomeDoesNotInferWinnerFromDone(t *testing.T) {
	primary := &hedgeAttempt{role: "primary", done: make(chan struct{})}
	hedge := &hedgeAttempt{role: "hedge", done: make(chan struct{})}
	winnerCh := make(chan *hedgeAttempt, 1)

	close(primary.done)
	close(hedge.done)

	if winner := awaitNonStreamRaceOutcome(winnerCh, primary, hedge); winner != nil {
		t.Fatalf("winner = %v, want nil without declared winner", winner)
	}
}

func TestAwaitNonStreamRaceOutcomeUsesDeclaredWinnerWhenBothDone(t *testing.T) {
	primary := &hedgeAttempt{role: "primary", done: make(chan struct{})}
	hedge := &hedgeAttempt{role: "hedge", done: make(chan struct{})}
	winnerCh := make(chan *hedgeAttempt, 1)

	close(primary.done)
	close(hedge.done)
	winnerCh <- hedge

	if winner := awaitNonStreamRaceOutcome(winnerCh, primary, hedge); winner != hedge {
		t.Fatalf("winner = %v, want declared hedge winner", winner)
	}
}

func TestAwaitNonStreamRaceOutcomeBothFailed(t *testing.T) {
	primary := &hedgeAttempt{
		role: "primary",
		err:  types.NewError(errors.New("primary failed"), types.ErrorCodeDoRequestFailed),
		done: make(chan struct{}),
	}
	hedge := &hedgeAttempt{
		role: "hedge",
		err:  types.NewError(errors.New("hedge failed"), types.ErrorCodeDoRequestFailed),
		done: make(chan struct{}),
	}
	winnerCh := make(chan *hedgeAttempt, 1)

	close(primary.done)
	close(hedge.done)

	if winner := awaitNonStreamRaceOutcome(winnerCh, primary, hedge); winner != nil {
		t.Fatalf("winner = %v, want nil", winner)
	}
}

func TestHasBillableHedgeUsage(t *testing.T) {
	attempt := &hedgeAttempt{
		info: &relaycommon.RelayInfo{
			HedgeState: &relaycommon.HedgeAttemptState{Role: relaycommon.HedgeRolePrimary},
		},
	}
	if hasBillableHedgeUsage(attempt) {
		t.Fatal("attempt without deferred usage should not be billable")
	}

	attempt.info.HedgeState.TryDefer(&dto.Usage{}, nil)
	if hasBillableHedgeUsage(attempt) {
		t.Fatal("zero usage should not be billable")
	}

	attempt.info.HedgeState = &relaycommon.HedgeAttemptState{Role: relaycommon.HedgeRolePrimary}
	attempt.info.HedgeState.TryDefer(&dto.Usage{PromptTokens: 10}, nil)
	if !hasBillableHedgeUsage(attempt) {
		t.Fatal("positive usage should be billable")
	}
}

func TestShouldClientGoneHedgeSkipsAudioBillingModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		_, _ = modelsetting.ApplyClientGoneFallbackSettings(`{"policies":[]}`)
	})
	if _, err := modelsetting.ApplyClientGoneFallbackSettings(`{"policies":[{"enabled":true,"model_id":"gpt-4o-audio-preview","frt_timeout_seconds":1,"mode":"non_stream"}]}`); err != nil {
		t.Fatalf("apply clientgone fallback settings: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4o-audio-preview",
		RelayMode:       relayconstant.RelayModeChatCompletions,
	}

	if _, ok := shouldClientGoneHedge(ctx, info, types.RelayFormatOpenAI, 0); ok {
		t.Fatal("audio billing model should not enter text hedge")
	}
}

func TestSyncHedgeUseChannelsCopiesFullRouteToAttempts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sourceRecorder := httptest.NewRecorder()
	source, _ := gin.CreateTestContext(sourceRecorder)
	source.Set("use_channel", []string{"11", "22"})

	primaryRecorder := httptest.NewRecorder()
	primaryCtx, _ := gin.CreateTestContext(primaryRecorder)
	primaryCtx.Set("use_channel", []string{"11"})

	hedgeRecorder := httptest.NewRecorder()
	hedgeCtx, _ := gin.CreateTestContext(hedgeRecorder)
	hedgeCtx.Set("use_channel", []string{"11"})

	syncHedgeUseChannels(source,
		&hedgeAttempt{role: relaycommon.HedgeRolePrimary, c: primaryCtx},
		&hedgeAttempt{role: relaycommon.HedgeRoleHedge, c: hedgeCtx},
	)

	for name, ctx := range map[string]*gin.Context{"primary": primaryCtx, "hedge": hedgeCtx} {
		got := ctx.GetStringSlice("use_channel")
		if len(got) != 2 || got[0] != "11" || got[1] != "22" {
			t.Fatalf("%s use_channel = %#v, want [11 22]", name, got)
		}
	}
}
