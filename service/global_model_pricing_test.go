package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func TestGlobalModelPricingUSD_minimaxM31Flash(t *testing.T) {
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"MiniMax-M3.1-Flash":0.075}`); err != nil {
		t.Fatal(err)
	}
	if err := ratio_setting.UpdateCompletionRatioByJSONString(`{"MiniMax-M3.1-Flash":4}`); err != nil {
		t.Fatal(err)
	}
	if err := ratio_setting.UpdateCacheRatioByJSONString(`{"MiniMax-M3.1-Flash":0.1}`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minimax-m3.1-flash", "MiniMax-M3.1-Flash"} {
		input, output, cache, _, ok := GlobalModelPricingUSD(name)
		if !ok || math.IsNaN(input+output+cache) || math.Abs(input-0.15) > 1e-9 || math.Abs(output-0.6) > 1e-9 || math.Abs(cache-0.015) > 1e-9 {
			t.Fatalf("%s pricing = %v/%v/%v, ok=%v; want 0.15/0.6/0.015", name, input, output, cache, ok)
		}
	}
}

func TestGlobalModelPricingUSD_minimax(t *testing.T) {
	jsonRatio := `{"minimax-m3":0.15}`
	if err := ratio_setting.UpdateModelRatioByJSONString(jsonRatio); err != nil {
		t.Fatal(err)
	}
	jsonComp := `{"minimax-m3":4}`
	if err := ratio_setting.UpdateCompletionRatioByJSONString(jsonComp); err != nil {
		t.Fatal(err)
	}
	jsonCache := `{"minimax-m3":0.2}`
	if err := ratio_setting.UpdateCacheRatioByJSONString(jsonCache); err != nil {
		t.Fatal(err)
	}

	in, out, cache, _, ok := GlobalModelPricingUSD("minimax-m3")
	if !ok {
		t.Fatal("expected ok")
	}
	if in != 0.3 {
		t.Fatalf("input=%v want 0.3", in)
	}
	if out != 1.2 {
		t.Fatalf("output=%v want 1.2", out)
	}
	if cache != 0.06 {
		t.Fatalf("cache=%v want 0.06", cache)
	}
}

func TestGlobalModelPricingUSD_minimax_alias(t *testing.T) {
	jsonRatio := `{"minimax-m3":0.15}`
	if err := ratio_setting.UpdateModelRatioByJSONString(jsonRatio); err != nil {
		t.Fatal(err)
	}
	in, _, _, _, ok := GlobalModelPricingUSD("MiniMax-M3")
	if !ok {
		t.Fatal("expected ok for provider alias MiniMax-M3")
	}
	if in != 0.3 {
		t.Fatalf("input=%v want 0.3", in)
	}
}
