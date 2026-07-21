// plugin.js — Provider Quota entry point.
//
// Tracks usage/quota for all configured providers. Registers:
//   /quota  (+ :refresh :json :auth-status :login:<p> :logout:<p>)
//   a status-bar segment tracking the active provider's quota
//   a hotkey (Ctrl+Shift+Q) to force-refresh quota data
//
// Architecture: JS owns polling + caching per the plan. Fetching happens in
// the refresh scheduler and on explicit commands; the status segment render
// only reads the cache (never fetches), so the footer stays non-blocking.
//
// Modules (require):
//   lib/format.js   — bars, percentages, durations, token formatting
//   lib/oauth.js    — device-code flow + token refresh
//   fetchers/*.js   — per-provider quota fetchers

var format = require("./lib/format.js");
var oauth = require("./lib/oauth.js");

// --- Fetcher registry -----------------------------------------------------

var _fetchers = {};       // id -> fetcher module
var _cache = {};          // id -> last quota result
var _lastFetch = {};      // id -> ms epoch of last fetch
var _fallbackId = "local";

function register(id, mod) {
	_fetchers[id] = mod;
}

// Load the built-in fetchers.
register("anthropic", require("./fetchers/anthropic.js"));
register("openai", require("./fetchers/openai.js"));
register("zai", require("./fetchers/zai.js"));
register("kimi", require("./fetchers/kimi.js"));
register("minimax", require("./fetchers/minimax.js"));
register("openrouter", require("./fetchers/openrouter.js"));
register(_fallbackId, require("./fetchers/local.js"));

// --- Provider config resolution ------------------------------------------

// providerConfigFor returns the {apiKey, baseUrl, ...} config map for a
// fetcher id, matching against goa.config().providers (keyed by config id).
// The fetcher id may differ from the config provider id (e.g. zai vs z.ai,
// kimi vs kimi-code), so we match on the provider identity field when
// present, else the id, with normalization + known aliases.
function providerConfigFor(fetcherId) {
	var providers = (goa.config() && goa.config().providers) || {};
	// Direct id match first.
	if (providers[fetcherId]) {
		return providers[fetcherId];
	}
	// Match on provider identity (config `provider:` field) — covers
	// "z.ai" config id mapping to the "zai" fetcher, "kimi-code" → "kimi", etc.
	var wanted = normalizeId(fetcherId);
	for (var key in providers) {
		var p = providers[key];
		if (!p) {
			continue;
		}
		var ident = (p.provider || p.id || key).toLowerCase();
		if (ident === fetcherId || normalizeId(ident) === wanted || fetcherAliases(wanted, normalizeId(ident))) {
			return p;
		}
	}
	return {};
}

// providerConfigured reports whether a fetcher id resolves to a real provider
// entry in goa.config().providers (any id/identity match), regardless of
// whether it has an API key. Used to decide whether a no_api_key quota row
// should be surfaced (configured but keyless) or hidden (not configured).
function providerConfigured(fetcherId) {
	var p = providerConfigFor(fetcherId);
	return p && (p.id || p.provider || p.endpoint || p.baseUrl);
}

// normalizeId strips dots/dashes so "z.ai" matches "zai".
function normalizeId(id) {
	return String(id).replace(/[.\-_]/g, "").toLowerCase();
}

// fetcherAliases reports whether a normalized config identity belongs to a
// normalized fetcher id. Covers branding variants the identity string alone
// cannot express: kimi-code/kimi-for-coding → kimi, moonshot → kimi;
// zai-coding/zai-coding-cn/zai-coding-plan → zai (same quota monitor).
function fetcherAliases(fetcher, ident) {
	if (fetcher === "kimi") {
		return ident === "kimicode" || ident === "kimiforcoding" || ident === "moonshot";
	}
	if (fetcher === "zai") {
		return ident === "zaicoding" || ident === "zaicodingcn" || ident === "zaicodingplan" || ident === "zhipu";
	}
	return false;
}

// sessionContext builds the ctx passed to fetchers: provider config + session
// usage snapshot.
function sessionContext(fetcherId) {
	return {
		config: providerConfigFor(fetcherId),
		session: goa.sessionUsage ? goa.sessionUsage() : {}
	};
}

// --- Refresh scheduler ----------------------------------------------------

