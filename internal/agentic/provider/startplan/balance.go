// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package startplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Balance request/response contract (ZCode parity, docs/zai-start-plan-provider.md):
//
//	GET {origin}/zcode-plan/billing/balance?app_version=<version>
//	Authorization: Bearer <zcode JWT>
//	X-Device-Mid: <deviceMid>            # required; missing -> 400 code 3001
//
// data.plans[] carries the active/pending plan instances; data.balances[]
// carries one quota bucket per plan instance, each with expires_at (the
// bucket's own reset time — never a plan-level renew time) and capabilities
// ("model:<id>" entries) that form the servable model whitelist. billing/current
// is deprecated and must not be called.

const (
	balanceTimeout   = 15 * time.Second
	balanceCacheTTL  = 60 * time.Second // matches ZCode's access-refresh throttle
	balanceBodyLimit = 1 << 20
)

// Plan is one entry of data.plans[].
type Plan struct {
	UserPlanID string `json:"user_plan_id,omitempty"`
	PlanID     string `json:"plan_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Status     string `json:"status,omitempty"`
	StartsAt   any    `json:"starts_at,omitempty"`
	EndsAt     any    `json:"ends_at,omitempty"`
	// Entitlements carries per-entitlement effective times; a plan whose
	// entitlements all start in the future is pending, not unavailable.
	Entitlements []PlanEntitlement `json:"entitlements,omitempty"`
}

// PlanEntitlement is one entitlement row of a plan.
type PlanEntitlement struct {
	EntitlementID string `json:"entitlement_id,omitempty"`
	ShowName      string `json:"show_name,omitempty"`
	Period        string `json:"period,omitempty"`
	EffectiveAt   any    `json:"effective_at,omitempty"`
}

// Balance is one quota bucket of data.balances[].
type Balance struct {
	BucketID       string   `json:"bucket_id,omitempty"`
	UserPlanID     string   `json:"user_plan_id,omitempty"`
	PlanID         string   `json:"plan_id,omitempty"`
	EntitlementID  string   `json:"entitlement_id,omitempty"`
	ShowName       string   `json:"show_name,omitempty"`
	Meter          string   `json:"meter,omitempty"`
	UnitType       string   `json:"unit_type,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	TotalUnits     any      `json:"total_units,omitempty"`
	UsedUnits      any      `json:"used_units,omitempty"`
	ReservedUnits  any      `json:"reserved_units,omitempty"`
	RemainingUnits any      `json:"remaining_units,omitempty"`
	AvailableUnits any      `json:"available_units,omitempty"`
	PeriodStart    any      `json:"period_start,omitempty"`
	PeriodEnd      any      `json:"period_end,omitempty"`
	ExpiresAt      any      `json:"expires_at,omitempty"`
}

// Envelope is the billing/balance response.
type Envelope struct {
	Code    *int   `json:"code"`
	Success *bool  `json:"success"`
	Msg     string `json:"msg"`
	Data    struct {
		ServerTime any       `json:"server_time,omitempty"`
		Plans      []Plan    `json:"plans,omitempty"`
		Balances   []Balance `json:"balances,omitempty"`
	} `json:"data"`
	// ResponseTimeSeconds pairs the payload with the HTTP Date header, which
	// anchors expiry decisions to the SERVER clock. data.server_time is the
	// fallback; local time is last — a skewed client clock must not extend a
	// dead plan's entitlement.
	ResponseTimeSeconds float64 `json:"-"`
}

// Snapshot is the classified result of one balance fetch.
type Snapshot struct {
	// State: "available", "pending", "unavailable", "unknown".
	State string
	// UnavailableReason is set for State "unavailable": not_authenticated,
	// auth_failed, not_entitled.
	UnavailableReason string
	// EffectiveAtSeconds is set for State "pending" (Unix seconds).
	EffectiveAtSeconds float64
	// Models lists the servable model ids in first-occurrence order.
	Models []string
	// Envelope is the raw (already expiry-normalized) payload.
	Envelope *Envelope
}

// Snapshot states.
const (
	StateAvailable   = "available"
	StatePending     = "pending"
	StateUnavailable = "unavailable"
	StateUnknown     = "unknown"
)

// Unavailable reasons.
const (
	ReasonNotAuthenticated = "not_authenticated"
	ReasonAuthFailed       = "auth_failed"
	ReasonNotEntitled      = "not_entitled"
)

