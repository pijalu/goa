package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/agent-mcp/client"
)

// callToolWithPayload runs one call_tool through a fake backend returning
// payload, with extraction rooted at dir.
func callToolWithPayload(t *testing.T, dir, payload string) (string, []string) {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		t.Fatalf("test payload does not decode: %v", err)
	}
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		return client.Shape(value, client.Request{Op: client.OpCallTool})
	}}
	tool := newAgentMCPTestTool(be)
	tool.ExtractDir = dir
	res, err := tool.ExecuteContextWithResult(context.Background(),
		`{"action":"call_tool","server":"chrome","tool":"take_screenshot"}`)
	if err != nil {
		t.Fatalf("ExecuteContextWithResult: %v", err)
	}
	return res.Output, res.Images
}

// soleFile returns the single regular file in dir.
func soleFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 {
		t.Fatalf("expected exactly one extracted file, got %v", names)
	}
	return filepath.Join(dir, names[0])
}

// TestAgentMCPImagePartExtractedToImages verifies the vision contract: an
// MCP image content part becomes a file attached via ToolResult.Images, and
// the JSON output carries a short marker instead of base64.
func TestAgentMCPImagePartExtractedToImages(t *testing.T) {
	dir := t.TempDir()
	raw := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 'f', 'a', 'k', 'e'}
	encoded := base64.StdEncoding.EncodeToString(raw)
	payload := `{"content":[{"type":"image","data":"` + encoded + `","mimeType":"image/png"}]}`
	output, images := callToolWithPayload(t, dir, payload)

	if len(images) != 1 {
		t.Fatalf("expected 1 image path, got %v", images)
	}
	got, err := os.ReadFile(images[0])
	if err != nil {
		t.Fatalf("read extracted image: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("extracted bytes mismatch: got %q want %q", got, raw)
	}
	if want := ".png"; filepath.Ext(images[0]) != want {
		t.Fatalf("extension = %q, want %q", filepath.Ext(images[0]), want)
	}
	if strings.Contains(output, encoded) {
		t.Fatal("output still contains base64 payload")
	}
	if !strings.Contains(output, "[binary: image/png → "+images[0]) {
		t.Fatalf("output missing marker, got:\n%s", output)
	}
}

// TestAgentMCPBinaryPartsTable exercises the non-image routes: audio parts
// and resource blobs become files + markers but never Images entries.
func TestAgentMCPBinaryPartsTable(t *testing.T) {
	pdfBytes := "%PDF-1.4 fake"
	wavBytes := "RIFFfake"
	cases := []struct {
		name       string
		payload    string
		ext        string
		mime       string
		wantImages int
	}{
		{
			name:    "audio content part",
			payload: `{"content":[{"type":"audio","data":"` + base64.StdEncoding.EncodeToString([]byte(wavBytes)) + `","mimeType":"audio/wav"}]}`,
			ext:     ".wav", mime: "audio/wav",
		},
		{
			name:    "resource blob",
			payload: `{"contents":[{"uri":"file:///x.pdf","mimeType":"application/pdf","blob":"` + base64.StdEncoding.EncodeToString([]byte(pdfBytes)) + `"}]}`,
			ext:     ".pdf", mime: "application/pdf",
		},
		{
			name:    "url-safe unpadded base64 image",
			payload: `{"content":[{"type":"image","data":"` + base64.RawURLEncoding.EncodeToString([]byte(wavBytes)) + `","mimeType":"image/jpeg"}]}`,
			ext:     ".jpg", mime: "image/jpeg", wantImages: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			output, images := callToolWithPayload(t, dir, tc.payload)
			if len(images) != tc.wantImages {
				t.Fatalf("images = %v, want %d", images, tc.wantImages)
			}
			path := soleFile(t, dir)
			if filepath.Ext(path) != tc.ext {
				t.Fatalf("extension = %q, want %q", filepath.Ext(path), tc.ext)
			}
			if !strings.Contains(output, "[binary: "+tc.mime+" → "+path) {
				t.Fatalf("output missing marker:\n%s", output)
			}
		})
	}
}