// refreshDue fetches quota for one provider if its declared interval has
// elapsed; returns the (possibly stale) cached entry. Never fetches more
// often than the fetcher declares.
function refreshDue(fetcherId, force) {
	var fetcher = _fetchers[fetcherId];
	if (!fetcher) {
		return null;
	}
	var now = Date.now();
	var minInterval = fetcher.refreshInterval;
	if (minInterval === undefined || minInterval === null) {
		minInterval = 300000;
	}
	var last = _lastFetch[fetcherId] || 0;
	if (!force && (now - last) < minInterval) {
		return _cache[fetcherId] || null;
	}
	_lastFetch[fetcherId] = now;
	var result;
	try {
		result = fetcher.fetch(sessionContext(fetcherId));
	} catch (e) {
		result = { error: String(e), plan: null, limits: [] };
	}
	result._fetchedAt = now;
	_cache[fetcherId] = result;
	return result;
}

// refreshAllDue refreshes every provider whose interval elapsed (or all when
// force). OAuth providers without a token are refreshed once so their
// auth_required state is cached and shown in /quota, then skipped on later
// non-forced ticks (they'd just return auth_required again).
function refreshAllDue(force) {
	for (var id in _fetchers) {
		var fetcher = _fetchers[id];
		if (fetcher.quotaEndpoint === false) {
			// Local fallback: cheap, refresh every scheduler tick.
			refreshDue(id, force);
			continue;
		}
		if (fetcher.auth && fetcher.auth.type === "oauth" && !oauth.hasToken(id)) {
			// Cache the auth_required state once (so /quota can show it), then
			// skip until the user logs in or forces a refresh.
			if (force || !_cache[id]) {
				refreshDue(id, true);
			}
			continue;
		}
		refreshDue(id, force);
	}
}

// hasUsableCache reports whether the cache holds at least one provider entry
// (any state — data, auth_required, no_api_key counts as known; only a
// completely absent entry is unknown). Used by /quota to decide between an
// instant render and the async cold-start path.
function hasUsableCache() {
	for (var id in _fetchers) {
		if (_cache[id]) {
			return true;
		}
	}
	return false;
}

// cacheAuthRequiredStates caches the auth_required state for OAuth providers
// without a token. Cheap and HTTP-free (the fetcher short-circuits when the
// token is absent), so it runs synchronously even on the non-forced render
// path — preserving the /quota auth-required rows without re-introducing
// network blocking on a bare /quota.
function cacheAuthRequiredStates() {
	for (var id in _fetchers) {
		var fetcher = _fetchers[id];
		if (fetcher.auth && fetcher.auth.type === "oauth" && !oauth.hasToken(id) && !_cache[id]) {
			refreshDue(id, true);
		}
	}
}

// --- Status segment (cache read only) -------------------------------------

// activeFetcherId resolves which provider the status segment tracks: the
// currently active provider from goa config, mapped through the same
// normalization/alias rules as providerConfigFor. Falls back to the local
// (inferred) fetcher when the active provider has no quota API, so the
// footer still shows something meaningful (session tokens).
function activeFetcherId() {
	var active = (goa.config() && goa.config().activeProvider) || "";
	if (!active) {
		return _fallbackId;
	}
	var wanted = normalizeId(active);
	for (var id in _fetchers) {
		if (id === _fallbackId) {
			continue;
		}
		if (normalizeId(id) === wanted || fetcherAliases(normalizeId(id), wanted)) {
			return id;
		}
	}
	// The active provider id may be a config alias (e.g. "my-kimi") whose
	// identity field carries the real provider; match via providerConfigFor.
	for (var fid in _fetchers) {
		if (fid === _fallbackId) {
			continue;
		}
		var cfg = providerConfigFor(fid);
		if (cfg && cfg.id && normalizeId(cfg.id) === wanted) {
			return fid;
		}
	}
	// Active provider has no quota API (or none configured): local fallback.
	return _fallbackId;
}
// isLocalProvider reports whether the currently active provider is a genuine
// local provider: config provider type lm-studio / ollama / local, or a
// localhost/127.0.0.1 endpoint (mirrors Goa's own local detection). A
// NON-local provider that merely has no quota fetcher is NOT local — it must
// not show the infinity segment.
function isLocalProvider() {
	var active = (goa.config() && goa.config().activeProvider) || "";
	if (!active) {
		return false;
	}
	var providers = (goa.config() && goa.config().providers) || {};
	var p = providers[active];
	if (!p) {
		// The active id may be an alias: find the entry whose id matches.
		for (var key in providers) {
			if (normalizeId(key) === normalizeId(active)) {
				p = providers[key];
				break;
			}
		}
	}
	if (!p) {
		return false;
	}
	var ptype = (p.provider || "").toLowerCase();
	if (ptype === "local" || ptype === "lm-studio" || ptype === "lmstudio" || ptype === "ollama") {
		return true;
	}
	// No/unknown provider type: a localhost endpoint means it's local.
	var endpoint = (p.endpoint || p.baseUrl || "").toLowerCase();
	return endpoint.indexOf("localhost") >= 0 || endpoint.indexOf("127.0.0.1") >= 0;
}


