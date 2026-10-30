// SPDX-License-Identifier: GPL-3.0-or-later

package google

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestGoogleToolResultImageEncodingAndTextOnlyDowngrade(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tool.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	wantBase64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	message := provider.NewToolResultMessage("call_read", "read", "image metadata", false)
	message.Content = append(message.Content, provider.ContentBlock{
		Type: provider.ContentBlockImage, ImageData: path,
	})

	t.Run("vision model", func(t *testing.T) {
		body := buildGoogleBody(provider.Model{InputTypes: []string{"text", "image"}}, provider.Context{
			Messages: []provider.Message{message},
		}, provider.StreamOptions{})
		payload := marshalGoogleToolImageBody(t, body)
		for _, want := range []string{`"functionResponse"`, `"inlineData"`, wantBase64, "image metadata"} {
			if !strings.Contains(payload, want) {
				t.Errorf("payload missing %q: %s", want, payload)
			}
		}
	})

	t.Run("text-only model", func(t *testing.T) {
		body := buildGoogleBody(provider.Model{InputTypes: []string{"text"}}, provider.Context{
			Messages: []provider.Message{message},
		}, provider.StreamOptions{})
		payload := marshalGoogleToolImageBody(t, body)
		if strings.Contains(payload, wantBase64) || strings.Contains(payload, path) {
			t.Errorf("text-only payload contains image data: %s", payload)
		}
		if !strings.Contains(payload, "tool image omitted: model does not support images") {
			t.Errorf("text-only payload lacks explicit omission notice: %s", payload)
		}
	})
}

func marshalGoogleToolImageBody(t *testing.T, body map[string]interface{}) string {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
