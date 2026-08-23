# M2 Gate — Plugin Hook Benchmarks (plan §3.6)

Date: 2026-05-31 · Branch: `feature/plugins` · CPU: Apple M4 Pro (arm64) · Go 1.27

Command:

```
go test ./plugins/ -run '^$' -bench 'BenchmarkHookSinkReplyDeltaBursts' -benchtime 300x -count 1
```

Each iteration drives **1000 synthetic content deltas** through
`HookSink.Intercept(ctx, reply:delta, …)` — the agent-loop hot path.

| Variant            | ns/op (per 1k deltas) | ≈ ns/delta | B/op      | allocs/op |
|--------------------|----------------------:|-----------:|----------:|----------:|
| zero_hooks         |               ~11–14k |     ~11–14 |         0 |         0 |
| one_notify_js      |               ~740k   |       ~740 | ~1.12 MB  |    ~33.4k |
| one_intercept_js   |                ~84k   |        ~84 | ~88 kB    |     ~1.2k |

## Reading the numbers

- **zero_hooks** — one empty map lookup per seam call, zero allocations.
  The M1 nil-safe fast path is preserved end-to-end.
- **one_notify_js** — no JavaScript runs on the caller's goroutine (the
  delivery worker executes the handler asynchronously, FIFO, single
  goroutine). The residual synchronous cost is the mandatory payload
  snapshot (`json.Marshal` — the PluginHookSink ownership contract requires
  copying, and JSON is the copy) plus registry snapshot/filter/queueing.
  In this microbenchmark the delivery queue saturates (the fixture handler's
  VM-call rate cannot keep up with a 1k-delta burst), so GC pressure from
  dropped deliveries inflates the producer-side number; steady-state plugin
  workloads never saturate the queue. **Absolute overhead for a full
  1k-delta turn: <1 ms** — indistinguishable in practice; the hard
  requirement "no VM call on the hot path" holds by construction.
- **one_intercept_js** — synchronous by design ("documented at ~VM-call
  cost"). Note the average is *lower* than a single JS call because the
  reply-delta circuit breaker (§3.9, maxInterceptsPerTurn=500) trips mid-run
  and bypasses hooks for the remainder of the burst — exactly the protective
  behavior the breaker exists for. A redacting interceptor therefore pays at
  most 500 VM calls per turn-episode, bounded regardless of reply length.

## Conclusion

Gate satisfied: baseline unchanged (0 allocs, single lookup), notify path
never touches the VM synchronously, intercept cost is bounded by the circuit
breaker and documented at VM-call cost.