// statusRender returns the compact quota segment for the footer, tracking
// ONLY the active provider. Local providers show "[∞]" (no quota). API
// providers show "[8%|24%]" (session|weekly), each percentage colored by its
// OWN projected window-end usage (green in-budget, orange close, red overrun,
// default when still pending) via goa.segmentColor. Reads the cache only —
// fetching is the scheduler's job.
function statusRender() {
	var id = activeFetcherId();
	if (!id) {
		return "";
	}
	var entry = _cache[id];
	if (!entry) {
		return { text: "[…]", color: "pending" };
	}
	if (entry.error) {
		if (entry.error === "auth_required") {
			return { text: "[∇ auth]", color: "warn" };
		}
		if (entry.error === "no_api_key") {
			return ""; // not configured for quota — stay silent
		}
		return { text: "[⚠]", color: "warn" };
	}
	// Local fallback: only genuine local providers show "[∞]". A NON-local
	// provider with no quota API must hide the segment entirely.
	if (entry.local) {
		if (!isLocalProvider()) {
			return ""; // unsupported non-local provider — remove the section
		}
		return { text: "[∞]", color: "ok" };
	}
	return colorizedSegment(entry);
}

// colorizedSegment builds "[8%|24%]" with each window's percentage wrapped in
// its own semantic color. Falls back to the single worst-window color when
// goa.segmentColor is unavailable (older hosts).
function colorizedSegment(entry) {
	var parts = [];
	for (var i = 0; i < entry.limits.length && parts.length < 2; i++) {
		var lim = entry.limits[i];
		if (!lim.limit || lim.limit <= 0) {
			continue;
		}
		parts.push({ pct: format.pct(lim.used, lim.limit) + "%", color: ratioColor(projectedRatio(lim)) });
	}
	if (parts.length === 0) {
		return "";
	}
	if (typeof goa.segmentColor !== "function") {
		// No per-part coloring: join plain and let the bridge apply one color.
		var plain = [];
		for (var j = 0; j < parts.length; j++) {
			plain.push(parts[j].pct);
		}
		return { text: "[" + plain.join("|") + "]", color: budgetColor(entry) };
	}
	var out = "[";
	for (var k = 0; k < parts.length; k++) {
		if (k > 0) {
			out += "|";
		}
		var hex = goa.segmentColor(parts[k].color);
		out += hex ? ansiWrap(hex, parts[k].pct) : parts[k].pct;
	}
	out += "]";
	return out; // plain string: bridge passes pre-colored text through
}

// ansiWrap wraps s in a 24-bit foreground color + reset (matches ansi.Fg).
function ansiWrap(hex, s) {
	var m = /^#?([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$/.exec(hex);
	if (!m) {
		return s;
	}
	return "\x1b[38;2;" + parseInt(m[1], 16) + ";" + parseInt(m[2], 16) + ";" + parseInt(m[3], 16) + "m" + s + "\x1b[0m";
}

// ratioColor maps a projected window-end ratio to a semantic color name.
function ratioColor(ratio) {
	if (ratio < 0) {
		return "pending";
	}
	if (ratio > 1.0) {
		return "critical";
	}
	if (ratio > 0.8) {
		return "warn";
	}
	return "ok";
}

// budgetColor estimates window-end usage from elapsed progress: green when
// the projected final usage stays comfortably under the limit, orange when
// close, red when the projection overruns. The worst window wins. "pending"
// when no window carries enough timing info to project.
function budgetColor(entry) {
	if (!entry.limits || entry.limits.length === 0) {
		return "pending";
	}
	var worst = -1;
	for (var i = 0; i < entry.limits.length; i++) {
		var lim = entry.limits[i];
		if (!lim.limit || lim.limit <= 0) {
			continue;
		}
		var ratio = projectedRatio(lim);
		if (ratio > worst) {
			worst = ratio;
		}
	}
	if (worst < 0) {
		return "pending"; // no bounded window to project from
	}
	if (worst > 1.0) {
		return "critical";
	}
	if (worst > 0.8) {
		return "warn";
	}
	return "ok";
}

