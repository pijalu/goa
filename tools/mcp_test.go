package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/agent-mcp/client"

	"github.com/pijalu/goa/internal"
)

// fakeBackend records requests and returns canned payloads.
type fakeBackend struct {
	do       func(r client.Request) (any, error)
	shutdown func() (client.ShutdownResult, error)
	upsert   func(path, name string, e client.ServerEntry) (client.UpsertResult, error)
	closed   int
}

func (f *fakeBackend) Do(ctx context.Context, r client.Request) (any, error) {
	if f.do == nil {
		return nil, nil
	}
	return f.do(r)
}

func (f *fakeBackend) ShutdownDaemon(ctx context.Context, wait time.Duration) (client.ShutdownResult, error) {
	if f.shutdown == nil {
		return client.ShutdownResult{Shutdown: true, SessionsClosed: 1}, nil
	}
	return f.shutdown()
}

func (f *fakeBackend) Close() error {
	f.closed++
	return nil
}

func (f *fakeBackend) UpsertServerEntry(path, name string, e client.ServerEntry) (client.UpsertResult, error) {
	if f.upsert == nil {
		return client.UpsertResult{Name: name, Action: "added", Path: "/cfg"}, nil
	}
	return f.upsert(path, name, e)
}

func newAgentMCPTestTool(be AgentMCPBackend) *AgentMCPTool {
	return &AgentMCPTool{NewBackend: func(client.Options) (AgentMCPBackend, error) { return be, nil }}
}

func TestAgentMCPListServers(t *testing.T) {
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		if r.Op != client.OpListServers {
			t.Errorf("op = %s, want list-servers", r.Op)
		}
		return map[string]any{"servers": []any{
			map[string]any{"name": "alpha", "state": "idle"},
		}, "total": 1}, nil
	}}
	out, err := newAgentMCPTestTool(be).Execute(`{"action":"list_servers"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `"alpha"`) {
		t.Errorf("output = %s, want alpha entry", out)
	}
}

func TestAgentMCPListToolsCarriesParams(t *testing.T) {
	var got client.Request
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		got = r
		return map[string]any{"tools": []any{}, "total": 0}, nil
	}}
	_, err := newAgentMCPTestTool(be).Execute(`{"action":"list_tools","server":"gh","search":"issue","limit":5,"cursor":"c1"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Server != "gh" || got.Search != "issue" || got.Limit != 5 || got.Cursor != "c1" {
		t.Errorf("request = %+v, want server gh, search issue, limit 5, cursor c1", got)
	}
}

func TestAgentMCPValidateErrorBecomesInvalidParams(t *testing.T) {
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		return nil, client.Request{Op: client.OpCallTool}.Validate()
	}}
	_, err := newAgentMCPTestTool(be).Execute(`{"action":"call_tool","tool":"echo"}`)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var te *internal.ToolError
	if !errors.As(err, &te) {
		t.Fatalf("error = %v, want ToolError", err)
	}
	if te.Type != "invalid_params" {
		t.Errorf("type = %s, want invalid_params", te.Type)
	}
}

func TestAgentMCPMCPErrorClass(t *testing.T) {
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		return nil, fmt.Errorf("%w: server exploded", client.ErrMCP)
	}}
	_, err := newAgentMCPTestTool(be).Execute(`{"action":"list_tools","server":"gh"}`)
	var te *internal.ToolError
	if !errors.As(err, &te) || te.Type != "mcp_error" {
		t.Fatalf("error = %v, want mcp_error ToolError", err)
	}
}

func TestAgentMCPInternalErrorClass(t *testing.T) {
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		return nil, fmt.Errorf("%w: boom", client.ErrInternal)
	}}
	_, err := newAgentMCPTestTool(be).Execute(`{"action":"list_tools","server":"gh"}`)
	var te *internal.ToolError
	if !errors.As(err, &te) || te.Type != "internal_error" {
		t.Fatalf("error = %v, want internal_error ToolError", err)
	}
}

func TestAgentMCPUnknownAction(t *testing.T) {
	_, err := newAgentMCPTestTool(&fakeBackend{}).Execute(`{"action":"explode"}`)
	var te *internal.ToolError
	if !errors.As(err, &te) || te.Type != "invalid_input" {
		t.Fatalf("error = %v, want invalid_input", err)
	}
}

func TestAgentMCPBadJSON(t *testing.T) {
	_, err := newAgentMCPTestTool(&fakeBackend{}).Execute(`{nope`)
	var te *internal.ToolError
	if !errors.As(err, &te) || te.Type != "invalid_input" {
		t.Fatalf("error = %v, want invalid_input", err)
	}
}

func TestAgentMCPMissingAction(t *testing.T) {
	_, err := newAgentMCPTestTool(&fakeBackend{}).Execute(`{}`)
	var te *internal.ToolError
	if !errors.As(err, &te) || te.Type != "invalid_input" {
		t.Fatalf("error = %v, want invalid_input", err)
	}
}

