package model

import "testing"

func TestFormatUserRegistrationChannel(t *testing.T) {
	tests := []struct {
		name string
		user *User
		want string
	}{
		{
			name: "direct",
			user: &User{RegistrationChannelCode: "direct"},
			want: "-",
		},
		{
			name: "managed channel uses name",
			user: &User{RegistrationChannelCode: "google", RegistrationChannelName: "google"},
			want: "google",
		},
		{
			name: "fallback to code",
			user: &User{RegistrationChannelCode: "bing"},
			want: "bing",
		},
		{
			name: "referral uses inviter email",
			user: &User{
				RegistrationChannelCode:  "referral",
				RegistrationChannelName:  "referral",
				RegistrationInviterEmail: "friend@example.com",
			},
			want: "friend@example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatUserRegistrationChannel(tc.user); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
