package service

import "testing"

func TestResolvePricingRoot(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "Packy load balancer uses website pricing API",
			baseURL: "https://slb-v1.api.fan",
			want:    "https://www.packyapi.com",
		},
		{
			name:    "Packy load balancer mapping ignores API path",
			baseURL: "https://slb-v1.api.fan/v1/",
			want:    "https://www.packyapi.com",
		},
		{
			name:    "generic v1 endpoint uses host root",
			baseURL: "https://relay.example.com/v1",
			want:    "https://relay.example.com",
		},
		{
			name:    "generic root remains unchanged",
			baseURL: "https://relay.example.com",
			want:    "https://relay.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolvePricingRoot(tt.baseURL); got != tt.want {
				t.Fatalf("ResolvePricingRoot(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}
