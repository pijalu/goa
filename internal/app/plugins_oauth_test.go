package app

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
)

func TestPluginOAuthToken_UsesCodexAlias(t *testing.T) {
	store, err := auth.NewStore(t.TempDir() + "/auth.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOAuth("codex", &oauth.Tokens{AccessToken: "access", AccountID: "acct"}); err != nil {
		t.Fatal(err)
	}
	got, err := pluginOAuthToken(context.Background(), store, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if got["accessToken"] != "access" || got["accountId"] != "acct" {
		t.Fatalf("token = %#v", got)
	}
}
