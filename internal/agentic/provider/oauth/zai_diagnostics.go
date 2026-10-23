// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package oauth

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

const zaiDiagnosticLimit = 400

var zaiDiagnosticURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// zaiOAuthRequiredError keeps the stable control-flow sentinel in the error
// tree while presenting only the sanitized diagnostic to users. The original
// cause remains available to errors.Is/errors.As; Error never exposes it raw.
type zaiOAuthRequiredError struct {
	diagnostic string
	cause      error
}

func (e *zaiOAuthRequiredError) Error() string { return e.diagnostic }

func (e *zaiOAuthRequiredError) Unwrap() []error {
	return []error{ErrZaiOAuthRequired, e.cause}
}

// requireZaiOAuth is the single error boundary for the interactive z.ai flow.
// Secrets accumulate as the flow learns them, so a failure at any later stage
// can quote a server/transport cause without exposing earlier credentials.
func requireZaiOAuth(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	return &zaiOAuthRequiredError{
		diagnostic: sanitizeZaiDiagnostic(err.Error(), secrets),
		cause:      err,
	}
}

// sanitizeZaiDiagnostic removes every known form of a flow credential, strips
// credentials embedded in URLs, and flattens untrusted terminal text into one
// bounded line suitable for an error flash or log entry.
func sanitizeZaiDiagnostic(message string, secrets []string) string {
	redacted := message
	for _, secret := range zaiSecretVariants(secrets) {
		redacted = strings.ReplaceAll(redacted, secret, "[redacted]")
	}
	redacted = zaiDiagnosticURLPattern.ReplaceAllStringFunc(redacted, sanitizeZaiDiagnosticURL)
	redacted = strings.Join(strings.Fields(ansi.Sanitize(redacted)), " ")
	if redacted == "" {
		return "z.ai OAuth login failed"
	}
	runes := []rune(redacted)
	if len(runes) > zaiDiagnosticLimit {
		return string(runes[:zaiDiagnosticLimit-1]) + "…"
	}
	return redacted
}

// zaiSecretVariants covers credentials copied verbatim, into a query, or into
// a path. Longest-first replacement prevents a short component from exposing
// the remainder of a full authorization URL.
func zaiSecretVariants(secrets []string) []string {
	var variants []string
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		variants = append(variants, secret, url.QueryEscape(secret), url.PathEscape(secret))
	}
	sort.SliceStable(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	return variants
}

// sanitizeZaiDiagnosticURL retains enough of a URL to identify its host and
// path while dropping userinfo, query values, and fragments. If parsing fails,
// conceal the whole URL rather than risk passing a credential through.
func sanitizeZaiDiagnosticURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[redacted URL]"
	}
	if u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		return raw
	}
	u.User = nil
	if u.RawQuery != "" {
		u.RawQuery = "redacted"
	}
	if u.Fragment != "" {
		u.Fragment = "redacted"
	}
	return u.String()
}

// zaiPendingFlowSecrets records the complete authorization URL and each of its
// components. The URL is intentionally shown by the browser handoff, but its
// state/query credentials must never be repeated in an error or log.
func zaiPendingFlowSecrets(init *zaiPendingFlow) []string {
	if init == nil {
		return nil
	}
	secrets := []string{init.AuthorizeURL, init.FlowID}
	u, err := url.Parse(init.AuthorizeURL)
	if err != nil {
		return secrets
	}
	if u.User != nil {
		secrets = append(secrets, u.User.Username())
		if password, ok := u.User.Password(); ok {
			secrets = append(secrets, password)
		}
	}
	for _, values := range u.Query() {
		secrets = append(secrets, values...)
	}
	return secrets
}
