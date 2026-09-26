// SPDX-License-Identifier: GPL-3.0-or-later
package commands

import (
	"os"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/event"
)

type SetupCommand struct{}

func (c *SetupCommand) Name() string      { return "setup" }
func (c *SetupCommand) Aliases() []string { return []string{} }
func (c *SetupCommand) IsInternal() bool  { return true }
func (c *SetupCommand) ShortHelp() string { return "Launch the setup wizard" }
func (c *SetupCommand) LongHelp() string  { return help.LongHelp(c.Name()) }
func (c *SetupCommand) Run(ctx core.Context, args []string) error {
	ctx.ControlEvent(event.ControlEvent{RunWizard: true})
	writeStr(ctx, "Launching setup wizard...\n")
	return nil
}

// ---------------------------------------------------------------------------
// Provider credential setup
// ---------------------------------------------------------------------------
//
// Adding or selecting a provider must always end with a usable credential.
// bugs.md "Vercel AI Gateway: no way to add an API key" (export
// 2026-09-26-113044) observed the opposite: a catalog provider could be
// selected, but nothing ever asked for its key, and the catalog's own env name
// was dead data. The credential chain implemented here is
//
//	provider api_key in config → auth store (API key or OAuth token) → catalog env var
//
// so an already-credentialed provider is never asked again, a headless host
// degrades to an actionable message instead of a stuck prompt, and the key is
// stored in the auth store — the same vault /login:<provider>:apikey uses.

// credentialSource labels where a provider's usable credential came from.
type credentialSource string

const (
	credentialNone   credentialSource = ""
	credentialConfig credentialSource = "config api_key"
	credentialStore  credentialSource = "auth store"
	credentialEnv    credentialSource = "environment"
)

// providerCredential is a resolved credential plus its provenance: which source
// supplied it (so the caller can skip prompting) and which catalog env var
// names the provider reads (so the prompt/message can name them).
type providerCredential struct {
	// Key is the credential material; "" means none was found.
	Key string
	// Source is where Key came from.
	Source credentialSource
	// EnvKeys are the catalog's API-key environment variables for the provider.
	EnvKeys []string
}

// resolveProviderCredential resolves the credential chain for a provider id:
// the provider's api_key in config, then the auth store (API key or OAuth
// token), then the catalog's environment variables. The env step comes LAST so
// an explicit configuration always wins; without it a headless/CI run with only
// AI_GATEWAY_API_KEY exported could not authenticate at all.
func resolveProviderCredential(cfg *config.Config, providerID string) providerCredential {
	cred := providerCredential{EnvKeys: loginEnvKeys(providerID)}
	if cfg != nil {
		if p := cfg.GetProviderByID(providerID); p != nil && p.APIKey != "" {
			cred.Key, cred.Source = p.APIKey, credentialConfig
			return cred
		}
	}
	if store := sharedAuthStore(); store != nil {
		key := normalizeProviderID(providerID)
		if k, ok := store.GetAPIKey(key); ok && k != "" {
			cred.Key, cred.Source = k, credentialStore
			return cred
		}
		if tokens, ok := store.GetOAuth(key); ok && tokens != nil && tokens.AccessToken != "" {
			cred.Key, cred.Source = tokens.AccessToken, credentialStore
			return cred
		}
	}
	if key, _ := envAPIKeyFor(cred.EnvKeys); key != "" {
		cred.Key, cred.Source = key, credentialEnv
		return cred
	}
	return cred
}

// envAPIKeyFor returns the first set environment variable among envKeys and its
// trimmed value.
func envAPIKeyFor(envKeys []string) (value, key string) {
	for _, k := range envKeys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, k
		}
	}
	return "", ""
}

// providerKeyPrefill returns the value the API-key prompt is pre-filled with:
// the provider's catalog env var when it is set in the environment (the
// "read the key from where I already exported it" offer), otherwise "".
func providerKeyPrefill(providerID string) string {
	value, _ := envAPIKeyFor(loginEnvKeys(providerID))
	return value
}

// missingProviderKeyMessage is the actionable text shown when a provider ends
// up without a credential: it names the provider and the three ways to add a
// key. Hosts that cannot prompt (headless/scripts) get exactly this instead of
// a stuck input.
func missingProviderKeyMessage(providerID string, envKeys []string) string {
	msg := "No API key for " + providerID + ". Add one with /login:" + providerID + ":apikey, " +
		"set api_key on the provider in config"
	if len(envKeys) > 0 {
		msg += ", or export " + strings.Join(envKeys, " or ")
	}
	return msg + "."
}

// setupProviderCredential makes sure providerID can authenticate.
//
// When the credential chain (config api_key → auth store → catalog env var)
// already yields a key, nothing is prompted. Otherwise the API key is requested
// — pre-filled with the catalog env value when one is set — and stored with
// auth.Store.SetAPIKey. onReady always runs exactly once, with the key when the
// caller must persist it itself (no auth store wired: the previous behaviour,
// which kept the key in the provider config) and with "" in every other case;
// that keeps the surrounding add/select flow from wedging on a cancel.
func setupProviderCredential(host core.UIHost, cfg *config.Config, providerID, name string, onReady func(apiKey string)) {
	cred := resolveProviderCredential(cfg, providerID)
	if cred.Key != "" {
		onReady("")
		return
	}
	host.ShowInput("API key for "+name+":", providerKeyPrefill(providerID), func(key string, ok bool) {
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			writeFmt(host, "%s\n", missingProviderKeyMessage(providerID, cred.EnvKeys))
			onReady("")
			return
		}
		if store := sharedAuthStore(); store != nil {
			storeProviderAPIKey(host, providerID, key)
			onReady("")
			return
		}
		// No vault wired (scripts, tests): the caller persists the key where the
		// flow has always put it — the provider's api_key in config.
		onReady(key)
	})
}

// storeProviderAPIKey persists a key in the auth store (the vault /login uses).
func storeProviderAPIKey(host core.UIHost, providerID, key string) {
	store := sharedAuthStore()
	if store == nil {
		return
	}
	if err := store.SetAPIKey(normalizeProviderID(providerID), key); err != nil {
		writeFmt(host, "Could not store the API key for %s: %v\n", providerID, err)
	}
}
