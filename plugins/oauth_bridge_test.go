package plugins

import (
	"context"
	"fmt"
	"testing"
)

func TestJSOAuthTokenBridge(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	ctx.Extended.OAuthToken = func(_ context.Context, provider string) (map[string]any, error) {
		if provider != "openai" {
			return nil, fmt.Errorf("unsupported provider")
		}
		return map[string]any{"accessToken": "access", "accountId": "acct"}, nil
	}
	bridge := runJS(t, ctx, `var token = goa.auth.oauthToken("openai"); goa.__result = token.accessToken + ":" + token.accountId;`)
	if got := goaResult(t, bridge, "__result").String(); got != "access:acct" {
		t.Fatalf("token = %q", got)
	}
}