// TestAgentMCPTextOnlyResultUntouched proves text/structured results are not
// mutated: output is the byte-identical JSON of the shaped payload.
func TestAgentMCPTextOnlyResultUntouched(t *testing.T) {
	dir := t.TempDir()
	payload := `{"content":[{"type":"text","text":"hello"}],"structuredContent":{"answer":42}}`
	output, images := callToolWithPayload(t, dir, payload)
	if len(images) != 0 {
		t.Fatalf("text-only result produced images: %v", images)
	}
	var shaped map[string]any
	if err := json.Unmarshal([]byte(payload), &shaped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	body, _ := json.MarshalIndent(shaped, "", "  ")
	if strings.TrimSpace(output) != string(body) {
		t.Fatalf("output mutated:\n got: %s\nwant: %s", output, body)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("extract dir polluted: %v", entries)
	}
}

// TestAgentMCPInvalidBase64DegradesToMarker: a malformed payload must not
// fail the call nor ship the raw base64; it degrades to an error marker.
func TestAgentMCPInvalidBase64DegradesToMarker(t *testing.T) {
	dir := t.TempDir()
	payload := `{"content":[{"type":"image","data":"!!!not-base64!!!","mimeType":"image/png"}]}`
	output, images := callToolWithPayload(t, dir, payload)
	if len(images) != 0 {
		t.Fatalf("undecodable part produced image: %v", images)
	}
	if !strings.Contains(output, "[binary: image/png undecodable:") {
		t.Fatalf("output missing undecodable marker:\n%s", output)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("failed decode still wrote a file: %v", entries)
	}
}

// TestAgentMCPExecuteFallbackStillWorks keeps the base Tool contract alive.
func TestAgentMCPExecuteFallbackStillWorks(t *testing.T) {
	dir := t.TempDir()
	raw := json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`)
	be := &fakeBackend{do: func(r client.Request) (any, error) {
		return client.Shape(raw, client.Request{Op: client.OpCallTool})
	}}
	tool := newAgentMCPTestTool(be)
	tool.ExtractDir = dir
	out, err := tool.Execute(`{"action":"call_tool","server":"chrome","tool":"hi"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("Execute output wrong: %s", out)
	}
}

// TestAgentMCPCloseRemovesExtractedArtifacts pins the cleanup contract: an
// injected dir loses its extracted files; an owned dir disappears entirely.
func TestAgentMCPCloseRemovesExtractedArtifacts(t *testing.T) {
	t.Run("injected dir keeps dir, drops files", func(t *testing.T) {
		dir := t.TempDir()
		keep := filepath.Join(dir, "keep.txt")
		if err := os.WriteFile(keep, []byte("stay"), 0o600); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		payload := `{"content":[{"type":"image","data":"` +
			base64.StdEncoding.EncodeToString([]byte("px")) + `","mimeType":"image/png"}]}`
		tool := newAgentMCPTestTool(&fakeBackend{do: func(r client.Request) (any, error) {
			var value any
			if err := json.Unmarshal([]byte(payload), &value); err != nil {
				t.Fatalf("payload: %v", err)
			}
			return client.Shape(value, client.Request{Op: client.OpCallTool})
		}})
		tool.ExtractDir = dir
		res, err := tool.ExecuteContextWithResult(context.Background(),
			`{"action":"call_tool","server":"chrome","tool":"take_screenshot"}`)
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if len(res.Images) != 1 {
			t.Fatalf("images = %v, want 1", res.Images)
		}
		if err := tool.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("unrelated file removed: %v", err)
		}
		if _, err := os.Stat(res.Images[0]); !os.IsNotExist(err) {
			t.Fatalf("extracted file survived Close: %v", err)
		}
	})
	t.Run("owned dir removed entirely", func(t *testing.T) {
		payload := `{"content":[{"type":"image","data":"` +
			base64.StdEncoding.EncodeToString([]byte("px")) + `","mimeType":"image/png"}]}`
		raw := json.RawMessage(payload)
		be := &fakeBackend{do: func(r client.Request) (any, error) {
			return client.Shape(raw, client.Request{Op: client.OpCallTool})
		}}
		tool := newAgentMCPTestTool(be)
		res, err := tool.ExecuteContextWithResult(context.Background(),
			`{"action":"call_tool","server":"chrome","tool":"take_screenshot"}`)
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if len(res.Images) != 1 {
			t.Fatalf("images = %v, want 1", res.Images)
		}
		if err := tool.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := os.Stat(res.Images[0]); !os.IsNotExist(err) {
			t.Fatalf("owned dir survived Close: %v", err)
		}
	})
}

// TestHumanSize covered the marker size rendering; that function now lives
// in the agent-mcp client library (client.HumanSize, with its own tests), so
// goa has no duplicate to test.
