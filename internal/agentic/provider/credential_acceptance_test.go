// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"os"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCredentialValidation_BeforeNetworkIO is the P15 acceptance core: a
// malformed key must produce a clear InvalidCredential error before any
// network I/O happens. The capture transport would record a request if the
// pipeline ever got past the auth hook; it must stay untouched.
func TestCredentialValidation_BeforeNetworkIO(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "sk-bad key with space")
	defer os.Unsetenv("OPENAI_API_KEY")

	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &captureTransport{}
	transport.SetDefault(capt)

	_, err := GenericStream(openAIModel,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{Headers: map[string]string{}})
	require.Error(t, err)
	var inv *schema.InvalidCredentialError
	require.ErrorAs(t, err, &inv, "malformed env key must surface as InvalidCredential, got: %v", err)
	assert.Equal(t, "OPENAI_API_KEY", inv.Source)
	assert.Nil(t, capt.req, "no network I/O may happen before credential validation")
}

// TestCredentialValidation_ExplicitKeyBeforeNetworkIO is the same acceptance
// for a key passed explicitly through StreamOptions (agent config path).
func TestCredentialValidation_ExplicitKeyBeforeNetworkIO(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &captureTransport{}
	transport.SetDefault(capt)

	_, err := GenericStream(openAIModel,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{APIKey: "sk-\u00a0bad", Headers: map[string]string{}})
	require.Error(t, err)
	var inv *schema.InvalidCredentialError
	require.ErrorAs(t, err, &inv)
	assert.Equal(t, "options.api_key", inv.Source)
	assert.Nil(t, capt.req, "no network I/O may happen before credential validation")
}

// TestCredentialValidation_MissingListsSources is the P15 acceptance for the
// missing case: the error must list every env var/config source checked and be
// distinguishable as MissingCredential.
func TestCredentialValidation_MissingListsSources(t *testing.T) {
	os.Unsetenv("DEEPSEEK_API_KEY")

	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &captureTransport{}
	transport.SetDefault(capt)

	_, err := GenericStream(deepSeekModel,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{Headers: map[string]string{}})
	require.Error(t, err)
	var miss *schema.MissingCredentialError
	require.ErrorAs(t, err, &miss, "missing required key must surface as MissingCredential, got: %v", err)
	assert.Equal(t, "deepseek", miss.Provider)
	require.ElementsMatch(t, []string{"options.api_key", "DEEPSEEK_API_KEY"}, miss.Sources)
	assert.Contains(t, err.Error(), "DEEPSEEK_API_KEY")
	assert.Nil(t, capt.req, "no network I/O may happen without a credential")
}

// TestStream_MalformedEnvKeyRejectedAtResolution exercises the stream() entry
// point: GetEnvAPIKey validates at resolution time, so the error surfaces even
// before the hook pipeline runs.
func TestStream_MalformedEnvKeyRejectedAtResolution(t *testing.T) {
	os.Setenv("DEEPSEEK_API_KEY", "bad key")
	defer os.Unsetenv("DEEPSEEK_API_KEY")

	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &captureTransport{}
	transport.SetDefault(capt)

	_, err := Stream(deepSeekModel,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{})
	require.Error(t, err)
	var inv *schema.InvalidCredentialError
	require.ErrorAs(t, err, &inv)
	assert.Equal(t, "DEEPSEEK_API_KEY", inv.Source)
	assert.Nil(t, capt.req, "no network I/O may happen before credential validation")
}

// TestStream_MissingEnvKeyRejectedAtPipeline verifies the stream() entry point
// still produces MissingCredential (with sources) when the env key is absent
// and the provider requires one.
func TestStream_MissingEnvKeyRejectedAtPipeline(t *testing.T) {
	os.Unsetenv("DEEPSEEK_API_KEY")

	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &captureTransport{}
	transport.SetDefault(capt)

	_, err := Stream(deepSeekModel,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{})
	require.Error(t, err)
	var miss *schema.MissingCredentialError
	require.ErrorAs(t, err, &miss)
	require.Contains(t, miss.Sources, "DEEPSEEK_API_KEY")
	assert.Nil(t, capt.req)
}

// captureTransport (defined in runtime_test.go) records the last request it
// was asked to send; the credential tests assert it is never touched.