// Client fetches and classifies billing/balance snapshots. The zero value is
// usable; Doer and Now are injectable for tests.
type Client struct {
	// Doer performs the HTTP request. Nil uses a default transport.
	Doer func(*http.Request) (*http.Response, error)
	// Now overrides the wall clock (tests only).
	Now func() time.Time

	mu       sync.Mutex
	cacheKey string
	cacheAt  time.Time
	cache    *Snapshot
	inflight *cacheEntry
}

type cacheEntry struct {
	done chan struct{}
	snap *Snapshot
	err  error
}

// Authorization builds the Bearer header value for a zcode JWT.
func Authorization(jwt string) string {
	return "Bearer " + strings.TrimSpace(jwt)
}

// FetchBalance returns a cached snapshot younger than the TTL, or performs one
// balance request. Concurrent callers share the in-flight request, so a burst
// of consumers (auth check, status, error path) produces exactly one call.
//
// An empty jwt yields a not_authenticated snapshot without a network call.
// Only AVAILABLE snapshots satisfy the TTL; other states and failures are
// re-checked on the next call.
func (c *Client) FetchBalance(ctx context.Context, jwt string) (*Snapshot, error) {
	jwt = strings.TrimSpace(jwt)
	if jwt == "" {
		return &Snapshot{State: StateUnavailable, UnavailableReason: ReasonNotAuthenticated}, nil
	}
	key := Authorization(jwt)

	c.mu.Lock()
	if c.cache != nil && c.cacheKey == key && c.since(c.cacheAt) < balanceCacheTTL {
		snap := c.cache
		c.mu.Unlock()
		return snap, nil
	}
	if c.inflight != nil && c.cacheKey == key {
		e := c.inflight
		c.mu.Unlock()
		select {
		case <-e.done:
			return e.snap, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	e := &cacheEntry{done: make(chan struct{})}
	c.inflight = e
	c.cacheKey = key
	c.mu.Unlock()

	snap, err := c.fetch(ctx, jwt)
	c.mu.Lock()
	// Only successful AVAILABLE snapshots satisfy the TTL cache: a pending or
	// not_entitled answer must be re-checked on the next explicit refresh,
	// while still being shared with the current burst via the in-flight entry.
	if err == nil && snap != nil && snap.State == StateAvailable {
		c.cache = snap
		c.cacheAt = c.now()
	}
	c.inflight = nil
	c.mu.Unlock()
	e.snap, e.err = snap, err
	close(e.done)
	return snap, err
}

func (c *Client) fetch(ctx context.Context, jwt string) (*Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, balanceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BalanceURL(""), nil)
	if err != nil {
		return nil, fmt.Errorf("start plan balance: create request: %w", err)
	}
	req.Header.Set("Authorization", Authorization(jwt))
	if mid := DeviceMid(); mid != "" {
		req.Header.Set("X-Device-Mid", mid)
	}
	doer := c.Doer
	if doer == nil {
		doer = http.DefaultClient.Do
	}
	resp, err := doer(req)
	if err != nil {
		return nil, fmt.Errorf("start plan balance: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, balanceBodyLimit))

	var env Envelope
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The body may still carry the business envelope (code 3001 for a
		// missing device header); parse it for classification but the HTTP
		// status decides auth vs unknown.
		_ = json.Unmarshal(raw, &env)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return &Snapshot{State: StateUnavailable, UnavailableReason: ReasonAuthFailed}, nil
		}
		return &Snapshot{State: StateUnknown}, nil
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("start plan balance: invalid response: %w", err)
	}
	if d := resp.Header.Get("Date"); d != "" {
		if t, err := http.ParseTime(d); err == nil {
			env.ResponseTimeSeconds = float64(t.Unix())
		}
	}
	NormalizeExpiry(&env, c.now())
	return Classify(&env), nil
}

// now returns the injectable clock.
func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// since reports d - t honoring the injectable clock.
func (c *Client) since(t time.Time) time.Duration {
	return c.now().Sub(t)
}

// NormalizeExpiry rewrites the envelope's plan statuses against the server
// clock: an "active" plan whose ends_at has passed is expired, and quota
// buckets that belong ONLY to expired plans are dropped. Bucket ownership
// follows user_plan_id when both sides carry it, else plan_id. Unattributed
// buckets are KEPT: the backend contract promises attribution, and silently
// deleting a bucket that belongs to another active instance of the same
// product would hide quota the user owns (ZCode tripwire parity — goa logs
// the anomaly through the caller instead).
func NormalizeExpiry(env *Envelope, now time.Time) {
	if env == nil || env.Data.Plans == nil && env.Data.Balances == nil {
		return
	}
	nowSec := nowSeconds(env, now)
	env.Data.Plans = expireStalePlans(env.Data.Plans, nowSec)
	env.Data.Balances = dropExpiredOwnedBuckets(env.Data.Plans, env.Data.Balances)
}

// expireStalePlans rewrites statuses against the server clock: an "active"
// plan whose ends_at has passed becomes "expired". Returns a fresh slice —
// the caller's envelope is never mutated through a shared backing array.
func expireStalePlans(plans []Plan, nowSec float64) []Plan {
	out := make([]Plan, len(plans))
	copy(out, plans)
	for i := range out {
		p := &out[i]
		if !strings.EqualFold(strings.TrimSpace(p.Status), "active") {
			continue
		}
		end, ok := unixSeconds(p.EndsAt)
		if ok && end > 0 && end <= nowSec {
			p.Status = "expired"
		}
	}
	return out
}

// dropExpiredOwnedBuckets removes buckets owned ONLY by expired plans.
// Unattributed buckets are KEPT: the backend contract promises attribution,
// and silently deleting a bucket that belongs to another active instance of
// the same product would hide quota the user owns (ZCode tripwire parity —
// goa logs the anomaly through the caller instead).
func dropExpiredOwnedBuckets(plans []Plan, balances []Balance) []Balance {
	out := make([]Balance, 0, len(balances))
	for _, b := range balances {
		owners := owningPlans(plans, b)
		if len(owners) == 0 {
			out = append(out, b)
			continue
		}
		for _, p := range owners {
			if !strings.EqualFold(strings.TrimSpace(p.Status), "expired") {
				out = append(out, b)
				break
			}
		}
	}
	return out
}

// Classify derives the snapshot state from an (expiry-normalized) envelope.
// It never performs I/O: unknown shapes degrade to unknown, never to
// "unauthenticated" or "not entitled" — those need positive evidence.
func Classify(env *Envelope) *Snapshot {
	if env == nil {
		return &Snapshot{State: StateUnknown}
	}
	if !envelopeOK(env) {
		return &Snapshot{State: StateUnavailable, UnavailableReason: ReasonAuthFailed, Envelope: env}
	}
	active := activePlans(env)
	if len(active) == 0 {
		return &Snapshot{State: StateUnavailable, UnavailableReason: ReasonNotEntitled, Envelope: env}
	}
	models := ModelIDs(env)
	if len(models) == 0 {
		// Every entitlement effective time strictly in the future → pending.
		// Missing times are not fabricated into a date (ZCode parity): a plan
		// with no model and no effective time is available-but-empty, and the
		// runtime's own model error will explain what happened.
		nowSec := nowSeconds(env, time.Now())
		if eff, ok := futureEffectiveAt(env, nowSec); ok {
			return &Snapshot{State: StatePending, EffectiveAtSeconds: eff, Envelope: env}
		}
		return &Snapshot{State: StateAvailable, Models: nil, Envelope: env}
	}
	return &Snapshot{State: StateAvailable, Models: models, Envelope: env}
}

// ModelIDs extracts the servable model whitelist from bucket capabilities.
// Entries are "model:<id>"; a bucket without model capabilities falls back to
// its show_name (the backend uses it as the single-model bucket label, ZCode
// parity). IDs are trimmed, de-duplicated case-insensitively, and keep
// first-occurrence order.
func ModelIDs(env *Envelope) []string {
	if env == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		k := strings.ToLower(v)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, v)
	}
	for _, b := range env.Data.Balances {
		fromCaps := false
		for _, cap := range b.Capabilities {
			c := strings.TrimSpace(cap)
			if !strings.HasPrefix(strings.ToLower(c), "model:") {
				continue
			}
			fromCaps = true
			add(strings.TrimSpace(c[len("model:"):]))
		}
		if !fromCaps {
			add(b.ShowName)
		}
	}
	return out
}

