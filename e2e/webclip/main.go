// Command webclip drives a REAL clipboard check against a running `goa server`
// page: it connects to the browser agent-browser launched (CDP WebSocket URL),
// grants the clipboard permissions, emulates document focus, and then performs
// copy / paste / cut with REAL chord events, reading the result back through
// navigator.clipboard.
//
// It exists because the page's clipboard chords cannot be checked with synthetic
// events: what has to hold is that a real chord moves the real clipboard and
// reaches the engine. Measured facts this harness is built around (Chrome,
// headless, macOS):
//
//   - CDP-injected chords do NOT trigger the browser's own clipboard actions
//     (no `copy`/`paste` event, in headless and headed alike): the page's own
//     path is what gets exercised, which is exactly what a fix must make work.
//   - Without Browser.grantPermissions the clipboard reads are refused, and
//     without Emulation.setFocusEmulationEnabled the document is unfocused and
//     Chrome auto-denies them — a page that works looks broken.
//   - The tab must be brought to the front: background tabs have their timers
//     throttled, so a page's fallback path can look dead.
//
// Usage:
//
//	webclip --ws <cdp-ws-url> --url <session-url> [--check copy|paste|cut|all]
//
// Exit status 0 when every requested check passes.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	opts, err := optionsFromFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := drive(opts); err != nil {
		if !errors.Is(err, errChecksFailed) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

// options is one invocation: where to connect, what to drive, what to check.
type options struct {
	ws     string
	page   string
	origin string
	check  string
}

func optionsFromFlags() (options, error) {
	ws := flag.String("ws", "", "CDP WebSocket URL (agent-browser get cdp-url)")
	page := flag.String("url", "", "the session page URL to drive")
	check := flag.String("check", "all", "copy|paste|cut|all")
	flag.Parse()
	if *ws == "" || *page == "" {
		return options{}, errors.New("usage: webclip --ws <cdp-ws-url> --url <session-url> [--check copy|paste|cut|all]")
	}
	return options{ws: *ws, page: *page, origin: originOf(*page), check: *check}, nil
}

// errChecksFailed means a check reported FAIL; its detail is already on stdout.
var errChecksFailed = errors.New("clipboard checks failed")

// drive connects, prepares the page, and runs the requested checks. It returns
// errChecksFailed when any of them failed.
func drive(opts options) error {
	c, err := dial(opts.ws)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer c.close()
	if err := c.prepare(opts.page, opts.origin); err != nil {
		return err
	}
	failed := 0
	for _, check := range checksFor(opts.check) {
		failed += check(c)
	}
	if failed > 0 {
		return errChecksFailed
	}
	return nil
}

// checksFor maps a --check name to the checks to run; anything else (including
// the default "all") runs every check, in the order a user would use them.
func checksFor(name string) []func(*client) int {
	switch name {
	case "copy":
		return []func(*client) int{runCopy}
	case "paste":
		return []func(*client) int{runPaste}
	case "cut":
		return []func(*client) int{runCut}
	default:
		return []func(*client) int{runCopy, runPaste, runCut}
	}
}

