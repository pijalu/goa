// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package startplan implements the ZCode-parity Start Plan surface: the
// zcode-plan runtime identity helpers, the billing/balance entitlement
// client, the persistent deviceMid, and the plan-specific business-error
// classification.
//
// # WHY THIS EXISTS
//
// Start Plan is the free GLM tier served by zcode's own plan backend. It is
// NOT a plain z.ai API-key provider:
//
//   - the chat runtime is {origin}/api/v1/zcode-plan (OpenAI-compatible),
//     authenticated with the ZCODE JWT minted by the z.ai / BigModel OAuth
//     login — the same credential the Coding Plan reset surface uses;
//   - the backend requires X-Device-Mid on plan requests (a missing header
//     yields 400 {"code":3001,"msg":"parameter error"}, which must never be
//     read as a backend outage);
//   - entitlement is dynamic: billing/balance returns the active plans, the
//     quota buckets, and — via bucket capabilities — the servable model ids.
//
// The billing/current endpoint is deprecated by the backend; balance is the
// only authoritative snapshot and every consumer must derive everything from
// one balance response.
package startplan

import (
	"net/url"
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// DefaultOrigin is the zcode plan backend origin. GOA_ZCODE_API_URL overrides
// it so tests and self-hosted mirrors can retarget without a rebuild.
const DefaultOrigin = "https://zcode.z.ai/api/v1"

// Family is the credential family a Start Plan identity belongs to. The zcode
// JWT is stored by /login under the family key ("zai" / "bigmodel"), and the
// plan backend validates the token against that family's account — a zai JWT
// never satisfies a bigmodel request or vice versa.
type Family string

const (
	FamilyZai      Family = "zai"
	FamilyBigModel Family = "bigmodel"
)

// LoginHint is the goa command that mints the family's zcode JWT. It is
// surfaced in MissingCredentialError sources so the fix is one paste away
// instead of a docs detour.
func (f Family) LoginHint() string {
	return "/login:" + string(f) + ":oauth"
}

// FamilyOf returns the credential family for a Start Plan provider identity.
// The second result is false for non-Start-Plan providers.
func FamilyOf(p schema.Provider) (Family, bool) {
	switch p {
	case schema.ProviderZaiStartPlan:
		return FamilyZai, true
	case schema.ProviderBigModelStartPlan:
		return FamilyBigModel, true
	default:
		return "", false
	}
}

// Origin returns the plan backend origin, honoring GOA_ZCODE_API_URL.
func Origin() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("GOA_ZCODE_API_URL")), "/"); v != "" {
		return v
	}
	return DefaultOrigin
}

// BalanceURL builds the billing/balance request URL. app_version is
// mandatory: the backend gates plan capabilities by caller version, and a
// faked version would observe a different capability set than the binary
// actually runs (ZCode sends its real ZCODE_VERSION for the same reason).
// GOA_ZCODE_APP_VERSION overrides the default for staging verification.
func BalanceURL(appVersion string) string {
	v := strings.TrimSpace(appVersion)
	if v == "" {
		v = AppVersion()
	}
	q := url.Values{}
	q.Set("app_version", v)
	return Origin() + "/zcode-plan/billing/balance?" + q.Encode()
}

// AppVersion is the caller identity sent as app_version. It defaults to the
// running goa version; GOA_ZCODE_APP_VERSION overrides it for backends that
// gate plan capabilities to specific client versions.
func AppVersion() string {
	if v := strings.TrimSpace(os.Getenv("GOA_ZCODE_APP_VERSION")); v != "" {
		return v
	}
	return goaVersion
}

// goaVersion mirrors the userAgent() placeholder in the provider hooks: the
// binary carries no build-stamped version today, so the honest identity is
// the placeholder rather than a fabricated release number.
const goaVersion = "0.0.0"
