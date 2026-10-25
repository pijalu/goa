// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package startplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testEnvelope is the balance payload of a healthy Start Plan account: one
// active plan, one bucket granting two models.
func testEnvelope(serverTime float64) string {
	return fmt.Sprintf(`{
		"code": 0,
		"msg": "ok",
		"data": {
			"server_time": %f,
			"plans": [{"plan_id": "start", "user_plan_id": "up1", "status": "active"}],
			"balances": [
				{
					"bucket_id": "b1", "user_plan_id": "up1", "plan_id": "start",
					"capabilities": ["model:GLM-5.3-Flash", "model:glm-5.2"],
					"remaining_units": 100
				},
				{
					"bucket_id": "b2", "plan_id": "other",
					"capabilities": ["model:glm-5-turbo"]
				}
			]
		}
	}`, serverTime)
}

// newBalanceServer spins an httptest server returning body with the given
// status, counting requests and capturing the last request.
type captureServer struct {
	srv     *httptest.Server
	calls   int32
	lastURL *url.URL
	lastReq *http.Request
	body    string
	status  int
	gate    chan struct{} // optional: blocks responses until closed
}

func newBalanceServer(t *testing.T, status int, body string) *captureServer {
	t.Helper()
	cs := &captureServer{body: body, status: status}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cs.calls, 1)
		cs.lastURL = r.URL
		cs.lastReq = r
		if cs.gate != nil {
			<-cs.gate
		}
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(cs.srv.Close)
	t.Setenv("GOA_ZCODE_API_URL", cs.srv.URL)
	return cs
}

func TestFetchBalance_SendsRequiredHeaders(t *testing.T) {
	isolateDeviceMid(t)
	t.Setenv("GOA_ZCODE_APP_VERSION", "test-9.9")
	cs := newBalanceServer(t, http.StatusOK, testEnvelope(1000))

	c := &Client{}
	snap, err := c.FetchBalance(context.Background(), "jwt-abc")
	if err != nil {
		t.Fatalf("FetchBalance: %v", err)
	}
	if snap.State != StateAvailable {
		t.Fatalf("state = %s (%s), want available; models=%v", snap.State, snap.UnavailableReason, snap.Models)
	}
	if got := cs.lastReq.Header.Get("Authorization"); got != "Bearer jwt-abc" {
		t.Errorf("Authorization = %q, want bearer zcode JWT", got)
	}
	if got := cs.lastReq.Header.Get("X-Device-Mid"); got == "" {
		t.Error("X-Device-Mid missing — the backend rejects headerless requests with 400 code 3001")
	}
	if !strings.HasSuffix(cs.lastURL.Path, "/zcode-plan/billing/balance") {
		t.Errorf("balance path = %q, want .../zcode-plan/billing/balance", cs.lastURL.Path)
	}
	if v := cs.lastURL.Query().Get("app_version"); v != "test-9.9" {
		t.Errorf("app_version = %q, want test-9.9", v)
	}
	want := []string{"GLM-5.3-Flash", "glm-5.2", "glm-5-turbo"}
	if len(snap.Models) != len(want) {
		t.Fatalf("models = %v, want %v", snap.Models, want)
	}
	for i := range want {
		if snap.Models[i] != want[i] {
			t.Errorf("models[%d] = %q, want %q", i, snap.Models[i], want[i])
		}
	}
}

func TestFetchBalance_EmptyJWTSkipsNetwork(t *testing.T) {
	cs := newBalanceServer(t, http.StatusOK, testEnvelope(1000))
	c := &Client{}
	snap, err := c.FetchBalance(context.Background(), "   ")
	if err != nil {
		t.Fatalf("FetchBalance: %v", err)
	}
	if snap.State != StateUnavailable || snap.UnavailableReason != ReasonNotAuthenticated {
		t.Fatalf("snapshot = %+v, want not_authenticated", snap)
	}
	if n := atomic.LoadInt32(&cs.calls); n != 0 {
		t.Errorf("server hit %d times without a credential", n)
	}
}