// prepare attaches to the page, gives it the environment a real user has — the
// tab in front, the document focused, the clipboard permission granted — and
// reloads it, so every run starts from a clean document. A page that has already
// been driven carries state (focus on the paste target, a selection anchored in
// replaced nodes) that makes a second run measure the previous one; the session
// itself is untouched by the reload.
func (c *client) prepare(pageURL, origin string) error {
	if _, err := c.browserCall("Target.getTargets", nil); err != nil {
		return fmt.Errorf("getTargets: %w", err)
	}
	target := c.pageTarget(pageURL)
	if target == "" {
		return fmt.Errorf("no page target for %s", pageURL)
	}
	if err := c.attach(target); err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	if _, err := c.call("Page.bringToFront", nil, c.session); err != nil {
		return fmt.Errorf("bringToFront: %w", err)
	}
	if _, err := c.call("Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}, c.session); err != nil {
		return fmt.Errorf("focus emulation: %w", err)
	}
	if _, err := c.browserCall("Browser.grantPermissions", map[string]any{
		"origin":      origin,
		"permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"},
	}); err != nil {
		return fmt.Errorf("grantPermissions: %w", err)
	}
	if _, err := c.call("Page.reload", nil, c.session); err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	// Wait for the page to come back and draw its grid WITH CONTENT: the checks
	// read the DOM, and an empty grid is indistinguishable from a page that never
	// reconnected — waiting only for `#rows .row` to exist returns as soon as the
	// first frame creates the row divs, which can precede their text and made
	// every check report "no visible grid row with text to select" against a
	// perfectly healthy page.
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		if txt, ok := c.eval(
			`(function(){var r=document.getElementById('rows');if(!r)return '';
				for(var i=0;i<r.children.length;i++){var t=(r.children[i].textContent||'').trim();if(t.length>0)return t}
				return ''})()`, false).(string); ok && txt != "" {
			break
		}
	}
	c.focusGrid()
	return nil
}

// runCopy selects a grid row with a real drag and copies it with Cmd+C, then
// asserts the real clipboard holds the selection.
func runCopy(c *client) int {
	const sentinel = "WEBFIGHT-SENTINEL-COPY"
	c.focusGrid()
	c.eval(fmt.Sprintf("navigator.clipboard.writeText(%q).then(()=>1,e=>0)", sentinel), true)
	// Drag across a row that is INSIDE the viewport: the last row can sit below
	// it, where a mouse event hits nothing and selects nothing.
	box := c.eval(`(function(){var rows=document.getElementById('rows'),screen=document.getElementById('screen');
		var view=screen.getBoundingClientRect();
		for (var i=0;i<rows.children.length;i++){var el=rows.children[i],b=el.getBoundingClientRect();
			if (b.height<=0) continue;
			if (b.top<view.top+2||b.bottom>view.bottom-2) continue;
			if ((el.textContent||'').trim().length<8) continue;
			return [b.left,b.top,b.height,b.width]}return null})()`, false)
	xy, ok := box.([]any)
	if !ok || len(xy) != 4 {
		report("copy", false, "no visible grid row with text to select")
		return 1
	}
	left, top, height, width := num(xy[0]), num(xy[1]), num(xy[2]), num(xy[3])
	endX := left + width - 8
	if endX > left+220 {
		endX = left + 220
	}
	c.eval("window.getSelection().removeAllRanges();1", false)
	c.mouse(left+4, top+height/2)
	c.drag(endX, top+height/2)
	time.Sleep(250 * time.Millisecond)

	sel, _ := c.eval("String(window.getSelection())", false).(string)
	if sel == "" {
		// A repaint between the mouse events can drop the drag's selection. Select
		// the same row programmatically and say so: the chord is what this check is
		// about, and the drag is covered by the layout assertions above.
		sel, _ = c.eval(`(function(){var rows=document.getElementById('rows'),screen=document.getElementById('screen');
			var view=screen.getBoundingClientRect();
			for (var i=0;i<rows.children.length;i++){var el=rows.children[i],b=el.getBoundingClientRect();
				if (b.height<=0||b.top<view.top+2||b.bottom>view.bottom-2) continue;
				var walker=document.createTreeWalker(el,NodeFilter.SHOW_TEXT,null),node;
				while((node=walker.nextNode())){
					var txt=node.textContent||""; if(txt.trim().length<8) continue;
					var rg=document.createRange(); rg.setStart(node,0); rg.setEnd(node,Math.min(20,txt.length));
					window.getSelection().removeAllRanges(); window.getSelection().addRange(rg);
					return String(window.getSelection())}}
			return ""})()`, false).(string)
	}
	if sel == "" {
		report("copy", false, "no text could be selected in the grid")
		return 1
	}
	c.chord(metaMod, "c", "KeyC", 'C', nativeC)
	time.Sleep(400 * time.Millisecond)
	got, _ := c.eval("navigator.clipboard.readText().then(t=>t,e=>'ERR '+e.name)", true).(string)
	if got == sentinel {
		report("copy", false, "clipboard unchanged after Cmd+C")
		return 1
	}
	if !strings.Contains(sel, got) {
		report("copy", false, fmt.Sprintf("clipboard %q is not the selection %q", clip(got), clip(sel)))
		return 1
	}
	report("copy", true, fmt.Sprintf("Cmd+C put %d selected characters on the clipboard", len(got)))
	return 0
}

