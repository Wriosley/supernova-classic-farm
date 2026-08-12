package friend

import "testing"

func TestGenerateFriendShareURLUsesConfiguredBaseURL(t *testing.T) {
	t.Parallel()
	got, err := FriendShareURL("https://farm.example.com/", "ABC123")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://farm.example.com/invite/friend?code=ABC123"
	if got != want {
		t.Fatalf("share URL = %q, want %q", got, want)
	}
}

func TestShareURLUsesFriendCodeExpirationIsIndependentOfURL(t *testing.T) {
	t.Parallel()
	// The URL is only a representation of the code; expiration lives on the
	// FriendCode row. Encoding special characters must not invent a second TTL.
	got, err := FriendShareURL("http://localhost:5173", "AB+C/1")
	if err != nil {
		t.Fatal(err)
	}
	want := "http://localhost:5173/invite/friend?code=AB%2BC%2F1"
	if got != want {
		t.Fatalf("escaped share URL = %q, want %q", got, want)
	}
}

func TestLoadPublicWebBaseURLDefaultsToLocalVite(t *testing.T) {
	t.Setenv("PUBLIC_WEB_BASE_URL", "")
	got, err := LoadPublicWebBaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultPublicWebBaseURL {
		t.Fatalf("default base URL = %q, want %q", got, defaultPublicWebBaseURL)
	}
}

func TestNormalizePublicWebBaseURLRejectsNonHTTP(t *testing.T) {
	t.Parallel()
	if _, err := normalizePublicWebBaseURL("ftp://example.com"); err == nil {
		t.Fatal("expected scheme error")
	}
}