func TestFetchBalance_AuthFailedOn401(t *testing.T) {
	cs := newBalanceServer(t, http.StatusUnauthorized, `{"error":"denied"}`)
	c := &Client{}
	snap, err := c.FetchBalance(context.Background(), "jwt-abc")
	if err != nil {
		t.Fatalf("FetchBalance: %v", err)
	}
	if snap.State != StateUnavailable || snap.UnavailableReason != ReasonAuthFailed {
		t.Fatalf("snapshot = %+v, want auth_failed", snap)
	}
	if n := atomic.LoadInt32(&cs.calls); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
}

func TestFetchBalance_UnknownOnServerError(t *testing.T) {
	newBalanceServer(t, http.StatusInternalServerError, `upstream exploded`)
	c := &Client{}
	snap, err := c.FetchBalance(context.Background(), "jwt-abc")
	if err != nil {
		t.Fatalf("FetchBalance: %v", err)
	}
	// An outage must look unknown, never like a local entitlement block.
	if snap.State != StateUnknown {
		t.Fatalf("state = %s, want unknown", snap.State)
	}
}

func TestFetchBalance_CacheOnlyForAvailable(t *testing.T) {
	cs := newBalanceServer(t, http.StatusOK, testEnvelope(1000))
	now := time.Unix(1_000_000, 0)
	c := &Client{Now: func() time.Time { return now }}

	for i := 0; i < 3; i++ {
		if _, err := c.FetchBalance(context.Background(), "jwt"); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if n := atomic.LoadInt32(&cs.calls); n != 1 {
		t.Fatalf("available snapshots must satisfy the TTL: calls = %d, want 1", n)
	}

	now = now.Add(2 * balanceCacheTTL)
	if _, err := c.FetchBalance(context.Background(), "jwt"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&cs.calls); n != 2 {
		t.Errorf("expired TTL must refetch: calls = %d, want 2", n)
	}

	// Different credential → different cache key.
	if _, err := c.FetchBalance(context.Background(), "other-jwt"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&cs.calls); n != 3 {
		t.Errorf("a new JWT must bypass the old cache entry: calls = %d, want 3", n)
	}
}

func TestFetchBalance_PendingNotCached(t *testing.T) {
	body := `{"code":0,"data":{"plans":[{"plan_id":"p","status":"active",
		"entitlements":[{"entitlement_id":"e","effective_at":9999999999}]}]}}`
	cs := newBalanceServer(t, http.StatusOK, body)
	c := &Client{}
	for i := 0; i < 2; i++ {
		snap, err := c.FetchBalance(context.Background(), "jwt")
		if err != nil {
			t.Fatal(err)
		}
		if snap.State != StatePending {
			t.Fatalf("fetch %d: state = %s, want pending", i, snap.State)
		}
	}
	if n := atomic.LoadInt32(&cs.calls); n != 2 {
		t.Errorf("non-available states must re-check: calls = %d, want 2", n)
	}
}

func TestFetchBalance_SingleFlight(t *testing.T) {
	isolateDeviceMid(t)
	cs := newBalanceServer(t, http.StatusOK, testEnvelope(1000))
	cs.gate = make(chan struct{})

	c := &Client{}
	var wg sync.WaitGroup
	firstEntered := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Signal once the request reached the handler (inside the gate).
		go func() {
			for atomic.LoadInt32(&cs.calls) == 0 {
				time.Sleep(time.Millisecond)
			}
			close(firstEntered)
		}()
		_, _ = c.FetchBalance(context.Background(), "jwt")
	}()

	<-firstEntered
	var second *Snapshot
	var secondErr error
	done := make(chan struct{})
	go func() {
		second, secondErr = c.FetchBalance(context.Background(), "jwt")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("second caller must share the in-flight request, not start its own")
	default:
	}
	close(cs.gate)
	wg.Wait()
	<-done
	if secondErr != nil {
		t.Fatalf("second fetch: %v", secondErr)
	}
	if second == nil || second.State != StateAvailable {
		t.Fatalf("second snapshot = %+v, want available", second)
	}
	if n := atomic.LoadInt32(&cs.calls); n != 1 {
		t.Errorf("calls = %d, want exactly 1 for a concurrent burst", n)
	}
}