// projectedRatio estimates the window-end usage fraction: used/limit scaled
// by the fraction of the window already elapsed (from resetsAt + periodMs).
// Without timing info it degrades to the raw used/limit fraction.
function projectedRatio(lim) {
	var raw = lim.used / lim.limit;
	var resetsAtMs = format.toMs(lim.resetsAt);
	if (!lim.periodMs || !resetsAtMs) {
		return raw;
	}
	var remaining = resetsAtMs - Date.now();
	var elapsed = lim.periodMs - remaining;
	if (elapsed <= 0 || elapsed >= lim.periodMs) {
		return raw;
	}
	var projected = lim.used / (elapsed / lim.periodMs);
	return projected / lim.limit;
}



// --- /quota command -------------------------------------------------------

// quotaCommand dispatches /quota[:sub[:arg]].
function quotaCommand(args) {
	var sub = args.length > 0 ? args[0] : "";
	var arg = args.length > 1 ? args[1] : "";
	switch (sub) {
		case "":
			// Bare /quota must never freeze the input line on provider HTTP
			// calls (bugs.md "Quota command unresponsive"). Warm cache →
			// instant render. Cold cache (plugin just loaded, scheduler tick
			// hasn't landed yet) → acknowledge immediately and fetch on a
			// timer goroutine, emitting the table via goa.output when done.
			if (!hasUsableCache()) {
				scheduleAsyncQuotaRender();
				return "Fetching quotas… results will appear when ready (usually a few seconds).";
			}
			return renderFull(false);
		case "refresh":
			refreshAllDue(true);
			goa.ui.refreshSegment("quota");
			return "Quota refreshed.";
		case "json":
			return renderJSON();
		case "auth-status":
			return renderAuthStatus();
		case "login":
			return loginProvider(arg);
		case "logout":
			return logoutProvider(arg);
		default:
			// /quota:<provider> → force-refresh just that provider and show it.
			if (_fetchers[sub]) {
				refreshDue(sub, true);
				return renderFull(false);
			}
			return "Unknown /quota subcommand: " + sub +
				"\nUsage: /quota[:refresh|:json|:auth-status|:login:<provider>|:logout:<provider>|:<provider>]";
	}
}

// scheduleAsyncQuotaRender fetches all providers on a scheduler timer
// goroutine (off the command path) and emits the rendered table into the chat
// viewport on completion. Coalesced: repeated cold /quota invocations while a
// fetch is in flight do not stack timers.
var _asyncQuotaPending = false;
function scheduleAsyncQuotaRender() {
	if (_asyncQuotaPending) {
		return;
	}
	_asyncQuotaPending = true;
	goa.setTimeout(function() {
		_asyncQuotaPending = false;
		refreshAllDue(true);
		goa.ui.refreshSegment("quota");
		goa.output(renderFull(false));
	}, 0);
}

// renderFull builds the full /quota breakdown as markdown: headings plus
// tables, rendered richly by goa's markdown pipeline (no console codes here).
// When force is true every provider is re-fetched synchronously first
// (explicit /quota:refresh); when false it renders from the cache only —
// fetching is the scheduler's job, so a bare /quota never blocks the input
// line on slow provider HTTP calls (bugs.md "Quota command unresponsive").
function renderFull(force) {
	if (force) {
		refreshAllDue(true);
	} else {
		cacheAuthRequiredStates();
	}
	var out = [];
	out.push("## Session Usage (current)");
	out.push("");
	out.push(renderSessionTable());
	out.push("");
	out.push("## Provider Quotas");
	out.push("");
	var rows = [];
	for (var id in _fetchers) {
		if (id === _fallbackId) {
			continue; // local rendered last
		}
		appendProviderRows(rows, id);
	}
	appendLocalRow(rows);
	// bugs.md "Quota": when the active provider has no quota API, say so
	// rather than silently showing only the local/inferred row.
	appendUnsupportedNote(out);
	if (rows.length === 0) {
		out.push("(no provider quota APIs configured)");
		return out.join("\n");
	}
	out.push("| Provider | Window | Usage | At reset | Resets in | Status |");
	out.push("| --- | --- | --- | ---: | --- | --- |");
	for (var i = 0; i < rows.length; i++) {
		out.push(rows[i]);
	}
	return out.join("\n");
}

