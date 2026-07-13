// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "context"

// ProgressFunc is invoked by a tool to report partial output while it is still
// running. The argument is the tool's output-so-far (a best-effort snapshot);
// the host renders it into the running tool widget so the user sees live
// progress instead of a frozen spinner ("nothing happens" syndrome).
type ProgressFunc func(partial string)

type progressKey struct{}

// WithProgress returns a context whose carried ProgressFunc is delivered to
// tools that opt into execution streaming. Tools retrieve it via
// ProgressFromContext. A nil f stored is equivalent to no progress support.
//
// This follows Open/Closed: the Tool/ContextTool contracts do not change;
// only tools that call ProgressFromContext participate, and they degrade
// gracefully (no progress) when no emitter is present.
func WithProgress(ctx context.Context, f ProgressFunc) context.Context {
	if f == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, f)
}

// ProgressFromContext returns the ProgressFunc injected by the host, or nil if
// none is present (tool runs without streaming). Tools must handle nil.
func ProgressFromContext(ctx context.Context) ProgressFunc {
	f, _ := ctx.Value(progressKey{}).(ProgressFunc)
	return f
}
