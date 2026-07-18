// plugin.js — Provider Quota entry point.
//
// Tracks usage/quota for all configured providers. Registers:
//   /quota  (+ :refresh :json :auth-status :login:<p> :logout:<p>)
//   a status-bar segment with a rotating quota carousel
//   a hotkey (Ctrl+Shift+Q) to force-refresh quota data
//
// Architecture: JS owns polling + caching per the plan. Fetching happens in
// the refresh scheduler and on explicit commands; the status segment render
// only reads the cache (never fetches), so the footer stays non-blocking.
//
// Modules (require):
//   lib/format.js   — bars, percentages, durations, token formatting
//   lib/oauth.js    — device-code flow + token refresh
//   lib/carousel.js — status segment rotation
//   fetchers/*.js   — per-provider quota fetchers

var format = require("./lib/format.js");
var oauth = require("./lib/oauth.js");
var carouselLib = require("./lib/carousel.js");

// --- Fetcher registry -----------------------------------------------------

var _fetchers = {};       // id -> fetcher module
var _cache = {};          // id -> last quota result
var _lastFetch = {};      // id -> ms epoch of last fetch
var _providers = [];      // ordered ids with data, for the carousel
var _carousel = new carouselLib.Carousel(3000);
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
register("opencode", require("./fetchers/opencode.js"));
register(_fallbackId, require("./fetchers/local.js"));

// --- Provider config resolution ------------------------------------------

// providerConfigFor returns the {apiKey, baseUrl, ...} config map for a
// fetcher id, matching against goa.config().providers (keyed by config id).
// The fetcher id may differ from the config provider id (e.g. zai vs z.ai),
// so we match on the provider identity field when present, else the id.
function providerConfigFor(fetcherId) {
	var providers = (goa.config() && goa.config().providers) || {};
	// Direct id match first.
	if (providers[fetcherId]) {
		return providers[fetcherId];
	}
	// Match on provider identity (config `provider:` field) — covers
	// "z.ai" config id mapping to the "zai" fetcher, "kimi", etc.
	for (var key in providers) {
		var p = providers[key];
		if (!p) {
			continue;
		}
		var ident = (p.provider || p.id || key).toLowerCase();
		if (ident === fetcherId || ident === normalizeId(fetcherId)) {
			return p;
		}
	}
	return {};
}

// normalizeId strips dots/dashes so "z.ai" matches "zai".
function normalizeId(id) {
	return String(id).replace(/[.\-_]/g, "").toLowerCase();
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
	refreshProviderList();
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

// refreshProviderList rebuilds the ordered provider list for the carousel:
// API-backed providers with data (no error, has limits), most-recently-
// fetched first. The local fallback only joins the carousel when no real
// provider has data — it's the fallback, not a rotation peer.
function refreshProviderList() {
	var list = [];
	for (var id in _cache) {
		if (id === _fallbackId) {
			continue;
		}
		var entry = _cache[id];
		if (entry && !entry.error && entry.limits && entry.limits.length > 0) {
			list.push(id);
		}
	}
	list.sort(function(a, b) { return (_cache[b]._fetchedAt || 0) - (_cache[a]._fetchedAt || 0); });
	// Fall back to local only when nothing else has data.
	if (list.length === 0 && _cache[_fallbackId] && !_cache[_fallbackId].error) {
		list.push(_fallbackId);
	}
	_providers = list;
}

// --- Status segment (cache read only) -------------------------------------

// statusRender returns the compact quota text for the footer. Reads the cache
// only — fetching is the scheduler's job. Empty string when nothing to show.
function statusRender() {
	if (_providers.length === 0) {
		return "";
	}
	var idx = _carousel.current() % _providers.length;
	var id = _providers[idx];
	var entry = _cache[id];
	if (!entry) {
		return "";
	}
	var text = formatShort(id, entry);
	// Prefix with provider label when cycling through multiple providers.
	if (_providers.length > 1) {
		var name = (_fetchers[id] && _fetchers[id].name) || id;
		text = name + " " + text;
	}
	return text;
}

// formatShort renders "5h:42% / 5d:30%" (windowed) or "142K tok" (local).
function formatShort(id, entry) {
	if (entry.local) {
		var used = entry.limits[0] ? entry.limits[0].used : 0;
		return format.tokens(used) + " tok";
	}
	var parts = [];
	for (var i = 0; i < entry.limits.length && parts.length < 2; i++) {
		var lim = entry.limits[i];
		if (!lim.limit || lim.limit <= 0) {
			continue;
		}
		var tag = shortTag(lim.label);
		parts.push(tag + ":" + format.pct(lim.used, lim.limit) + "%");
	}
	return parts.join(" / ");
}

// shortTag maps a limit label to a compact tag: "Session (5h)" → "5h".
function shortTag(label) {
	var m = String(label).match(/\(([^)]+)\)/);
	if (m) {
		return m[1];
	}
	var lower = String(label).toLowerCase();
	if (lower.indexOf("week") >= 0) {
		return "wk";
	}
	if (lower.indexOf("session") >= 0) {
		return "sess";
	}
	if (lower.indexOf("month") >= 0) {
		return "mo";
	}
	return String(label).substring(0, 4);
}

// authMark returns the auth indicator for the segment: "✓" logged in,
// "∇" needs re-auth, "" for no-quota-API/local providers.
function authMark(id) {
	var fetcher = _fetchers[id];
	if (!fetcher || !fetcher.auth || fetcher.auth.type === "none") {
		return "";
	}
	if (fetcher.auth.type === "api_key") {
		return providerConfigFor(id).apiKey ? " ✓" : " ∇";
	}
	// OAuth
	return oauth.hasToken(id) ? " ✓" : " ∇";
}

