package controller

import "testing"

func TestIsHiddenChannelDataModel(t *testing.T) {
	if !isHiddenChannelDataModel(" GPT-5.4 ") {
		t.Fatal("gpt-5.4 should be hidden from channel data and marketplace")
	}
	if isHiddenChannelDataModel("gpt-5.4-mini") {
		t.Fatal("gpt-5.4-mini should remain visible")
	}
	if !isHiddenChannelDataModel(" gemini-3.1-flash-lite ") {
		t.Fatal("gemini-3.1-flash-lite should be hidden from channel data and marketplace")
	}
	if !isHiddenChannelDataModel(" KIMI-K2.5 ") {
		t.Fatal("kimi-k2.5 should be hidden from channel data and marketplace")
	}
	if isHiddenChannelDataModel("gemini-3.5-flash") {
		t.Fatal("gemini-3.5-flash should remain visible")
	}
}
