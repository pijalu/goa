// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

type imageSubmitScenario struct {
	*uiScenario
	term     *clarifyKeyTerminal
	provider *goalSteeringCaptureProvider
}

func newImageSubmitScenario(t *testing.T) *imageSubmitScenario {
	t.Helper()
	term := &clarifyKeyTerminal{}
	term.w, term.h = 120, 34
	sc := newUIScenarioTerm(t, term, 120, 34)
	sc.engine.RunLoops()
	sc.app.subs.inputEditor = sc.editor
	sc.editor.SetOnSubmit(sc.app.makeSubmitHandler(sc.engine, sc.chat))
	sc.editor.OnEscape = func() { sc.app.handleEscape() }

	registry := core.NewCommandRegistry()
	sc.app.subs.registry = registry
	sc.app.subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))
	sc.app.subs.sessionStore = nil

	p := installImageSubmitAgent(t, sc.app)
	return &imageSubmitScenario{uiScenario: sc, term: term, provider: p}
}

func installImageSubmitAgent(t *testing.T, a *App) *goalSteeringCaptureProvider {
	t.Helper()
	p := &goalSteeringCaptureProvider{api: goalSteeringAPI("test-image-submit")}
	provider.RegisterApiProvider(p)
	model := goalSteeringModel(p.api)
	model.InputTypes = []string{"text", "image"}
	am := core.NewAgentManager(a.subs.cfg, nil, nil, nil, a.subs.events, "")
	if _, err := am.StartSession(model, provider.StreamOptions{}, "sys", nil, a.subs.cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	a.subs.agentMgr = am
	return p
}

func (s *imageSubmitScenario) submit(input string) {
	s.term.sendKey(input)
	s.term.sendKey("\r")
}

func (s *imageSubmitScenario) capture(label string) tui.Snapshot {
	s.tb.Helper()
	s.engine.RenderNow()
	return s.film.Capture(label, s.engine.AgentFrame(), s.status.Text())
}

func waitForImageRequests(t *testing.T, p *goalSteeringCaptureProvider, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for p.requestCount() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := p.requestCount(); got < want {
		t.Fatalf("provider requests = %d, want at least %d", got, want)
	}
}

func lastImageSubmission(p *goalSteeringCaptureProvider) (string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return "", nil
	}
	msgs := p.requests[len(p.requests)-1]
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != provider.RoleUser {
			continue
		}
		var text string
		var images []string
		for _, block := range msgs[i].Content {
			switch block.Type {
			case provider.ContentBlockText:
				text += block.Text
			case provider.ContentBlockImage:
				images = append(images, block.ImageData)
			}
		}
		return text, images
	}
	return "", nil
}

func normalizedWrappedText(text string) string {
	text = ansi.Strip(text)
	text = strings.NewReplacer("│", "", "╭", "", "╮", "", "╰", "", "╯", "").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

func assertNoAttachedImageChat(t *testing.T, s *imageSubmitScenario) {
	t.Helper()
	s.engine.ApplySync(func() {
		if containsRendered(s.chat, "[attached image:") {
			t.Error("image was displayed as attached before or without confirmation")
		}
	})
}

func TestImageAttachmentConfirmation_AcceptAttachesAllImages(t *testing.T) {
	s := newImageSubmitScenario(t)
	dir := t.TempDir()
	a := writePNG(t, dir, "Screenshot 2026-10-30 at 15.29.10.png")
	b := writePNG(t, dir, "second image.png")
	// Start with an absolute path: a valid image submission is not a slash command.
	input := shellEscapeImagePath(a) + " compare @" + shellEscapeImagePath(b)
	s.submit(input)

	waitForVisibleText(t, s.engine, "Attach 2 images?")
	prompt := s.capture("batch image confirmation")
	visible := strings.Join(prompt.Frame.Visible, "\n")
	if !strings.Contains(visible, "Attach 2 images?") {
		t.Fatalf("confirmation missing from filmstrip:\n%s", visible)
	}
	// Paths wrap at terminal-cell boundaries; normalize that layout whitespace
	// before checking that neither filename was clipped out of the card.
	normalized := normalizedWrappedText(visible)
	for _, name := range []string{filepath.Base(a), filepath.Base(b)} {
		if !strings.Contains(normalized, name) {
			t.Errorf("confirmation does not show %q:\n%s", name, visible)
		}
	}
	if got := s.provider.requestCount(); got != 0 {
		t.Fatalf("provider requests before confirmation = %d, want 0", got)
	}
	assertNoAttachedImageChat(t, s)

	// The safe default is text-only; move up to Attach and accept explicitly.
	s.term.sendKey("\x1b[A")
	s.term.sendKey("\r")
	waitForImageRequests(t, s.provider, 1)
	text, images := lastImageSubmission(s.provider)
	if text != "compare" {
		t.Errorf("model text = %q, want %q", text, "compare")
	}
	if !reflect.DeepEqual(images, []string{a, b}) {
		t.Errorf("model images = %v, want %v", images, []string{a, b})
	}
	s.engine.ApplySync(func() {
		rendered := normalizedWrappedText(strings.Join(s.chat.Render(110), "\n"))
		if got := strings.Count(rendered, "[attached image:"); got != 2 {
			t.Errorf("attached-image chat rows = %d, want 2:\n%s", got, rendered)
		}
		for _, path := range []string{a, b} {
			if !strings.Contains(rendered, filepath.Base(path)) {
				t.Errorf("accepted image %s missing from chat:\n%s", path, rendered)
			}
		}
	})
	s.capture("accepted images")
}

func TestImageAttachmentConfirmation_SlashCommandImageArgumentStaysCommand(t *testing.T) {
	s := newImageSubmitScenario(t)
	path := writePNG(t, t.TempDir(), "command argument.png")
	ran := false
	registry := core.NewCommandRegistry()
	if err := registry.Register(&scriptedCommand{name: "inspect", help: "inspect", ran: &ran}); err != nil {
		t.Fatal(err)
	}
	s.app.subs.registry = registry
	s.app.subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))

	s.submit("/inspect:" + shellEscapeImagePath(path))
	s.engine.ApplySync(func() {})

	if !ran {
		t.Errorf("slash command with an image argument was not executed; screen:\n%s", s.engine.VisibleText())
	}
	if got := s.provider.requestCount(); got != 0 {
		t.Errorf("command image argument reached provider as a user turn: %d requests", got)
	}
	if visible := s.engine.VisibleText(); strings.Contains(visible, "Attach image?") {
		t.Errorf("slash command unexpectedly opened attachment confirmation:\n%s", visible)
	}
}