// renderSessionTable renders the per-session token table from
// goa.sessionUsage as a markdown table.
function renderSessionTable() {
	var u = goa.sessionUsage ? goa.sessionUsage() : {};
	var lines = [];
	lines.push("| Msgs | Input | Output |");
	lines.push("| ---: | ---: | ---: |");
	lines.push("| " + (u.turns || 0) + " | " + format.tokens(u.input || 0) + " | " + format.tokens(u.output || 0) + " |");
	return lines.join("\n");
}

// appendProviderRows appends one markdown row per quota window for provider
// id, or a single status row for auth/error states. Providers with no usable
// data (not configured, never fetched) contribute nothing.
function appendProviderRows(rows, id) {
	var fetcher = _fetchers[id];
	var entry = _cache[id];
	if (!entry) {
		return;
	}
	var name = fetcher.name || id;
	if (entry.error) {
		if (entry.error === "no_api_key") {
			// Surface the reason instead of vanishing silently: a provider that
			// is configured (present in goa.config().providers) but has no key
			// must tell the user *why* it has no quota row, otherwise it looks
			// like z.ai is "not supported" (bugs.md: z.ai not visible in /quota).
			if (providerConfigured(id)) {
				rows.push("| " + name + " | — | — | — | — | no API key — set via `/login " + id + "` |");
			}
			return;
		}
		if (entry.error === "auth_required") {
			rows.push("| " + name + " | — | — | — | — | auth required — `/quota:login:" + id + "` |");
			return;
		}
		rows.push("| " + name + " | — | — | — | — | error: " + entry.error + " |");
		return;
	}
	var display = entry.plan ? name + " (" + entry.plan + ")" : name;
	for (var i = 0; i < entry.limits.length; i++) {
		rows.push(renderLimitRow(display, entry.limits[i], entry));
	}
}

// renderLimitRow renders one quota window as a markdown table row:
// "| Kimi (Advanced) | Session (5h) | ██░░ 8% | 8% | +1h 36m | plenty of room |".
// Usage merges the bar + current % (the redundant "4/100" numbers and the
// separate % column are gone). "At reset" projects the % at window end from
// the current pace. "Status" is the per-window level in words.
// Cost windows (entry.costUnit === "cents") render dollar amounts.
function renderLimitRow(display, lim, entry) {
	var reset = lim.resetsAt ? format.durationUntil(lim.resetsAt) : "—";
	var isCost = entry.costUnit === "cents";
	if (!lim.limit || lim.limit <= 0) {
		// Unbounded / accumulated (e.g. local tokens, OpenAI cost).
		var val = isCost
			? format.cost(lim.used / 100)
			: format.tokens(lim.used);
		return "| " + display + " | " + lim.label + " | " + val + " | — | " + reset + " | — |";
	}
	var p = format.pct(lim.used, lim.limit);
	var usage = format.bar(p, 8) + " " + p + "%";
	var atReset = atResetPct(lim);
	return "| " + display + " | " + lim.label + " | " + usage + " | " + atReset + " | " + reset + " | " + windowStatus(lim) + " |";
}

// atResetPct returns the projected usage % at window reset (e.g. "8%"),
// derived from the same pace projection as the footer color. Falls back to the
// raw current % when there is not enough timing info to project.
function atResetPct(lim) {
	return Math.round(projectedRatio(lim) * 100) + "%";
}

// windowStatus returns the per-window budget level in words ("plenty of
// room", "close to limit", "over budget"), matching the footer color for that
// window's projected window-end usage.
function windowStatus(lim) {
	var r = projectedRatio(lim);
	if (r > 1.0) {
		return "over budget";
	}
	if (r > 0.8) {
		return "close to limit";
	}
	return "plenty of room";
}

// appendLocalRow appends the local/inferred fallback row.
function appendLocalRow(rows) {
	var entry = _cache[_fallbackId];
	var used = 0;
	if (entry && entry.limits && entry.limits.length > 0) {
		used = entry.limits[0].used;
	}
	rows.push("| Local (inferred) | Session tokens | " + format.tokens(used) + " | — | — | — |");
}

// appendUnsupportedNote adds a "quota not supported" note to the /quota output
// when the active provider resolved to the local fallback (i.e. it has no
// quota API), so the user understands why no real quota window is shown.
function appendUnsupportedNote(out) {
	var active = (goa.config() && goa.config().activeProvider) || "";
	if (active === "") {
		return;
	}
	if (activeFetcherId() === _fallbackId) {
		out.push("");
		out.push("_Quota tracking is not supported for provider `" + active + "` — showing local session tokens._");
	}
}