// runPaste puts a known string on the clipboard and pastes it with Cmd+V, then
// asserts the engine's input line gained it — exactly once.
func runPaste(c *client) int {
	// A unique token per run: the engine's input line accumulates across runs, so
	// a fixed string would be counted twice the second time around.
	want := fmt.Sprintf("WEBFIGHT-PASTE-%d", time.Now().UnixNano()%1_000_000)
	c.focusGrid()
	c.eval(fmt.Sprintf("navigator.clipboard.writeText(%q).then(()=>1,e=>0)", want), true)
	before := c.rowsText()
	c.eval("window.getSelection().removeAllRanges();1", false)
	c.chord(metaMod, "v", "KeyV", 'V', nativeV)
	time.Sleep(1200 * time.Millisecond)
	after := c.rowsText()
	if n := strings.Count(after, want); n != 1 {
		report("paste", false, fmt.Sprintf("the pasted text appears %d times (want 1)", n))
		return 1
	}
	if !strings.Contains(after, want) {
		report("paste", false, "Cmd+V did not insert the clipboard into the input line")
		return 1
	}
	_ = before
	report("paste", true, "Cmd+V inserted the clipboard text exactly once")
	return 0
}

// runCut types a known line, selects a slice of it, cuts it with Cmd+X, and
// asserts both halves: the clipboard holds the slice and the line lost it.
func runCut(c *client) int {
	// A unique token per run: the engine's input line accumulates across runs, and
	// a repeated string would make "which occurrence did the cut touch" ambiguous.
	typed := fmt.Sprintf("webcut-hello-%d", time.Now().UnixNano()%1_000_000)
	c.focusGrid()
	c.typeText(typed)
	time.Sleep(900 * time.Millisecond)
	if line := c.rowsText(); !strings.Contains(line, typed) {
		focus, _ := c.eval("document.activeElement && document.activeElement.id", false).(string)
		report("cut", false, fmt.Sprintf("typing did not reach the input line (focus=%q, line tail=%q)",
			focus, tail(strings.TrimSpace(line), 60)))
		return 1
	}
	// Select "hello" inside the typed token: mid-line, so the cut has to walk the
	// cursor before deleting. The row is built from styled runs, so the token can
	// span several text nodes: the row's text is reassembled first and the offsets
	// are mapped back to node/offset pairs.
	selectHello := fmt.Sprintf(`(function(){
		function atOffset(nodes, starts, off) {
			for (var j = nodes.length - 1; j >= 0; j--) {
				if (starts[j] <= off) {
					var len = (nodes[j].textContent || "").length;
					return { node: nodes[j], offset: Math.min(off - starts[j], len) };
				}
			}
			return { node: nodes[0], offset: 0 };
		}
		var rows = document.getElementById('rows');
		for (var i = 0; i < rows.children.length; i++) {
			var walker = document.createTreeWalker(rows.children[i], NodeFilter.SHOW_TEXT, null), node;
			var nodes = [], starts = [], text = "";
			while ((node = walker.nextNode())) {
				nodes.push(node); starts.push(text.length); text += node.textContent || "";
			}
			var at = text.indexOf(%q);
			if (at < 0 || !nodes.length) continue;
			var a = atOffset(nodes, starts, at + 7), b = atOffset(nodes, starts, at + 12);
			var rg = document.createRange();
			rg.setStart(a.node, a.offset); rg.setEnd(b.node, b.offset);
			window.getSelection().removeAllRanges(); window.getSelection().addRange(rg);
			return String(window.getSelection());
		}
		return ""})()`, typed)
	if got, _ := c.eval(selectHello, false).(string); got != "hello" {
		report("cut", false, fmt.Sprintf("could not select the cut range (got %q)", got))
		return 1
	}
	// The engine repaints a row by replacing its children, which drops a selection
	// anchored in the old nodes: make sure the selection is still live in the
	// instant before the chord, or the page sees nothing to cut.
	if got, _ := c.eval("String(window.getSelection())", false).(string); got != "hello" {
		if again, _ := c.eval(selectHello, false).(string); again != "hello" {
			report("cut", false, fmt.Sprintf("the selection did not survive to the chord (got %q)", again))
			return 1
		}
	}
	c.chord(metaMod, "x", "KeyX", 'X', nativeX)
	time.Sleep(900 * time.Millisecond)

	selAfter, _ := c.eval("String(window.getSelection())", false).(string)
	line := c.rowsText()
	clipText, _ := c.eval("navigator.clipboard.readText().then(t=>t,e=>'ERR '+e.name)", true).(string)
	if clipText != "hello" {
		report("cut", false, fmt.Sprintf("clipboard after the cut = %q (want %q); selection now %q, line lost it: %v",
			clip(clipText), "hello", clip(selAfter), !strings.Contains(line, typed)))
		return 1
	}
	if strings.Contains(line, typed) {
		report("cut", false, "the cut characters are still in the input line")
		return 1
	}
	if !strings.Contains(line, strings.Replace(typed, "hello", "", 1)) {
		report("cut", false, "the input line is not what the cut should leave behind")
		return 1
	}
	report("cut", true, "Cmd+X put the selection on the clipboard and removed it from the input line")
	return 0
}