// envelopeOK applies the plan backend's success convention: HTTP-style code
// 200 counts, the broker's 0 counts, an omitted code counts; success=false
// never counts.
func envelopeOK(env *Envelope) bool {
	if env.Success != nil && !*env.Success {
		return false
	}
	return env.Code == nil || *env.Code == 0 || *env.Code == 200
}

func activePlans(env *Envelope) []Plan {
	var out []Plan
	for _, p := range env.Data.Plans {
		if strings.EqualFold(strings.TrimSpace(p.Status), "active") {
			out = append(out, p)
		}
	}
	return out
}

// futureEffectiveAt returns the earliest entitlement effective time when ALL
// entitlement times (falling back to the plan's starts_at) are strictly in the
// future.
func futureEffectiveAt(env *Envelope, nowSec float64) (float64, bool) {
	min, found := minEntitlementTime(env)
	if !found || min <= nowSec {
		return 0, false
	}
	return min, true
}

// minEntitlementTime scans the active plans' entitlement effective times
// (falling back to starts_at for plans without entitlements) and returns the
// earliest one.
func minEntitlementTime(env *Envelope) (float64, bool) {
	var min float64
	found := false
	for _, p := range activePlans(env) {
		for _, t := range planEffectiveTimes(p) {
			if !found || t < min {
				min = t
				found = true
			}
		}
	}
	return min, found
}