// renderJSON emits machine-readable quota data.
function renderJSON() {
	refreshAllDue(true);
	var out = { providers: {}, session: goa.sessionUsage ? goa.sessionUsage() : {} };
	for (var id in _cache) {
		var e = _cache[id];
		out.providers[id] = {
			name: (_fetchers[id] && _fetchers[id].name) || id,
			plan: e.plan || null,
			error: e.error || null,
			limits: e.limits || [],
			fetchedAt: e._fetchedAt || 0
		};
	}
	return JSON.stringify(out, null, 2);
}

// renderAuthStatus lists each provider's quota auth state as a markdown table.
function renderAuthStatus() {
	var rows = [];
	for (var id in _fetchers) {
		var f = _fetchers[id];
		if (!f.auth || f.auth.type === "none") {
			continue;
		}
		var state;
		if (f.auth.type === "api_key") {
			state = providerConfigFor(id).apiKey ? "api key ✓" : "no api key ∇";
		} else {
			state = oauth.hasToken(id) ? "authenticated ✓" : "not authenticated ∇";
		}
		rows.push("| " + (f.name || id) + " | " + state + " |");
	}
	if (rows.length === 0) {
		return "(no providers with quota auth configured)";
	}
	var out = ["## Quota auth status", "", "| Provider | State |", "| --- | --- |"];
	for (var i = 0; i < rows.length; i++) {
		out.push(rows[i]);
	}
	return out.join("\n");
}

// loginProvider starts the OAuth device flow for a provider.
function loginProvider(id) {
	if (!id) {
		return "Usage: /quota:login:<provider>";
	}
	var fetcher = _fetchers[id];
	if (!fetcher) {
		return "Unknown provider: " + id;
	}
	if (!fetcher.auth || fetcher.auth.type !== "oauth") {
		return (fetcher.name || id) + " uses API-key auth — no login needed (set the key in config).";
	}
	goa.output("Starting OAuth login for " + (fetcher.name || id) + "…");
	oauth.startDeviceFlow(id, fetcher.auth, function(err) {
		if (err) {
			goa.output("Login failed for " + id + ": " + err);
			return;
		}
		goa.output("Authenticated " + (fetcher.name || id) + ". Run /quota to see usage.");
		refreshDue(id, true);
		goa.ui.refreshSegment("quota");
	});
	return "Opening browser for " + (fetcher.name || id) + " authorization…";
}

// logoutProvider clears stored OAuth tokens for a provider.
function logoutProvider(id) {
	if (!id) {
		return "Usage: /quota:logout:<provider>";
	}
	if (!_fetchers[id]) {
		return "Unknown provider: " + id;
	}
	oauth.logout(id);
	delete _cache[id];
	goa.ui.refreshSegment("quota");
	return "Logged out " + (_fetchers[id].name || id) + ".";
}

// --- Registration ---------------------------------------------------------

goa.registerCommand({
	name: "quota",
	shortHelp: "Show provider usage/quota breakdown",
	longHelp: "Usage: /quota[:sub]\n\n" +
		"  /quota                 Full session + provider quota breakdown\n" +
		"  /quota:refresh         Force-refresh all provider quotas\n" +
		"  /quota:json            Machine-readable JSON output\n" +
		"  /quota:auth-status     Show per-provider auth state\n" +
		"  /quota:login:<id>      OAuth device login (OAuth providers only)\n" +
		"  /quota:logout:<id>     Clear stored OAuth tokens\n" +
		"  /quota:<id>            Force-refresh one provider",
	run: quotaCommand
});

goa.ui.addSegment({
	id: "quota",
	priority: 10,
	render: function() {
		var seg = statusRender();
		if (!seg) {
			return "";
		}
		return seg;
	}
});

goa.registerHotkey({
	key: "q",
	ctrl: true,
	shift: true,
	description: "Refresh provider quota",
	handler: function() {
		refreshAllDue(true);
		goa.ui.refreshSegment("quota");
	}
});

// The refresh scheduler fetches due quotas every 60s and repaints the
// segment. No carousel: the segment tracks only the active provider, so a
// rotation timer would just churn the footer.
goa.setInterval(function() {
	refreshAllDue(false);
	goa.ui.refreshSegment("quota");
}, 60000);

// Prime the cache so the first segment render has data. Runs on a timer
// goroutine, NOT synchronously at load: provider HTTP calls must never block
// plugin startup (a slow/hanging endpoint would freeze the whole app boot
// and delay the first /quota behind the load path — bugs.md "Quota command
// unresponsive").
goa.setTimeout(function() {
	refreshAllDue(false);
	goa.ui.refreshSegment("quota");
}, 0);