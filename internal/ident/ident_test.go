package ident

import "testing"

func TestParse(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ENG-123", "ENG-123"},
		{"eng-7", "ENG-7"},
		{"  ENG2-10 ", "ENG2-10"},
		{"https://linear.app/example/issue/ENG-123/fix-the-login-page", "ENG-123"},
		{"https://linear.app/acme/issue/acme-9", "ACME-9"},
		{"https://linear.app/acme/issue/ACME-9/title#comment-abc123", "ACME-9"},
	} {
		got, err := Parse(tc.in)
		if err != nil || got.String() != tc.want {
			t.Errorf("Parse(%q) = %v, %v; want %s", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "123", "ENG", "ENG-", "ENG-12a", "-12", "https://example.com/x/issue/ENG-1", "https://linear.app/acme/project/foo", "https://linear.app/acme"} {
		if got, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) = %v, want error", bad, got)
		}
	}
}