func TestImageAttachmentConfirmation_DeclineSendsOriginalTextWithoutImages(t *testing.T) {
	s := newImageSubmitScenario(t)
	path := writePNG(t, t.TempDir(), "Screenshot 2026-10-30 at 15.29.10.png")
	input := "inspect " + shellEscapeImagePath(path) + " please"
	s.submit(input)

	waitForVisibleText(t, s.engine, "Attach image?")
	if got := s.provider.requestCount(); got != 0 {
		t.Fatalf("provider requests before confirmation = %d, want 0", got)
	}
	s.term.sendKey("\r") // default: send as text, do not attach
	waitForImageRequests(t, s.provider, 1)

	text, images := lastImageSubmission(s.provider)
	if text != input {
		t.Errorf("model text = %q, want untouched input %q", text, input)
	}
	if len(images) != 0 {
		t.Errorf("declined images reached model: %v", images)
	}
	assertNoAttachedImageChat(t, s)
	s.capture("declined image attachment")
}

func waitForWebScreen(t *testing.T, s *webCommandSession, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.render()
		if strings.Contains(s.screen(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("WEB screen never showed %q:\n%s", want, s.screen())
}

type webImageSubmitScenario struct {
	*webCommandSession
	provider *goalSteeringCaptureProvider
	input    string
	path     string
}

func newWebImageSubmitScenario(t *testing.T) *webImageSubmitScenario {
	t.Helper()
	s := newWebCommandSession(t)
	p := installImageSubmitAgent(t, s.sc.app)
	s.sc.engine.RunLoops()
	path := writePNG(t, t.TempDir(), "web screenshot.png")
	input := "inspect " + shellEscapeImagePath(path)
	s.submit(t, input)
	waitForWebScreen(t, s, "Attach image?")
	if got := p.requestCount(); got != 0 {
		t.Fatalf("WEB provider requests before confirmation = %d, want 0", got)
	}
	return &webImageSubmitScenario{webCommandSession: s, provider: p, input: input, path: path}
}

func TestWebUI_ImageAttachmentConfirmationAccept(t *testing.T) {
	s := newWebImageSubmitScenario(t)
	s.press(t, webui.KeyEvent{Key: "ArrowUp"})
	s.press(t, webui.KeyEvent{Key: "Enter"})
	waitForImageRequests(t, s.provider, 1)

	text, images := lastImageSubmission(s.provider)
	if text != "inspect" || !reflect.DeepEqual(images, []string{s.path}) {
		t.Errorf("WEB accepted submission = (%q, %v), want (inspect, [%s])", text, images, s.path)
	}
}

func TestWebUI_ImageAttachmentConfirmationDecline(t *testing.T) {
	s := newWebImageSubmitScenario(t)
	s.press(t, webui.KeyEvent{Key: "Enter"})
	waitForImageRequests(t, s.provider, 1)

	text, images := lastImageSubmission(s.provider)
	if text != s.input || len(images) != 0 {
		t.Errorf("WEB declined submission = (%q, %v), want original text without images", text, images)
	}
}

func waitForWebEditorText(t *testing.T, s *webCommandSession, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		s.sc.engine.ApplySync(func() { got = s.sc.editor.Text() })
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	var got string
	s.sc.engine.ApplySync(func() { got = s.sc.editor.Text() })
	t.Fatalf("WEB editor text = %q, want %q", got, want)
}

func TestWebUI_ImageAttachmentConfirmationCancel(t *testing.T) {
	s := newWebImageSubmitScenario(t)
	s.press(t, webui.KeyEvent{Key: "Escape"})
	waitForWebEditorText(t, s.webCommandSession, s.input)

	time.Sleep(50 * time.Millisecond)
	if got := s.provider.requestCount(); got != 0 {
		t.Errorf("WEB provider requests after cancel = %d, want 0", got)
	}
}

func TestImageAttachmentConfirmation_CancelRestoresInputWithoutSending(t *testing.T) {
	s := newImageSubmitScenario(t)
	path := writePNG(t, t.TempDir(), "cancel me.png")
	input := "inspect " + shellEscapeImagePath(path)
	s.submit(input)

	waitForVisibleText(t, s.engine, "Attach image?")
	s.term.sendKey("\x1b")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var restored string
		s.engine.ApplySync(func() { restored = s.editor.Text() })
		if restored == input {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	var restored string
	s.engine.ApplySync(func() { restored = s.editor.Text() })
	if restored != input {
		t.Errorf("editor text after cancel = %q, want %q", restored, input)
	}
	time.Sleep(50 * time.Millisecond)
	if got := s.provider.requestCount(); got != 0 {
		t.Errorf("provider requests after cancel = %d, want 0", got)
	}
	assertNoAttachedImageChat(t, s)
	s.capture("cancelled image attachment")
}
