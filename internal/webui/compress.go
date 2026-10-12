// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// Compression for the web transport (spec §12). Everything a browser pulls from
// this server is text that compresses by an order of magnitude — a 15 kB
// script, a screenful of mostly spaces, a theme block — and the one consumer
// that cannot afford the bytes is a browser on a slow link, which is also the
// consumer most likely to have JavaScript disabled and be reading the no-JS
// page.
//
// Two encodings, deliberately:
//
//   - HTTP responses get Content-Encoding: gzip, negotiated per request.
//   - The WebSocket gets permessage-deflate (see Server.upgr), which deflates
//     each message independently. That property is the whole point: a screen
//     update is only useful if it arrives now, and a stream compressor that
//     holds bytes back until its window fills would turn a live terminal into a
//     slideshow.
//
// What must never be compressed is the SSE stream, for exactly that reason: it
// depends on each event being flushed the moment it is written.

// gzipAccepted reports whether the client asked for gzip. Only gzip is
// implemented, and a client that does not name it gets identity bytes — the
// safe default, since a mislabelled body is a broken body.
func gzipAccepted(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		// Strip any ";q=…" parameters; a zero quality weight means "do not use
		// this", so it must not match.
		coding, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		if q := strings.TrimSpace(params); strings.Contains(q, "q=0") && !strings.Contains(q, "q=0.") {
			return false
		}
		return true
	}
	return false
}

// compressibleContentType reports whether a response of this media type is worth
// deflating. Images and archives are already compressed, and the multipart and
// event-stream types must reach the client byte-for-byte.
func compressibleContentType(ct string) bool {
	if ct == "" {
		return false
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch {
	case strings.Contains(ct, "javascript"),
		strings.Contains(ct, "json"),
		strings.Contains(ct, "xml"),
		strings.Contains(ct, "svg"):
		return true
	}
	return false
}

// skipCompression reports whether this request must be handed to the handler
// untouched.
//
// The WebSocket upgrade writes the handshake straight to the ResponseWriter and
// hijacks the connection; wrapping it would corrupt the handshake, and the
// permessage-deflate extension already does that job per message. The SSE stream
// is not an error case but a different trade: it is explicitly flushed per
// event, so a compressor in front of it would undo the flushes the fallback
// transport depends on.
func skipCompression(r *http.Request) bool {
	if r.URL.Path == "/events" {
		return true
	}
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// gzipMiddleware compresses the responses it is allowed to compress.
//
// It buffers nothing itself: the decision is made when the handler has declared
// its Content-Type and is about to write, so a stream that flushes per event
// never enters the compressor at all.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipCompression(r) || !gzipAccepted(r) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		// Vary goes on unconditionally, even when this particular response was
		// not compressed: a shared cache must key on Accept-Encoding, or it will
		// hand a compressed body to a client that cannot read one.
		w.Header().Add("Vary", "Accept-Encoding")
		next.ServeHTTP(gw, r)
		gw.finish()
	})
}

// gzipResponseWriter compresses the first Write of a response whose declared
// content type is worth deflating, and passes everything else straight through.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	started bool
	done    bool
}

// Write compresses the body on first use, or forwards it untouched when the
// response is not compressible.
func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.started {
		g.started = true
		if !compressibleContentType(g.Header().Get("Content-Type")) {
			return g.ResponseWriter.Write(p)
		}
		g.Header().Set("Content-Encoding", "gzip")
		// The compressed length is not knowable in advance, and a Content-Length
		// left over from the handler would describe the wrong number of bytes.
		g.Header().Del("Content-Length")
		g.gz = gzip.NewWriter(g.ResponseWriter)
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

// finish closes the compressor, flushing the gzip trailer. A handler that never
// wrote anything leaves the response exactly as it found it.
func (g *gzipResponseWriter) finish() {
	if g.done || g.gz == nil {
		return
	}
	g.done = true
	_ = g.gz.Close()
}

// Flush forwards a flush to the compressor when compressing (which emits a
// sync marker, so the bytes actually leave) and to the underlying writer
// otherwise. The SSE stream never reaches here — skipCompression keeps it out —
// but a handler that flushes for any other reason must keep working.
func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
