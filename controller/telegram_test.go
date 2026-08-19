package controller

import "testing"

func TestIsTelegramGroupMember(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		isMember bool
		want     bool
	}{
		{name: "member", status: "member", want: true},
		{name: "administrator", status: "administrator", want: true},
		{name: "creator", status: "creator", want: true},
		{name: "restricted member", status: "restricted", isMember: true, want: true},
		{name: "restricted non member", status: "restricted", want: false},
		{name: "left", status: "left", want: false},
		{name: "kicked", status: "kicked", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTelegramGroupMember(tt.status, tt.isMember); got != tt.want {
				t.Fatalf("isTelegramGroupMember(%q, %t) = %t, want %t", tt.status, tt.isMember, got, tt.want)
			}
		})
	}
}
