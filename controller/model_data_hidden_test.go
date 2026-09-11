package controller

import "testing"

func TestIsHiddenChannelDataModel(t *testing.T) {
	if !isHiddenChannelDataModel(" GPT-5.4 ") {
		t.Fatal("gpt-5.4 should be hidden from channel data and marketplace")
	}
	if !isHiddenChannelDataModel(" GPT-5.4-MINI ") {
		t.Fatal("gpt-5.4-mini should be hidden from channel data and marketplace")
	}
	if isHiddenChannelDataModel("gpt-5.4-nano") {
		t.Fatal("gpt-5.4-nano should remain visible")
	}
	if !isHiddenChannelDataModel(" gemini-3.1-flash-lite ") {
		t.Fatal("gemini-3.1-flash-lite should be hidden from channel data and marketplace")
	}
	if !isHiddenChannelDataModel(" KIMI-K2.5 ") {
		t.Fatal("kimi-k2.5 should be hidden from channel data and marketplace")
	}
	if !isHiddenChannelDataModel(" GEMINI-3.5-FLASH ") {
		t.Fatal("gemini-3.5-flash should be hidden from channel data and marketplace")
	}
	if isHiddenChannelDataModel("gemini-3.6-flash") {
		t.Fatal("gemini-3.6-flash should remain visible")
	}
}