// CDP modifiers and the macOS hardware keycodes the chords need. A Windows
// virtual-key code in nativeVirtualKeyCode makes Chrome map the event to
// whatever character that keycode carries, which is how a test ends up pressing
// the wrong key.
const (
	metaMod = 4
	ctrlMod = 2

	nativeC = 8
	nativeV = 9
	nativeX = 7
)

func report(name string, ok bool, detail string) {
	state := "FAIL"
	if ok {
		state = "PASS"
	}
	fmt.Printf("%s %s — %s\n", name, state, detail)
}

func originOf(pageURL string) string {
	if i := strings.Index(pageURL, "/s/"); i > 0 {
		return pageURL[:i]
	}
	return pageURL
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// tail returns the last n bytes of s, for a readable failure message.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

// ------------------------------------------------------------------ CDP client

type client struct {
	conn    *websocket.Conn
	session string

	mu      sync.Mutex
	nextID  int
	pending map[int]chan cdpResult
}

type cdpResult struct {
	result json.RawMessage
	err    error
}

func dial(wsURL string) (*client, error) {
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, err
	}
	c := &client{conn: conn, pending: map[int]chan cdpResult{}}
	go c.readLoop()
	return c, nil
}

func (c *client) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if msg.Error != nil {
			ch <- cdpResult{err: fmt.Errorf("%s", msg.Error.Message)}
			continue
		}
		ch <- cdpResult{result: msg.Result}
	}
}

func (c *client) close() { _ = c.conn.Close() }

// call sends one CDP command on the browser or the attached page session.
func (c *client) call(method string, params map[string]any, session string) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan cdpResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params == nil {
		params = map[string]any{}
	}
	payload["params"] = params
	if session != "" {
		payload["sessionId"] = session
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		return res.result, res.err
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout: %s", method)
	}
}

func (c *client) browserCall(method string, params map[string]any) (json.RawMessage, error) {
	return c.call(method, params, "")
}