func TestAgentMCPConfigAdd(t *testing.T) {
	var gotPath, gotName string
	var gotEntry client.ServerEntry
	be := &fakeBackend{upsert: func(path, name string, e client.ServerEntry) (client.UpsertResult, error) {
		gotPath, gotName, gotEntry = path, name, e
		return client.UpsertResult{Name: name, Action: "added", Path: path}, nil
	}}
	out, err := newAgentMCPTestTool(be).Execute(`{"action":"config_add","name":"gh","command":"npx","argv":["-y","srv"],"env":{"K":"V"},"config_path":"/tmp/cfg.toml"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotName != "gh" || gotPath != "/tmp/cfg.toml" {
		t.Errorf("upsert args = %q %q, want gh /tmp/cfg.toml", gotName, gotPath)
	}
	if gotEntry.Command != "npx" || len(gotEntry.Args) != 2 || gotEntry.Env["K"] != "V" {
		t.Errorf("entry = %+v, want npx with 2 argv entries and env", gotEntry)
	}
	if !strings.Contains(out, `"added"`) {
		t.Errorf("output = %s, want added", out)
	}
}

func TestAgentMCPConfigAddRequiresNameAndTarget(t *testing.T) {
	cases := []string{
		`{"action":"config_add"}`,
		`{"action":"config_add","name":"gh"}`,
		`{"action":"config_add","command":"npx"}`,
	}
	for _, in := range cases {
		if _, err := newAgentMCPTestTool(&fakeBackend{}).Execute(in); err == nil {
			t.Errorf("%s: want error, got nil", in)
		}
	}
}

func TestAgentMCPShutdown(t *testing.T) {
	be := &fakeBackend{shutdown: func() (client.ShutdownResult, error) {
		return client.ShutdownResult{Shutdown: false}, nil
	}}
	out, err := newAgentMCPTestTool(be).Execute(`{"action":"shutdown"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `"shutdown": false`) {
		t.Errorf("output = %s, want shutdown false", out)
	}
}

func TestAgentMCPNoticeAppendedToResult(t *testing.T) {
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		return map[string]any{"servers": []any{}}, nil
	}}
	var buf noticeBuffer
	tool := &AgentMCPTool{
		Options: client.Options{Stderr: &buf},
		NewBackend: func(o client.Options) (AgentMCPBackend, error) {
			// Mirror the real backend: notices flow through Options.Stderr.
			_, _ = o.Stderr.Write([]byte("daemon not reachable at x; auto-starting one\n"))
			return be, nil
		},
	}
	out, err := tool.Execute(`{"action":"list_servers"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "auto-starting") {
		t.Errorf("output = %s, want daemon notice appended", out)
	}
	// Notice consumed: second result carries none.
	out2, _ := tool.Execute(`{"action":"list_servers"}`)
	if strings.Contains(out2, "auto-starting") {
		t.Errorf("second output = %s, notice should be consumed", out2)
	}
}

// noticeBuffer collects daemon notices for assertions.
type noticeBuffer struct{ b strings.Builder }

func (n *noticeBuffer) Write(p []byte) (int, error) { return n.b.Write(p) }

func TestAgentMCPRawPassThrough(t *testing.T) {
	var got client.Request
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		got = r
		return map[string]any{"servers": []any{}}, nil
	}}
	if _, err := newAgentMCPTestTool(be).Execute(`{"action":"list_servers","raw":true}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !got.Raw {
		t.Error("raw flag lost in mapping")
	}
}

func TestAgentMCPCallToolArgsRawJSON(t *testing.T) {
	var got client.Request
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		got = r
		return map[string]any{"content": []any{map[string]any{"text": "hi"}}}, nil
	}}
	if _, err := newAgentMCPTestTool(be).Execute(`{"action":"call_tool","server":"gh","tool":"echo","args":{"text":"hi"}}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if string(got.Args) != `{"text":"hi"}` {
		t.Errorf("args = %s, want original JSON", got.Args)
	}
}

// TestAgentMCPSchemaIsValid checks the schema shape the deferred loader
// ships: name (B46: the tool is the generic "mcp" tool), enum actions, and
// required action.
func TestAgentMCPSchemaIsValid(t *testing.T) {
	s := (&AgentMCPTool{}).Schema()
	if s.Name != "mcp" {
		t.Errorf("name = %s, want mcp", s.Name)
	}
	props, ok := s.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("no properties")
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("no action property")
	}
	enum, _ := action["enum"].([]string)
	if len(enum) != 9 {
		t.Errorf("enum has %d actions, want 9", len(enum))
	}
	req, _ := s.Schema["required"].([]string)
	if len(req) != 1 || req[0] != "action" {
		t.Errorf("required = %v, want [action]", req)
	}
}

func TestAgentMCPDocsAndExamples(t *testing.T) {
	tool := &AgentMCPTool{}
	if tool.ShortDoc() == "" || tool.LongDoc() == "" {
		t.Fatal("docs must be embedded")
	}
	if len(tool.Examples()) == 0 {
		t.Fatal("examples must be present")
	}
	for _, ex := range tool.Examples() {
		var v map[string]any
		if err := json.Unmarshal([]byte(ex), &v); err != nil {
			t.Errorf("example %q is not JSON: %v", ex, err)
		}
	}
}

func TestAgentMCPRetryableFalse(t *testing.T) {
	if (&AgentMCPTool{}).IsRetryable(errors.New("x")) {
		t.Error("mcp tool errors must not be retryable")
	}
}
