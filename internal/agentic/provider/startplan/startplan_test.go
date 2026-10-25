// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package startplan

import (
	"errors"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

func TestFamilyOf(t *testing.T) {
	tests := []struct {
		p      schema.Provider
		want   Family
		wantOK bool
	}{
		{schema.ProviderZaiStartPlan, FamilyZai, true},
		{schema.ProviderBigModelStartPlan, FamilyBigModel, true},
		{schema.ProviderZai, "", false},
		{schema.ProviderZaiApi, "", false},
		{schema.ProviderOpenAI, "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		f, ok := FamilyOf(tt.p)
		if ok != tt.wantOK || f != tt.want {
			t.Errorf("FamilyOf(%q) = (%q, %v), want (%q, %v)", tt.p, f, ok, tt.want, tt.wantOK)
		}
	}
}

func TestFamilyLoginHint(t *testing.T) {
	if got := FamilyZai.LoginHint(); got != "/login:zai:oauth" {
		t.Errorf("zai hint = %q", got)
	}
	if got := FamilyBigModel.LoginHint(); got != "/login:bigmodel:oauth" {
		t.Errorf("bigmodel hint = %q", got)
	}
}

func TestOriginDefaultAndOverride(t *testing.T) {
	t.Setenv("GOA_ZCODE_API_URL", "")
	if got := Origin(); got != DefaultOrigin {
		t.Errorf("Origin() = %q, want default %q", got, DefaultOrigin)
	}
	t.Setenv("GOA_ZCODE_API_URL", "http://127.0.0.1:1/api/v1/")
	// Trailing slash is trimmed so BalanceURL never produces "//".
	if got := Origin(); got != "http://127.0.0.1:1/api/v1" {
		t.Errorf("Origin() = %q, want trimmed override", got)
	}
}

func TestBalanceURL(t *testing.T) {
	t.Setenv("GOA_ZCODE_API_URL", "https://zcode.example/api/v1")
	t.Setenv("GOA_ZCODE_APP_VERSION", "9.9-test")

	u := BalanceURL("")
	if !strings.HasPrefix(u, "https://zcode.example/api/v1/zcode-plan/billing/balance?") {
		t.Fatalf("BalanceURL = %q", u)
	}
	if !strings.Contains(u, "app_version=9.9-test") {
		t.Errorf("app_version missing from %q", u)
	}

	// An explicit version wins over the env default.
	if u := BalanceURL("1.2.3"); !strings.Contains(u, "app_version=1.2.3") {
		t.Errorf("explicit app_version not honored: %q", u)
	}
}

func TestAppVersion(t *testing.T) {
	t.Setenv("GOA_ZCODE_APP_VERSION", "")
	if got := AppVersion(); got != "0.0.0" {
		t.Errorf("AppVersion() = %q, want the userAgent placeholder 0.0.0", got)
	}
	t.Setenv("GOA_ZCODE_APP_VERSION", "staging-42")
	if got := AppVersion(); got != "staging-42" {
		t.Errorf("AppVersion() = %q, want env override", got)
	}
}

func TestParseBusinessCode(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
		msg  string
		ok   bool
	}{
		{"empty", "", 0, "", false},
		{"blank", "   \n", 0, "", false},
		{"non-JSON", "upstream exploded", 0, "", false},
		{"plain int code", `{"code":1005,"msg":"quota gone"}`, 1005, "quota gone", true},
		{"string code", `{"code":"3008","msg":"busy"}`, 3008, "busy", true},
		{"nested error.code int", `{"error":{"code":3007,"message":"verify"}}`, 3007, "verify", true},
		{"nested error.code string", `{"error":{"code":"3010","message":"busy"}}`, 3010, "busy", true},
		{"openai string type is not a plan code", `{"error":{"code":"invalid_request_error"}}`, 0, "", false},
		{"float non-integer", `{"code":1.5}`, 0, "", false},
		{"no code", `{"data":{"plans":[]}}`, 0, "", false},
		{"code zero is a real code", `{"code":0}`, 0, "", true},
		{"msg empty", `{"code":3001}`, 3001, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg, ok := ParseBusinessCode(tt.body)
			if ok != tt.ok || code != tt.code || msg != tt.msg {
				t.Errorf("ParseBusinessCode(%s) = (%d, %q, %v), want (%d, %q, %v)",
					tt.body, code, msg, ok, tt.code, tt.msg, tt.ok)
			}
		})
	}
}

func TestIsRetryableCode(t *testing.T) {
	for _, code := range []int{CodeBusy, CodeModelBusy, CodeModelBusyAlt} {
		if !IsRetryableCode(code) {
			t.Errorf("code %d must be retryable (transient busy)", code)
		}
	}
	for _, code := range []int{CodeQuotaExhausted, CodeSecurityVerify, CodeParameterError, 9999, 0} {
		if IsRetryableCode(code) {
			t.Errorf("code %d must NOT be retryable", code)
		}
	}
}

func TestDescribe(t *testing.T) {
	for _, code := range []int{CodeQuotaExhausted, CodeSecurityVerify, CodeBusy, CodeModelBusy, CodeModelBusyAlt, CodeParameterError} {
		if Describe(code) == "" {
			t.Errorf("Describe(%d) empty; productized codes must have stable copy", code)
		}
	}
	if Describe(4242) != "" {
		t.Error("unknown codes must fall back to the server message, not invented copy")
	}
}

func TestBusinessErrorUnwrapChain(t *testing.T) {
	cause := errors.New("transport boom")
	be := WrapBusinessError(cause, CodeQuotaExhausted, "server said no")

	if !errors.Is(be, cause) {
		t.Error("errors.Is must reach the transport cause through BusinessError")
	}
	var got *BusinessError
	if !errors.As(cause, &got) { // negative control
		// expected: the bare cause is not a BusinessError
	} else {
		t.Error("bare cause must not match BusinessError")
	}
	wrapped := WrapBusinessError(cause, CodeBusy, "busy")
	if !errors.As(error(wrapped), &got) || got.Code != CodeBusy {
		t.Fatalf("errors.As = %+v, want code %d", got, CodeBusy)
	}
	if !got.IsRetryable() {
		t.Error("busy code must be retryable")
	}
	if !strings.Contains(be.Error(), "quota") {
		t.Errorf("Error() = %q, want the quota guidance copy", be.Error())
	}
}
