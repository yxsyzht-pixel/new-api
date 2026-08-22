package controller

import "testing"

func TestMaskDSNRemovesPassword(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"empty", "", ""},
		{
			"url with password",
			"postgres://logger:s3cret@10.0.0.1:5432/chatlog?sslmode=disable",
			"postgres://logger:***@10.0.0.1:5432/chatlog?sslmode=disable",
		},
		{
			"url without password",
			"postgres://logger@10.0.0.1:5432/chatlog",
			"postgres://logger@10.0.0.1:5432/chatlog",
		},
		{"key value form", "host=10.0.0.1 user=logger password=s3cret", "(已配置)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskDSN(tc.dsn); got != tc.want {
				t.Fatalf("maskDSN(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}
