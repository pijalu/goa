package config

import (
	"testing"
)

func TestRenderTraceEnvOverride(t *testing.T) {
	t.Setenv("GOA_LOGGING_RENDER_TRACE", "/tmp/from_env.jsonl")
	cl := NewCascadeLoader(".", "", map[string]string{})
	cfg := &Config{}
	cl.applyEnvOverrides(cfg)
	if cfg.Logging.RenderTrace != "/tmp/from_env.jsonl" {
		t.Fatalf("GOA_LOGGING_RENDER_TRACE did not populate RenderTrace: got %q", cfg.Logging.RenderTrace)
	}
}

func TestRenderTraceCLIMerge(t *testing.T) {
	cl := NewCascadeLoader(".", "", map[string]string{"render_trace": "/tmp/from_cli.jsonl"})
	cfg := &Config{}
	cl.applyCLIOverrides(cfg)
	if cfg.Logging.RenderTrace != "/tmp/from_cli.jsonl" {
		t.Fatalf("render_trace CLI override did not populate RenderTrace: got %q", cfg.Logging.RenderTrace)
	}
}

func TestRenderTraceMerge(t *testing.T) {
	base := &Config{}
	base.Logging.RenderTrace = "/base.jsonl"
	other := &Config{}
	other.Logging.RenderTrace = "/other.jsonl"
	base.mergeLogging(other)
	if base.Logging.RenderTrace != "/other.jsonl" {
		t.Fatalf("merge did not take other.RenderTrace: got %q", base.Logging.RenderTrace)
	}
}
