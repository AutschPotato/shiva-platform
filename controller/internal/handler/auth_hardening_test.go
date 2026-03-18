package handler

import (
	"testing"

	"github.com/shiva-load-testing/controller/internal/model"
)

func TestNormalizeAuthInputRejectsNonHTTPTokenURL(t *testing.T) {
	auth := model.AuthInput{
		Enabled:            true,
		Mode:               "oauth_client_credentials",
		TokenURL:           "ftp://auth.example.com/token",
		ClientID:           "demo-client",
		ClientAuthMethod:   "basic",
		RefreshSkewSeconds: 30,
	}

	if err := normalizeAuthInput(&auth); err == nil {
		t.Fatalf("expected invalid auth token url to fail")
	}
}

func TestNormalizeAuthTokenURLMasksCredentialsAndQuery(t *testing.T) {
	got := normalizeAuthTokenURL("https://user:secret@auth.example.com/oauth/token?client_id=demo#frag")
	want := "https://auth.example.com/oauth/token"
	if got != want {
		t.Fatalf("expected normalized auth token url %q, got %q", want, got)
	}
}