// --- /quota command -------------------------------------------------------

// quotaCommand dispatches /quota[:sub[:arg]].
function quotaCommand(args) {
	var sub = args.length > 0 ? args[0] : "";
	var arg = args.length > 1 ? args[1] : "";
	switch (sub) {
		case "":
			return renderFull();
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
				return renderFull();
			}
			return "Unknown /quota subcommand: " + sub +
				"\nUsage: /quota[:refresh|:json|:auth-status|:login:<provider>|:logout:<provider>|:<provider>]";
	}
}

// renderFull builds the full /quota breakdown.
function renderFull() {
	refreshAllDue(true);
	var out = [];
	out.push("Session Usage (current)");
	out.push(renderSessionTable());
	out.push("");
	out.push("Provider Quotas:");
	var shown = 0;
	for (var id in _fetchers) {
		if (id === _fallbackId) {
			continue; // local rendered last
		}
		var block = renderProviderBlock(id);
		if (block) {
			out.push(block);
			shown++;
		}
	}
	if (shown === 0) {
		out.push("  (no provider quota APIs configured)");
	}
	// Local fallback always last.
	out.push(renderLocalBlock());
	return out.join("\n");
}

// renderSessionTable renders the per-session token/cost table from
// goa.sessionUsage.
function renderSessionTable() {
	var u = goa.sessionUsage ? goa.sessionUsage() : {};
	var input = u.input || 0;
	var output = u.output || 0;
	var turns = u.turns || 0;
	var lines = [];
	lines.push("  Msgs      Input      Output");
	lines.push("  " + format.pad(turns, 6) + format.pad(format.tokens(input), 11) + format.tokens(output));
	return lines.join("\n");
}

// renderProviderBlock renders one provider's quota block, or "" when the
// provider has no usable data (not configured / auth missing and never
// fetched).
function renderProviderBlock(id) {
	var fetcher = _fetchers[id];
	var entry = _cache[id];
	if (!entry) {
		return "";
	}
	var name = fetcher.name || id;
	var lines = [];
	if (entry.error) {
		if (entry.error === "auth_required") {
			lines.push("  " + name + "  — auth required (run /quota:login:" + id + ")");
			return lines.join("\n");
		}
		if (entry.error === "no_api_key") {
			return ""; // not configured; skip quietly
		}
		lines.push("  " + name + "  — error: " + entry.error);
		return lines.join("\n");
	}
	var planSuffix = entry.plan ? " (" + entry.plan + ")" : "";
	lines.push("  " + name + planSuffix);
	for (var i = 0; i < entry.limits.length; i++) {
		lines.push(renderLimitLine(entry.limits[i], entry));
	}
	return lines.join("\n");
}

// renderLimitLine renders one "Label  ████░░  42%  → +1h 48m" line.
function renderLimitLine(lim, entry) {
	var label = format.pad(lim.label, 16);
	if (!lim.limit || lim.limit <= 0) {
		// Unbounded / accumulated (e.g. local tokens, OpenAI cost).
		var val = entry.costUnit === "cents"
			? format.cost(lim.used / 100)
			: format.tokens(lim.used);
		return "    " + label + val + (lim.resetsAt ? "  → " + format.durationUntil(lim.resetsAt) : "");
	}
	var p = format.pct(lim.used, lim.limit);
	var reset = lim.resetsAt ? "  → " + format.durationUntil(lim.resetsAt) : "";
	return "    " + label + format.bar(p, 10) + "  " + format.padLeft(p + "%", 4) + reset;
}

// renderLocalBlock renders the local/inferred fallback block.
function renderLocalBlock() {
	var entry = _cache[_fallbackId];
	if (!entry || !entry.limits || entry.limits.length === 0) {
		return "  Local (inferred)  — no quota API\n    Tokens used     0";
	}
	var used = entry.limits[0].used;
	return "  Local (inferred)  — no quota API\n    Tokens used     " + format.tokens(used) + " / unlimited";
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

// renderAuthStatus lists each OAuth provider and its auth state.
function renderAuthStatus() {
	var lines = ["Quota auth status:"];
	var any = false;
	for (var id in _fetchers) {
		var f = _fetchers[id];
		if (!f.auth || f.auth.type === "none") {
			continue;
		}
		any = true;
		var state;
		if (f.auth.type === "api_key") {
			state = providerConfigFor(id).apiKey ? "api key ✓" : "no api key ∇";
		} else {
			state = oauth.hasToken(id) ? "authenticated ✓" : "not authenticated ∇";
		}
		lines.push("  " + format.pad(f.name || id, 12) + state);
	}
	if (!any) {
		lines.push("  (no providers with quota auth configured)");
	}
	return lines.join("\n");
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
	refreshProviderList();
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
		"  /quota:login:<id>      OAuth device login (opencode, kimi)\n" +
		"  /quota:logout:<id>     Clear stored OAuth tokens\n" +
		"  /quota:<id>            Force-refresh one provider",
	run: quotaCommand
});

goa.ui.addSegment({
	id: "quota",
	priority: 10,
	render: function() {
		var text = statusRender();
		if (!text) {
			return "";
		}
		return text;
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

// Start the carousel (rotates the segment across providers) and the refresh
// scheduler (fetches due quotas every 60s). Initial fetch kicks immediately.
_carousel.start(function() {
	goa.ui.refreshSegment("quota");
	return _providers.length;
});

goa.setInterval(function() {
	refreshAllDue(false);
	goa.ui.refreshSegment("quota");
}, 60000);

// Prime the cache so the first segment render has data.
refreshAllDue(false);
