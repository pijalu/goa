// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package startplan

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Business error codes the zcode-plan backend returns inside its envelope.
// The adapter layer must keep 1005 non-retryable (a quota-exhausted state,
// NOT a system-busy state) and treat the concurrency codes as transient:
// they feed the retry policy, never fail the turn outright (ZCode parity:
// docs/start-plan-provider-business-errors.md).
const (
	CodeQuotaExhausted = 1005 // daily quota gone; resets on the bucket clock
	CodeSecurityVerify = 3007 // security verification rejected
	CodeBusy           = 3008 // concurrent request limit reached
	CodeModelBusy      = 3009 // model concurrent limit reached
	CodeModelBusyAlt   = 3010 // model concurrent limit reached
	CodeParameterError = 3001 // e.g. missing X-Device-Mid — a LOCAL defect, never an outage
)

// BusinessError is a plan-envelope failure with its business code attached.
// The error text is user-facing and deliberately free of server internals.
// Cause keeps the transport-level error so errors.Is/As chains still reach
// it after the classification layer swaps ctx.Err.
type BusinessError struct {
	Code  int
	Msg   string // server message, kept for diagnostics only
	Cause error  // underlying transport/parse error, may be nil
}

// Error implements error.
func (e *BusinessError) Error() string {
	if d := Describe(e.Code); d != "" {
		return d
	}
	if e.Msg != "" {
		return fmt.Sprintf("start plan error %d: %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("start plan error %d", e.Code)
}

// Unwrap exposes the underlying transport error, if any, so
// errors.Is/As keep working through the classification wrap.
func (e *BusinessError) Unwrap() error { return e.Cause }

// IsRetryable reports whether the failure is transient. The busy family and
// rate-limit-shaped outcomes retry with the normal bounded backoff; quota
// exhaustion and security verification do not — retrying them cannot succeed
// within the turn and only burns the budget.
func (e *BusinessError) IsRetryable() bool {
	return IsRetryableCode(e.Code)
}

// IsRetryableCode reports retryability for a raw business code.
func IsRetryableCode(code int) bool {
	switch code {
	case CodeBusy, CodeModelBusy, CodeModelBusyAlt:
		return true
	default:
		return false
	}
}

// Describe maps a business code onto the user-facing sentence. Unknown codes
// return "" so the caller falls back to the server message instead of
// inventing a meaning (ZCode parity: only productized codes have stable copy).
func Describe(code int) string {
	switch code {
	case CodeQuotaExhausted:
		return "Start Plan quota is exhausted for today; it resets on the quota bucket's clock, or upgrade to a Coding Plan to continue now"
	case CodeSecurityVerify:
		return "Start Plan request was rejected by security verification"
	case CodeBusy, CodeModelBusy, CodeModelBusyAlt:
		return "Start Plan is busy (concurrent request limit); retrying"
	case CodeParameterError:
		return "Start Plan request was rejected as malformed (missing device identity)"
	default:
		return ""
	}
}

// ParseBusinessCode extracts the plan envelope's business code from an error
// body. The backend sends {"code":<int|string>,"msg":...}; the code may also
// be nested under error.code in OpenAI-shaped bodies. Returns ok=false when
// the body carries no plausible business code — a plain OpenAI error must not
// be misread as a plan failure.
func ParseBusinessCode(body string) (code int, msg string, ok bool) {
	if strings.TrimSpace(body) == "" {
		return 0, "", false
	}
	var probe struct {
		Code  any    `json:"code"`
		Msg   string `json:"msg"`
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		return 0, "", false
	}
	if v, ok := coerceCode(probe.Code); ok {
		return v, probe.Msg, true
	}
	if v, ok := coerceCode(probe.Error.Code); ok {
		return v, probe.Error.Message, true
	}
	return 0, "", false
}

// coerceCode accepts int and numeric-string codes; anything else (including
// OpenAI's string error types like "invalid_request_error") is not a plan
// business code.
func coerceCode(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		if t == float64(int(t)) {
			return int(t), true
		}
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n, true
		}
	}
	return 0, false
}

// WrapBusinessError builds the *BusinessError for a parsed code, preserving
// the transport error for errors.Is/As chains.
func WrapBusinessError(cause error, code int, msg string) *BusinessError {
	return &BusinessError{Code: code, Msg: strings.TrimSpace(msg), Cause: cause}
}