func TestNormalizeExpiry_ServerClockExpiresPlan(t *testing.T) {
	// Server says "now" is 2000; the plan ended at 1000 — a skewed local
	// clock at 500 must NOT keep the plan alive.
	env := &Envelope{
		ResponseTimeSeconds: 2000,
	}
	env.Data.Plans = []Plan{{PlanID: "start", UserPlanID: "up1", Status: "active", EndsAt: float64(1000)}}
	env.Data.Balances = []Balance{
		{BucketID: "owned", UserPlanID: "up1", Capabilities: []string{"model:glm-5.3-flash"}},
		{BucketID: "orphan", Capabilities: []string{"model:glm-5.2"}},
	}
	NormalizeExpiry(env, time.Unix(500, 0))

	if got := env.Data.Plans[0].Status; got != "expired" {
		t.Fatalf("status = %q, want expired per the server clock", got)
	}
	var kept []string
	for _, b := range env.Data.Balances {
		kept = append(kept, b.BucketID)
	}
	if len(kept) != 1 || kept[0] != "orphan" {
		t.Errorf("kept buckets = %v, want only the unattributed one", kept)
	}
}

func TestNormalizeExpiry_SharedPlanIDKeepsBucket(t *testing.T) {
	// Two plan instances share plan_id "start" (renewal); the bucket is
	// attributed by plan_id. Expiring one instance must not drop the
	// other's quota.
	env := &Envelope{ResponseTimeSeconds: 2000}
	env.Data.Plans = []Plan{
		{PlanID: "start", UserPlanID: "old", Status: "active", EndsAt: float64(1000)},
		{PlanID: "start", UserPlanID: "new", Status: "active", EndsAt: float64(9000)},
	}
	env.Data.Balances = []Balance{{BucketID: "shared", PlanID: "start"}}
	NormalizeExpiry(env, time.Unix(2000, 0))
	if len(env.Data.Balances) != 1 {
		t.Errorf("bucket dropped, want kept: %+v", env.Data.Balances)
	}
	if env.Data.Plans[0].Status != "expired" || env.Data.Plans[1].Status != "active" {
		t.Errorf("plan statuses = %q, %q; want expired+active", env.Data.Plans[0].Status, env.Data.Plans[1].Status)
	}
}

