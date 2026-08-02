package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGroupRatioTreatsEmptySuccessBodyAsUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	groupRatio, err := fetchGroupRatio(context.Background(), server.Client(), server.URL, "")
	if err != nil {
		t.Fatalf("fetchGroupRatio() error = %v, want nil", err)
	}
	if groupRatio != nil {
		t.Fatalf("fetchGroupRatio() = %#v, want nil", groupRatio)
	}
}

func TestFetchGroupRatioTreatsHTMLSuccessBodyAsUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html></html>"))
	}))
	defer server.Close()

	groupRatio, err := fetchGroupRatio(context.Background(), server.Client(), server.URL, "")
	if err != nil {
		t.Fatalf("fetchGroupRatio() error = %v, want nil", err)
	}
	if groupRatio != nil {
		t.Fatalf("fetchGroupRatio() = %#v, want nil", groupRatio)
	}
}

func TestFetchGroupRatioDecodesValidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"group_ratio":{"default":1,"vip":0.8}}`))
	}))
	defer server.Close()

	groupRatio, err := fetchGroupRatio(context.Background(), server.Client(), server.URL, "")
	if err != nil {
		t.Fatalf("fetchGroupRatio() error = %v, want nil", err)
	}
	if groupRatio["default"] != 1 || groupRatio["vip"] != 0.8 {
		t.Fatalf("fetchGroupRatio() = %#v, want default=1 and vip=0.8", groupRatio)
	}
}

func TestFetchGroupRatioRejectsMalformedNonEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	if _, err := fetchGroupRatio(context.Background(), server.Client(), server.URL, ""); err == nil {
		t.Fatal("fetchGroupRatio() error = nil, want malformed JSON error")
	}
}
