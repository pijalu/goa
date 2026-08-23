# Bug 4: Streaming render CPU

Implemented fixed 30fps render ticker with atomic dirty flag. Streaming deltas
coalesce into one frame per ticker interval, bounding latency to one interval.
Idle paths no longer have a render request channel that can trigger empty
re-anchor frames; rendering occurs only when dirty (apart from active tool live
updates).

Validation: `go test ./...` passed. Regression test
`TestRenderLoop_CoalescesBurstOnFixedTicker` verifies burst coalescing and
content delivery.