func TestClassify(t *testing.T) {
	future := float64(time.Now().Add(time.Hour).Unix())
	tests := []struct {
		name   string
		env    *Envelope
		state  string
		reason string
		models int
		pendAt float64
	}{
		{
			name:  "nil envelope is unknown, never a block",
			env:   nil,
			state: StateUnknown,
		},
		{
			name:   "healthy account",
			env:    mustJSONEnv(t, testEnvelope(0)),
			state:  StateAvailable,
			models: 3,
		},
		{
			name:   "success=false is auth failure",
			env:    mustJSONEnv(t, `{"success":false,"code":0,"msg":"no"}`),
			state:  StateUnavailable,
			reason: ReasonAuthFailed,
		},
		{
			name:   "business error code is auth failure",
			env:    mustJSONEnv(t, `{"code":401,"msg":"token expired"}`),
			state:  StateUnavailable,
			reason: ReasonAuthFailed,
		},
		{
			name:  "code 200 counts as success",
			env:   mustJSONEnv(t, `{"code":200,"data":{"plans":[{"plan_id":"p","status":"active"}]}}`),
			state: StateAvailable,
		},
		{
			name:   "no plans is not_entitled",
			env:    mustJSONEnv(t, `{"code":0,"data":{"plans":[],"balances":[]}}`),
			state:  StateUnavailable,
			reason: ReasonNotEntitled,
		},
		{
			name: "future entitlement is pending",
			env: mustJSONEnv(t, fmt.Sprintf(`{"code":0,"data":{"plans":[
				{"plan_id":"p","status":"active","entitlements":[{"effective_at":%f}]}]}}`, future)),
			state:  StatePending,
			pendAt: future,
		},
		{
			name: "effective_at=0 means immediate, not missing",
			env: mustJSONEnv(t, `{"code":0,"data":{"plans":[
				{"plan_id":"p","status":"active","entitlements":[{"effective_at":0}]}]}}`),
			state: StateAvailable,
		},
		{
			name: "starts_at fallback for pending",
			env: mustJSONEnv(t, fmt.Sprintf(`{"code":0,"data":{"plans":[
				{"plan_id":"p","status":"active","starts_at":%f}]}}`, future)),
			state:  StatePending,
			pendAt: future,
		},
		{
			name: "bucket without model caps falls back to show_name",
			env: mustJSONEnv(t, `{"code":0,"data":{
				"plans":[{"plan_id":"p","status":"active"}],
				"balances":[{"bucket_id":"b","show_name":"GLM-5.3-Flash"}]}}`),
			state:  StateAvailable,
			models: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := Classify(tt.env)
			if snap.State != tt.state {
				t.Fatalf("state = %s (%s), want %s", snap.State, snap.UnavailableReason, tt.state)
			}
			if snap.UnavailableReason != tt.reason {
				t.Errorf("reason = %q, want %q", snap.UnavailableReason, tt.reason)
			}
			if tt.models > 0 && len(snap.Models) != tt.models {
				t.Errorf("models = %v, want %d entries", snap.Models, tt.models)
			}
			if tt.state == StatePending && snap.EffectiveAtSeconds != tt.pendAt {
				t.Errorf("pending at %f, want %f", snap.EffectiveAtSeconds, tt.pendAt)
			}
		})
	}
}

func mustJSONEnv(t *testing.T, body string) *Envelope {
	t.Helper()
	env := &Envelope{}
	if err := json.Unmarshal([]byte(body), env); err != nil {
		t.Fatalf("test envelope invalid: %v", err)
	}
	return env
}

func TestModelIDs(t *testing.T) {
	env := &Envelope{}
	env.Data.Balances = []Balance{
		{Capabilities: []string{"model:GLM-5.3-Flash", "web_search", "model:glm-5.3-flash", " "}},
		{ShowName: "GLM-5.2", Capabilities: nil},
		{Capabilities: []string{"model:GLM-5.2"}}, // duplicate, different case
		{ShowName: "  "},
	}
	got := ModelIDs(env)
	want := []string{"GLM-5.3-Flash", "GLM-5.2"}
	if len(got) != len(want) {
		t.Fatalf("ModelIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ModelIDs[%d] = %q, want %q (first occurrence, case-insensitive dedupe)", i, got[i], want[i])
		}
	}
	if ModelIDs(nil) != nil {
		t.Error("ModelIDs(nil) must be nil")
	}
}

func TestRequireModels(t *testing.T) {
	avail := &Snapshot{State: StateAvailable, Models: []string{"glm-5.3-flash"}}
	pending := &Snapshot{State: StatePending, EffectiveAtSeconds: 123}
	tests := []struct {
		name    string
		snap    *Snapshot
		model   string
		wantErr bool
	}{
		{"nil snapshot allows", nil, "glm-5.3-flash", false},
		{"listed model allows", avail, "glm-5.3-flash", false},
		{"case-insensitive match", avail, "GLM-5.3-FLASH", false},
		{"unlisted model blocked", avail, "glm-5", true},
		{"empty whitelist allows (backend stays authority)", &Snapshot{State: StateAvailable}, "anything", false},
		{"pending blocks", pending, "glm-5.3-flash", true},
		{"unknown allows", &Snapshot{State: StateUnknown}, "glm-5.3-flash", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.snap.RequireModels(tt.model)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RequireModels = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrNoEntitlement) {
				t.Errorf("error %v does not match ErrNoEntitlement", err)
			}
		})
	}
}