// planEffectiveTimes collects one plan's entitlement effective times, falling
// back to the plan's starts_at when it carries no entitlements.
func planEffectiveTimes(p Plan) []float64 {
	if len(p.Entitlements) == 0 {
		if v, ok := unixSeconds(p.StartsAt); ok {
			return []float64{v}
		}
		return nil
	}
	times := make([]float64, 0, len(p.Entitlements))
	for _, e := range p.Entitlements {
		if v, ok := unixSeconds(e.EffectiveAt); ok {
			times = append(times, v)
		}
	}
	return times
}

// owningPlans returns the plans a bucket belongs to, matching user_plan_id
// when both sides carry it (plan instances can repeat plan_id across renewals)
// and plan_id otherwise.
func owningPlans(plans []Plan, b Balance) []Plan {
	var out []Plan
	for _, p := range plans {
		if b.UserPlanID != "" && p.UserPlanID != "" {
			if p.UserPlanID == b.UserPlanID {
				out = append(out, p)
			}
			continue
		}
		if b.PlanID != "" && p.PlanID != "" && p.PlanID == b.PlanID {
			out = append(out, p)
		}
	}
	return out
}

// nowSeconds picks the decision clock: HTTP Date (paired with THIS response),
// then data.server_time, then the local clock.
func nowSeconds(env *Envelope, now time.Time) float64 {
	if env.ResponseTimeSeconds > 0 {
		return env.ResponseTimeSeconds
	}
	if v, ok := unixSeconds(env.Data.ServerTime); ok && v >= 0 {
		return v
	}
	return float64(now.Unix())
}

// unixSeconds coerces the backend's number-or-string Unix-second fields.
// The zero sentinel (effective_at = 0) means "effective immediately" and is
// reported as a valid 0, never as missing.
func unixSeconds(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// ErrNoEntitlement is returned by RequireModels when the snapshot cannot
// prove a usable plan. It is matched with errors.Is by the transport layer.
var ErrNoEntitlement = errors.New("start plan: no active entitlement")

// RequireModels gates a chat request on the entitlement snapshot: available
// plans must cover the requested model. A pending plan reports its effective
// time; an unknown snapshot degrades to "allow" — the plan backend remains
// the authority and will reject with its own business code, which the error
// classifier renders. Blocking a request because a status check failed would
// turn a backend outage into a local one.
func (s *Snapshot) RequireModels(modelID string) error {
	if s == nil {
		return nil
	}
	switch s.State {
	case StateAvailable:
		if len(s.Models) == 0 {
			return nil
		}
		for _, m := range s.Models {
			if strings.EqualFold(strings.TrimSpace(m), strings.TrimSpace(modelID)) {
				return nil
			}
		}
		return fmt.Errorf("%w: model %q is not part of the active Start Plan", ErrNoEntitlement, modelID)
	case StatePending:
		return fmt.Errorf("%w: Start Plan is pending until %s", ErrNoEntitlement,
			time.Unix(int64(s.EffectiveAtSeconds), 0).Format(time.RFC3339))
	default:
		return nil
	}
}