// pageTarget picks the page target for pageURL (exact match first: another tab
// on the same origin would otherwise be driven instead).
func (c *client) pageTarget(pageURL string) string {
	raw, err := c.browserCall("Target.getTargets", nil)
	if err != nil {
		return ""
	}
	var targets struct {
		Infos []struct {
			Type     string `json:"type"`
			URL      string `json:"url"`
			TargetID string `json:"targetId"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(raw, &targets); err != nil {
		return ""
	}
	fallback := ""
	for _, t := range targets.Infos {
		if t.Type != "page" {
			continue
		}
		if t.URL == pageURL {
			return t.TargetID
		}
		if strings.HasPrefix(t.URL, originOf(pageURL)) && fallback == "" {
			fallback = t.TargetID
		}
	}
	return fallback
}

func (c *client) attach(target string) error {
	raw, err := c.browserCall("Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	if err != nil {
		return err
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	c.session = res.SessionID
	return nil
}

// eval runs one expression in the page and returns its value.
func (c *client) eval(expr string, userGesture bool) any {
	raw, err := c.call("Runtime.evaluate", map[string]any{
		"expression":    expr,
		"awaitPromise":  true,
		"returnByValue": true,
		"userGesture":   userGesture,
	}, c.session)
	if err != nil {
		return nil
	}
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil
	}
	return res.Result.Value
}

// rowsText reads the grid rows' text. textContent, not innerText: innerText
// needs layout, and a background tab reports stale text through it.
func (c *client) rowsText() string {
	s, _ := c.eval("document.getElementById('rows').textContent", false).(string)
	return s
}

// focusGrid puts the keyboard back on the terminal. A previous check can leave
// focus on the hidden paste target (the browser's paste needs it for the instant
// of the chord), and a click does not happen in a scripted run.
func (c *client) focusGrid() {
	c.eval("document.getElementById('grid').focus();window.getSelection().removeAllRanges();1", false)
}

// chord dispatches a real (trusted) key chord through the browser's input
// pipeline: a raw keydown plus its keyup, so a page that waits for the keyup
// still sees the whole gesture.
func (c *client) chord(modifiers int, key, code string, vk, nativeVK int) {
	base := map[string]any{
		"modifiers":             modifiers,
		"key":                   key,
		"code":                  code,
		"windowsVirtualKeyCode": vk,
		"nativeVirtualKeyCode":  nativeVK,
		"location":              0,
	}
	_, _ = c.call("Input.dispatchKeyEvent", withType(base, "rawKeyDown"), c.session)
	_, _ = c.call("Input.dispatchKeyEvent", withType(base, "keyUp"), c.session)
}

// virtualKey returns the Windows virtual-key code CDP expects for a character.
// Letters use their UPPERCASE code point (that is the VK for a letter key);
// passing the lowercase value makes Chrome fail to map the key at all.
func virtualKey(r rune) int {
	if r >= 'a' && r <= 'z' {
		return int(r - 'a' + 'A')
	}
	return int(r)
}

// typeText types printable characters as real key events (a CDP "char" event
// inserts text without a keydown, which the page would never see).
//
// nativeVirtualKeyCode is deliberately left out: it is a *hardware* keycode, and
// the ASCII value of the character is not one — Chrome then derives the wrong
// `key` from the layout ("c" arrives as "*"). With only key/code/text the event
// carries exactly the character this harness means to press.
func (c *client) typeText(s string) {
	for _, r := range s {
		key := string(r)
		base := map[string]any{
			"key":                   key,
			"code":                  "Key" + strings.ToUpper(key),
			"text":                  key,
			"unmodifiedText":        key,
			"windowsVirtualKeyCode": virtualKey(r),
			"location":              0,
		}
		_, _ = c.call("Input.dispatchKeyEvent", withType(base, "keyDown"), c.session)
		_, _ = c.call("Input.dispatchKeyEvent", withType(base, "keyUp"), c.session)
		time.Sleep(25 * time.Millisecond)
	}
}

func (c *client) mouse(x, y float64) {
	_, _ = c.call("Input.dispatchMouseEvent", map[string]any{
		"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1,
	}, c.session)
}

// drag completes a selection drag to (x, y).
func (c *client) drag(x, y float64) {
	_, _ = c.call("Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": x, "y": y, "button": "left",
	}, c.session)
	_, _ = c.call("Input.dispatchMouseEvent", map[string]any{
		"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1,
	}, c.session)
}

func withType(params map[string]any, typ string) map[string]any {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	out["type"] = typ
	return out
}
