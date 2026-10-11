package auth

import "testing"

func TestSafeRedirect(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/phrases/1?x=y":       "/phrases/1?x=y",
		"//evil.example":       "/",
		`/\evil.example`:       "/",
		"https://evil.example": "/",
	}
	for in, want := range cases {
		if got := safeRedirect(in); got != want {
			t.Errorf("safeRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}
