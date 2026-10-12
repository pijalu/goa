// plugin.js — Provider Quota entry point.
//
// Tracks usage/quota for all configured providers. Registers:
//   /quota  (+ :refresh :json :auth-status :resets :reset[:<id>]
//            :login:<p> :logout:<p>)
//   a status-bar segment tracking the active provider's quota
//   a hotkey (Ctrl+Shift+Q) to force-refresh quota data
//   an observer that hints available Codex rate-limit resets when a
//   Codex-ish model hits a classified rate limit (plugins plan §6; debounced)
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
// _resetSurfaces holds the provider RESET surfaces (quota credits that clear
// an exhausted window), keyed by the fetcher id whose quota they belong to.
// A surface is not a fetcher: it has no plan/limits of its own and never
// contributes a row to /quota — the owning fetcher rides its status along and
// the commands below drive the claim/consume calls.
var _resetSurfaces = {};

function register(id, mod) {
	_fetchers[id] = mod;
}

function registerResetSurface(fetcherId, mod) {
	_resetSurfaces[fetcherId] = mod;
}

// Load the built-in fetchers.
register("anthropic", require("./fetchers/anthropic.js"));
register("openai", require("./fetchers/openai.js"));
register("codex", require("./fetchers/codex.js"));
register("zai", require("./fetchers/zai.js"));
register("kimi", require("./fetchers/kimi.js"));
register("minimax", require("./fetchers/minimax.js"));
register("openrouter", require("./fetchers/openrouter.js"));
register("opencode", require("./fetchers/opencode.js"));
register(_fallbackId, require("./fetchers/local.js"));

// The z.ai Coding Plan reset surface (status/opportunity/use/history-read,
// bugs.md #7). The require cache hands zai.js and plugin.js the SAME instance,
// so a cooldown persisted by one is honored by the other.
registerResetSurface("zai", require("./fetchers/zai-coding-plan.js"));

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
	if (fetcher === "codex") {
		return ident === "codex" || ident === "openaicodex" || ident === "openai";
	}
	if (fetcher === "opencode") {
		// Both OpenCode variants (Zen "opencode" and Go "opencode-go") share
		// the OPENCODE_API_KEY and the usage endpoint under their base URL.
		return ident === "opencode" || ident === "opencodego" || ident === "opencodezen";
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
// force). Providers without authentication are refreshed once so their
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
		if (fetcher.auth && !authAvailable(id, fetcher)) {
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
function goaOAuthAvailable(fetcher) {
	if (!fetcher || !fetcher.auth || fetcher.auth.type !== "goa_oauth") return true;
	if (!goa.auth || typeof goa.auth.oauthToken !== "function") return false;
	var token = goa.auth.oauthToken(fetcher.auth.provider);
	return !!(token && token.accessToken);
}

function authAvailable(fetcherId, fetcher) {
	if (!fetcher || !fetcher.auth) return true;
	if (fetcher.auth.type === "oauth") return oauth.hasToken(fetcherId);
	if (fetcher.auth.type === "goa_oauth") return goaOAuthAvailable(fetcher);
	return true;
}

function cacheAuthRequiredStates() {
	for (var id in _fetchers) {
		var fetcher = _fetchers[id];
		if (fetcher.auth && !authAvailable(id, fetcher) && !_cache[id]) {
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
// providers show "[8%|24%]" or "[8%|24%|85%]" (session|weekly|monthly —
// three windows when the provider reports a monthly limit, as opencode-go
// does), each percentage colored by its OWN projected window-end usage
// (green in-budget, orange close, red overrun, default when still pending)
// via goa.segmentColor. Reads the cache only — fetching is the scheduler's
// job.
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
	return withCodingPlanResets(colorizedSegment(entry), entry);
}

// withCodingPlanResets appends the available Coding Plan reset count to the
// z.ai quota segment ("[38%|62%]+1↻"). A present-but-throttled status renders
// nothing extra: a cooldown is not quota, and the retry boundary already
// shows up on /quota:resets. Non-object segments (the no-color host fallback)
// get the same suffix in plain text.
function withCodingPlanResets(seg, entry) {
	if (!seg) {
		return seg;
	}
	var cp = entry && entry.codingPlan;
	var n = cp && !cp.throttled ? cp.availableCount : 0;
	if (!n) {
		return seg;
	}
	var suffix = "+" + n + "↻";
	if (typeof seg === "string") {
		return seg + " " + suffix;
	}
	return { text: seg.text + " " + suffix, color: seg.color };
}

// colorizedSegment builds "[8%|24%]" (or "[8%|24%|85%]" when the provider
// reports three windows, as opencode-go's monthly limit) with each window's
// percentage wrapped in its own semantic color. Windows are sorted
// shortest-period first (session|weekly|monthly) — fetcher emission order is
// NOT guaranteed (the z.ai monitor API returns monthly first). At most three
// windows are shown: providers reporting more (shouldn't happen today) would
// overflow the footer. A window at 0% is omitted entirely — an untouched
// quota is noise ([0%|71%|0%] reads [71%]). Falls back to the single
// worst-window color when goa.segmentColor is unavailable (older hosts).
function colorizedSegment(entry) {
	var parts = [];
	// Sort a copy: the cached limits array is shared with /quota, which keeps
	// the provider's own row order. Windows without a known period sort last.
	var sorted = entry.limits.slice().sort(function(a, b) {
		return (a.periodMs || 1e15) - (b.periodMs || 1e15);
	});
	for (var i = 0; i < sorted.length && parts.length < 3; i++) {
		var lim = sorted[i];
		if (!lim.limit || lim.limit <= 0) {
			continue;
		}
		var pct = format.pct(lim.used, lim.limit);
		if (pct === 0) {
			continue; // untouched window — hide, don't render 0%
		}
		parts.push({ pct: pct + "%", color: ratioColor(projectedRatio(lim)) });
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

// runOutAtMs returns the epoch-ms instant at which the current pace consumes the
// window's remaining budget, or null when it does not run out before the window
// resets (or when there is not enough timing info to project at all).
//
// It is the same projection as projectedRatio, read the other way round: at the
// current pace the window would end with `projected` used instead of `limit`, so
// the budget is consumed after the matching fraction of the window. Deriving it
// from the ratio (rather than from a second rate calculation) keeps the word
// "over budget" and the time next to it from ever disagreeing.
function runOutAtMs(lim) {
	if (!lim.limit || lim.limit <= 0) {
		return null;
	}
	var resetsAtMs = format.toMs(lim.resetsAt);
	if (!lim.periodMs || !resetsAtMs) {
		return null;
	}
	var projected = projectedRatio(lim);
	if (!(projected > 1.0)) {
		return null;
	}
	var windowStart = resetsAtMs - lim.periodMs;
	return windowStart + lim.periodMs * (1 / projected);
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
		case "resets":
			return quotaResetsCommand(arg);
		case "reset":
			return quotaResetCommand(arg);
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
	// Reset-credit surfacing: with cached DETAILS the breakdown renders the
	// full credits table inline (user-reported gap: details used to live only
	// behind /quota:resets); without them the count-only how-to note is the
	// fallback. Both name the command that consumes a credit.
	var note = resetUsageNote();
	if (note !== "") {
		out.push("");
		out.push(note);
	}
	var detailsSection = codexDetailsSection();
	if (detailsSection !== "") {
		out.push("");
		out.push(detailsSection);
	}
	return out.join("\n");
}

// codexDetailsSection renders the cached reset-credit details as an inline
// /quota section. Empty unless the codex snapshot carries a successful
// details payload with at least one credit row — otherwise the count-only
// note above covers the state.
function codexDetailsSection() {
	var entry = _cache.codex;
	if (!entry || entry.error || !entry.details) {
		return "";
	}
	var d = entry.details;
	if (!Array.isArray(d.credits) || d.credits.length === 0) {
		return "";
	}
	return renderResetsTable(d);
}

// --- z.ai Coding Plan resets (status / opportunity / use) -------------------

// zaiResetSurface returns the z.ai Coding Plan reset module when the loaded
// surface exposes the full call set, else null (older build).
function zaiResetSurface() {
	var s = _resetSurfaces.zai;
	if (!s || typeof s.status !== "function" || typeof s.useReset !== "function") {
		return null;
	}
	return s;
}

// codingPlanRow renders the one-line /quota summary of available Coding Plan
// resets ("N available (5h 1 · week 1)"). Empty when the snapshot carries no
// status, when nothing is available, or while the API is throttling — a
// cooldown is state, not quota, and belongs on /quota:resets. A FAILED status
// renders its reason instead (see codingPlanFailureLine): reporting "0
// available" for a fetch that never authenticated is a false reading of the
// account, not an absence of resets.
function codingPlanRow(display, cp) {
	if (!cp) {
		return "";
	}
	if (cp.error && !cp.throttled) {
		return "| " + display + " | Coding Plan Resets | " +
			resetFailureSummary(resetFailureReason(cp)) + " | — | — | — |";
	}
	if (cp.throttled) {
		return "";
	}
	var five = cp.availableFiveHour ? cp.availableFiveHour.length : 0;
	var week = cp.availableWeek ? cp.availableWeek.length : 0;
	if (five + week === 0) {
		return "";
	}
	return "| " + display + " | Coding Plan Resets | " + (five + week) +
		" available (" + five + " × 5h · " + week + " × week) | — | — | `/quota:reset` |";
}

// resetFailureReason returns the most specific failure code available. An auth
// failure carries the generic "auth_required" in `error` and the actionable
// wire-level reason in `reason` ("coding_plan_reset_zcode_jwt_required"); the
// generic code alone cannot be acted on, so `reason` wins when present.
function resetFailureReason(cp) {
	return String((cp && cp.reason) || (cp && cp.error) || "");
}

// RESET_FAILURE_HELP maps a reset-surface failure reason to the one action
// that fixes it. The reasons are stable wire-level codes, so the mapping is a
// lookup rather than prose: an unmapped reason still renders its raw code, so
// a new backend code degrades to "unhelpful but honest" instead of blank.
var RESET_FAILURE_HELP = {
	coding_plan_reset_zcode_jwt_required:
		"set providers[].extra.zcodeJwt (the zcode business JWT)",
	coding_plan_reset_maas_jwt_required:
		"set providers[].extra.accessToken (the z.ai/bigmodel OAuth access token)"
};

// resetFailureSummary renders a reset-surface failure as a short table cell:
// the stable reason code plus the remedy.
function resetFailureSummary(reason) {
	var help = RESET_FAILURE_HELP[reason];
	return help ? "unavailable — " + reason + " (" + help + ")" : "unavailable — " + reason;
}

// codingPlanFailureLine renders the /quota:resets explanation for a status
// that failed to load. Without it the reset table rendered "| 5-hour | 0 |",
// which reads as "you have no resets" when the truth is "we could not ask".
function codingPlanFailureLine(cp) {
	if (!cp || !cp.error || cp.throttled) {
		return "";
	}
	var reason = resetFailureReason(cp);
	var help = RESET_FAILURE_HELP[reason];
	return "Reset status unavailable — `" + reason + "`" +
		(help ? ". Fix: " + help + "." : "") +
		" Resets you hold are NOT counted above.";
}

// renderZaiCodingPlanSection renders the /quota:resets block for z.ai:
// available 5-hour / week resets with their expiries, the last-used history,
// and — when the server is throttling — the retry boundary it handed us.
// Empty when z.ai is not configured or its status is unavailable.
function renderZaiCodingPlanSection() {
	if (!zaiResetSurface()) {
		return "";
	}
	var cp = refreshZaiCodingPlan();
	if (!cp) {
		return "";
	}
	if (cp.throttled) {
		return "## z.ai Coding Plan Resets\n\n" +
			"Throttled by the API — next attempt " + cooldownText(cp) + ".";
	}
	var failLine = codingPlanFailureLine(cp);
	var out = ["## z.ai Coding Plan Resets", ""];
	// A failed status has no honest table to draw: every cell would be a
	// fabricated zero. Say so instead.
	if (failLine) {
		out.push(failLine);
		return out.join("\n");
	}
	out.push("| Reset | Available | Earliest expiry | Last used |");
	out.push("| --- | ---: | --- | --- |");
	out.push(codingPlanLine("5-hour", cp.availableFiveHour, cp.latestFiveHourUsedAtMs));
	out.push(codingPlanLine("Week", cp.availableWeek, cp.latestWeekUsedAtMs));
	if (cp.hasUnreadHistory) {
		out.push("");
		out.push("Unread reset history — `/quota:resets:read` clears the marker.");
	}
	out.push("");
	out.push("Use one with `/quota:reset[:week]` · claim a new one with `/quota:reset:claim`.");
	return out.join("\n");
}

// codingPlanLine renders one reset-type row of the Coding Plan table.
function codingPlanLine(label, list, lastUsedMs) {
	var items = Array.isArray(list) ? list : [];
	return "| " + label + " | " + items.length + " | " +
		(items.length > 0 ? expiryText(earliestExpiry(items)) : "—") + " | " +
		(lastUsedMs > 0 ? format.durationUntil(lastUsedMs) + " ago" : "never") + " |";
}

// earliestExpiry returns the soonest expiry of a reset list, or 0 when empty.
function earliestExpiry(items) {
	var best = 0;
	for (var i = 0; i < items.length; i++) {
		var ms = numMs(items[i].expireAtMs);
		if (ms > 0 && (best === 0 || ms < best)) {
			best = ms;
		}
	}
	return best;
}

// cooldownText renders a throttle boundary as relative time, keeping the
// server's own next_try_at when it sent one (that value is the retry boundary
// the backend enforces; see fetchers/zai-coding-plan.js).
function cooldownText(cp) {
	var ms = cp.cooldownUntil > 0 ? cp.cooldownUntil : 0;
	if (ms === 0) {
		return "shortly";
	}
	var rel = format.durationUntil(ms);
	return rel === "" ? "shortly" : "in " + rel;
}

// refreshZaiCodingPlan force-refreshes zai (which rides the reset status
// along with the monitor quota) and returns the fresh status, or null when the
// provider is unconfigured or the reset API is unreachable.
function refreshZaiCodingPlan() {
	if (!providerConfigured("zai")) {
		return null;
	}
	refreshDue("zai", true);
	var entry = _cache.zai;
	if (!entry || entry.error) {
		return null;
	}
	return entry.codingPlan || null;
}

// numMs coerces a value to a number (0 when absent/unparsable). The values it
// sees here — reset expiries — are timestamps, hence the name.
function numMs(v) {
	var n = Number(v);
	return isNaN(n) ? 0 : n;
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
			var loginHint = fetcher.auth && fetcher.auth.type === "goa_oauth"
				? "`/login:openai:oauth`"
				: "`/quota:login:" + id + "`";
			rows.push("| " + name + " | — | — | — | — | auth required — " + loginHint + " |");
			return;
		}
		rows.push("| " + name + " | — | — | — | — | error: " + entry.error + " |");
		return;
	}
	var display = entry.plan ? name + " (" + entry.plan + ")" : name;
	for (var i = 0; i < entry.limits.length; i++) {
		rows.push(renderLimitRow(display, entry.limits[i], entry));
	}
	if (Array.isArray(entry.lines)) {
		for (var j = 0; j < entry.lines.length; j++) {
			rows.push("| " + display + " | " + entry.lines[j].label + " | " + entry.lines[j].value + " | — | — | — |");
		}
	}
	// Reset-credit row: the z.ai snapshot rides its Coding Plan reset status
	// along, so the available 5-hour / week resets show in the main table
	// (bugs.md #7) instead of hiding behind /quota:resets only.
	var cpRow = codingPlanRow(display, entry.codingPlan);
	if (cpRow !== "") {
		rows.push(cpRow);
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

// windowStatus returns the per-window budget level, matching the footer color
// for that window's projected window-end usage. An over-budget window also names
// the moment it runs out ("over budget — exhausted at 14:32", local time): the
// projection already knows the window will not make it to its reset, and a
// percentage plus a countdown makes the user do that arithmetic. When the
// projection cannot be made (no resetsAt/periodMs, or a window already past its
// reset) the words stand alone rather than an invented time.
function windowStatus(lim) {
	var r = projectedRatio(lim);
	if (r > 1.0) {
		var runOut = runOutAtMs(lim);
		if (runOut === null) {
			return "over budget";
		}
		return "over budget — exhausted at " + format.clock(runOut);
	}
	if (r > 0.8) {
		return "close to limit";
	}
	return "plenty of room";
}

// appendUnsupportedNote adds a "quota not supported" note to the /quota output
// when the active provider resolved to the local fallback (i.e. it has no
// quota API). The local/inferred row itself was dropped from the table
// (bugs.md: redundant with the Session Usage table above), so the note just
// explains the absence of a quota window for the active provider.
function appendUnsupportedNote(out) {
	var active = (goa.config() && goa.config().activeProvider) || "";
	if (active === "") {
		return;
	}
	if (activeFetcherId() === _fallbackId) {
		out.push("");
		out.push("_Quota tracking is not supported for provider `" + active + "`._");
	}
}

// renderJSON emits the machine-readable snapshot. Each limit carries the
// projected exhaustion instant (`exhaustedAt`, epoch ms) next to its raw fields,
// so a script does not have to parse the sentence the table prints in Status.
// It is null when the window is not projected to run out (see runOutAtMs).
function renderJSON() {
	refreshAllDue(true);
	var out = { providers: {}, session: goa.sessionUsage ? goa.sessionUsage() : {} };
	for (var id in _cache) {
		var e = _cache[id];
		out.providers[id] = {
			name: (_fetchers[id] && _fetchers[id].name) || id,
			plan: e.plan || null,
			error: e.error || null,
			limits: limitsWithRunOut(e.limits || []),
			resetsCount: typeof e.resetsCount === "number" ? e.resetsCount : null,
			resets: e.details || null,
			codingPlan: e.codingPlan || null,
			fetchedAt: e._fetchedAt || 0
		};
	}
	return JSON.stringify(out, null, 2);
}

// limitsWithRunOut copies each limit and adds exhaustedAt (epoch ms, or null).
function limitsWithRunOut(limits) {
	var out = [];
	for (var i = 0; i < limits.length; i++) {
		var lim = limits[i];
		var copy = {};
		for (var k in lim) {
			copy[k] = lim[k];
		}
		copy.exhaustedAt = runOutAtMs(lim);
		out.push(copy);
	}
	return out;
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
		} else if (f.auth.type === "goa_oauth") {
			state = goaOAuthAvailable(f) ? "Goa OAuth ✓" : "login with /login:openai:oauth ∇";
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

// --- Codex rate-limit resets ------------------------------------------------

// Simplification vs Codex (plan §5.6): Codex tracks in-flight redemptions by
// u64 request ids and can address any pending one from the TUI. Goa has no
// request-id scheme — a module-scope single-flight boolean serializes resets,
// and the fetcher's retained redeem_request_id (UUID-v4) carries the actual
// idempotency guarantee. Stale-picker/request-id races therefore cannot occur.
var _resetInFlight = false;

// codexResetFetcher returns the codex fetcher module when it supports resets.
function codexResetFetcher() {
	var f = _fetchers.codex;
	if (!f || typeof f.resetCredits !== "function" || typeof f.consumeReset !== "function") {
		return null;
	}
	return f;
}

// quotaResetsCommand implements /quota:resets: force-refresh codex usage, then
// fetch the details endpoint. On a details error, degrade to count-only (the
// count arrives via the usage snapshot's resetsCount). Every configured reset
// surface gets a section: Codex rate-limit credits, then the z.ai Coding Plan
// resets. `/quota:resets:read` clears the Coding Plan unread-history marker.
function quotaResetsCommand(arg) {
	if (arg === "read") {
		return codingPlanHistoryReadCommand();
	}
	var out = [];
	var codex = codexResetsSection();
	if (codex !== "") {
		out.push(codex);
	}
	var zai = renderZaiCodingPlanSection();
	if (zai !== "") {
		if (out.length > 0) {
			out.push("");
		}
		out.push(zai);
	}
	if (out.length === 0) {
		return "No reset credits are supported for the configured providers.";
	}
	return out.join("\n");
}

// codexResetsSection is the Codex half of /quota:resets: force-refresh usage,
// then the details endpoint, degrading to the count-only note on error.
function codexResetsSection() {
	var f = codexResetFetcher();
	if (!f) {
		return "";
	}
	refreshDue("codex", true);
	var details = f.resetCredits();
	if (details && details.error) {
		return renderResetsCountOnly(details.error);
	}
	return renderResetsTable(details);
}

// codingPlanHistoryReadCommand implements `/quota:resets:read`: POST the
// read-cursor clear off the command path (provider HTTP must never block the
// input line), then report the outcome.
function codingPlanHistoryReadCommand() {
	var s = zaiResetSurface();
	if (!s || typeof s.markHistoryRead !== "function") {
		return "z.ai Coding Plan resets are not supported by this plugin build.";
	}
	if (!providerConfigured("zai")) {
		return "No z.ai provider is configured.";
	}
	goa.setTimeout(function() { codingPlanHistoryReadOnce(zaiResetSurface()); }, 0);
	return "Clearing the z.ai reset-history marker…";
}

// codingPlanHistoryReadOnce performs the read-cursor clear and reports it,
// re-fetching z.ai so the next render shows the marker gone.
function codingPlanHistoryReadOnce(s) {
	var out = s.markHistoryRead(sessionContext("zai"));
	if (out && out.error) {
		goa.output("Could not clear the z.ai reset-history marker (" + out.error + ").");
		return;
	}
	delete _cache.zai;
	refreshDue("zai", true);
	goa.ui.refreshSegment("quota");
	goa.output("✔ z.ai reset-history marker cleared.");
}

// renderResetsTable renders the reset-credit details as markdown:
// id-short | title | expiry | status rows plus the available count.
function renderResetsTable(details) {
	var d = details || {};
	var credits = Array.isArray(d.credits) ? d.credits : [];
	if (credits.length === 0) {
		return "No Codex rate-limit reset credits on record.";
	}
	var count = typeof d.availableCount === "number" ? d.availableCount : countAvailableRows(credits);
	var out = [];
	out.push("## Codex Rate-Limit Resets");
	out.push("");
	out.push(count + " available.");
	out.push("");
	out.push("| ID | Title | Expires | Status |");
	out.push("| --- | --- | --- | --- |");
	for (var i = 0; i < credits.length; i++) {
		var c = credits[i] || {};
		var status = c.status || "unknown";
		if (c.resetType && c.resetType !== "unknown") {
			status += " (" + c.resetType + ")";
		}
		out.push("| " + c.id + " | " + (c.title || "—") + " | " + expiryText(c.expiresAtMs) + " | " + status + " |");
	}
	out.push("");
	out.push("Use one with `/quota:reset[:<credit-id>]` (a unique id prefix works too).");
	return out.join("\n");
}

// renderResetsCountOnly is the details-degradation path: the count from the
// last usage fetch still tells the user what they have.
function renderResetsCountOnly(err) {
	var entry = _cache.codex;
	var n = entry && typeof entry.resetsCount === "number" ? entry.resetsCount : null;
	if (n == null) {
		return "Codex rate-limit reset details unavailable (" + err + ") and no cached count — run /quota:refresh.";
	}
	return "Codex rate-limit reset details unavailable (" + err + ").\n\n" +
		"You have **" + n + "** rate-limit reset" + (n === 1 ? "" : "s") + " available _(count only)_.";
}

// quotaResetCommand implements /quota:reset[:<credit-id>]: explain what will
// happen, confirm via goa.ui.confirm (danger-styled Yes, default Cancel —
// Codex parity), then POST off the command path on a 0-delay timer. A
// no-UI/headless confirm resolves {cancelled:true} fail-closed and aborts
// before any request is sent.
function quotaResetCommand(creditId) {
	if (_resetInFlight) {
		return "A rate-limit reset is already in progress — wait for its result.";
	}
	// Route by ACTIVE provider: the command means "reset my current provider's
	// quota", and each provider has its own credit facility (z.ai Coding Plan
	// resets vs Codex rate-limit credits). Without this an active z.ai user
	// typing /quota:reset would be offered Codex credits they do not have.
	if (activeFetcherId() === "zai") {
		return codingPlanResetCommand(creditId);
	}
	var f = codexResetFetcher();
	if (!f) {
		return "Codex rate-limit resets are not supported by this plugin build.";
	}
	refreshDue("codex", true);
	var entry = _cache.codex;
	if (entry && entry.error === "auth_required") {
		return "Codex auth required — run /login:openai:oauth first.";
	}
	var count = entry && typeof entry.resetsCount === "number" ? entry.resetsCount : null;
	var targetCredit = null;
	if (creditId) {
		// Accept a unique id PREFIX as well as a full id — the resets table
		// shows long ids, so typing them whole is friction. Resolution reads
		// the cached details only; with nothing cached the id passes through
		// verbatim and the server validates it.
		var res = resolveCreditId(creditId);
		if (res.error) {
			return res.error;
		}
		if (res.credit) {
			targetCredit = res.credit;
			creditId = targetCredit.id;
		}
	}
	var target = creditId
		? "credit `" + creditId + "`" + (targetCredit && targetCredit.title ? " (" + targetCredit.title + ")" : "")
		: "your earliest available reset credit";
	var body = "This consumes one Codex rate-limit reset credit to reset your usage limits now.\n\nTarget: " + target;
	if (count != null) {
		body += "\nRemaining after: " + Math.max(0, count - 1);
	}
	var res = confirmReset("Use a rate-limit reset?", body);
	if (!res || res.cancelled) {
		return "Cancelled — no reset consumed.";
	}
	if (res.error) {
		return "Cannot ask for confirmation (" + res.error + ") — no reset consumed.";
	}
	// Single-flight: one pending redemption at a time (see _resetInFlight
	// note above). Cleared when the flow reaches any terminal message.
	_resetInFlight = true;
	scheduleConsumeReset(creditId);
	return "Resetting your usage…";
}

// confirmReset wraps goa.ui.confirm with Codex-parity defaults: the
// destructive option is danger-styled; Cancel is the highlighted default.
function confirmReset(title, body) {
	if (typeof goa.ui.confirm !== "function") {
		return { cancelled: true, error: "no-ui" };
	}
	return goa.ui.confirm({
		title: title,
		body: body,
		options: [
			{ id: "yes", label: "Yes, use reset", style: "danger" },
			{ id: "cancel", label: "Cancel" }
		],
		defaultId: "cancel",
		allowCancel: true
	});
}

// scheduleConsumeReset runs the consume POST on a timer goroutine so the
// command path never blocks on provider HTTP (bugs.md precedent).
function scheduleConsumeReset(creditId) {
	goa.setTimeout(function() { consumeResetOnce(creditId); }, 0);
}

// consumeResetOnce performs one consume attempt and reports the outcome.
// reset/already_redeemed invalidate the cache and re-fetch so the success
// message carries the refreshed remaining count; transport errors offer an
// immediate retry that REUSES the retained redeem_request_id.
function consumeResetOnce(creditId) {
	var f = codexResetFetcher();
	if (!f) {
		_resetInFlight = false;
		return;
	}
	var out = f.consumeReset(null, creditId);
	switch (out && out.outcome) {
		case "reset":
		case "already_redeemed":
			_resetInFlight = false;
			delete _cache.codex;
			var fresh = refreshDue("codex", true);
			goa.ui.refreshSegment("quota");
			goa.output(resetSuccessMessage(fresh));
			return;
		case "no_credit":
			_resetInFlight = false;
			setCachedResetsCount(0);
			goa.ui.refreshSegment("quota");
			goa.output("No rate-limit reset credits left on your account.");
			return;
		case "nothing_to_reset":
			_resetInFlight = false;
			goa.output("Nothing to reset — your usage limits do not need a reset right now.");
			return;
		default:
			// Transport/unknown: offerResetRetry owns the flag — it stays
			// true when a retry is scheduled, false once the flow ends.
			offerResetRetry(out && out.error ? out.error : "unknown error", function() {
				scheduleConsumeReset(creditId);
			});
	}
}

// offerResetRetry asks whether to resend the SAME request after a failure;
// declining consumes nothing. The fetcher keeps the idempotency key until a
// terminal outcome either way (server dedupes double-redeems). retry is a
// zero-arg scheduler callback supplied by the caller (the surface that owns
// the call), so one retry path serves every reset facility.
function offerResetRetry(err, retry) {
	var res = confirmReset("Reset failed",
  		"The reset request failed (" + err + ").\n\nTry again with the same request?");
	if (res && !res.cancelled && !res.error) {
		goa.output("Retrying your rate-limit reset…");
		retry(); // stays in flight through the retry
		return;
	}
	_resetInFlight = false;
	goa.output("Reset not retried — no credit was consumed.");
}

// resetSuccessMessage builds the post-reset line with the refreshed remaining
// count when the re-fetch landed one.
function resetSuccessMessage(freshEntry) {
	var base = "✔ Usage limit reset.";
	if (freshEntry && typeof freshEntry.resetsCount === "number") {
		base += " You have " + freshEntry.resetsCount +
			" rate-limit reset" + (freshEntry.resetsCount === 1 ? "" : "s") + " remaining.";
	}
	return base;
}

// setCachedResetsCount pins the cached codex reset count (no_credit pins 0)
// and keeps the display line consistent for later /quota renders.
function setCachedResetsCount(n) {
	var entry = _cache.codex;
	if (!entry) {
		return;
	}
	entry.resetsCount = n;
	if (Array.isArray(entry.lines)) {
		for (var i = 0; i < entry.lines.length; i++) {
			if (entry.lines[i].label === "Rate Limit Resets") {
				entry.lines[i].value = n + " available";
				return;
			}
		}
		entry.lines.push({ label: "Rate Limit Resets", value: n + " available" });
	}
}

// --- z.ai Coding Plan reset flow (claim / use) -------------------------------

// codingPlanResetCommand implements /quota:reset for an active z.ai provider.
// arg is "claim" (ask the server whether a new grant is available), "week"
// (consume the weekly reset) or empty (consume a 5-hour reset). It reuses the
// shared confirm + single-flight machinery: a reset spends a real credit, so
// it is confirmed and serialized exactly like a Codex redemption.
function codingPlanResetCommand(arg) {
	var s = zaiResetSurface();
	if (!s || typeof s.requestOpportunity !== "function") {
		return "z.ai Coding Plan resets are not supported by this plugin build.";
	}
	if (!providerConfigured("zai")) {
		return "No z.ai provider is configured.";
	}
	var st = s.status(sessionContext("zai"));
	if (st && st.error === "auth_required") {
		return "z.ai Coding Plan credentials missing (" + (st.reason || "auth_required") +
			") — set the zcode JWT and log in with `/login zai:oauth`.";
	}
	// A throttled status is the server's own instruction: answer from the
	// persisted boundary instead of sending a call it just refused.
	if (st && st.throttled) {
		return "z.ai Coding Plan API throttled — next attempt " + cooldownText(st) + ".";
	}
	if (arg === "claim") {
		_resetInFlight = true;
		goa.setTimeout(function() { codingPlanClaimOnce(s); }, 0);
		return "Asking z.ai for a Coding Plan reset grant…";
	}
	var type = s.normalizeResetType(arg === "week" || arg === "weekly" ? "WEEK" : "FIVE_HOUR");
	if (type.error) {
		return "Unknown reset type `" + arg + "` — use `/quota:reset` (5-hour), `/quota:reset:week`, or `/quota:reset:claim`.";
	}
	var available = st && !st.error ? (st.availableFiveHour || []).length + (st.availableWeek || []).length : 0;
	if (available === 0) {
		return "No z.ai Coding Plan resets available — `/quota:reset:claim` asks the server for one.";
	}
	var body = "This consumes one z.ai Coding Plan reset to clear your " +
		(type.value === "WEEK" ? "weekly" : "5-hour") + " usage window now.\n\n" +
		"Available now: " + available + ".";
	var res = confirmReset("Use a Coding Plan reset?", body);
	if (!res || res.cancelled) {
		return "Cancelled — no reset consumed.";
	}
	if (res.error) {
		return "Cannot ask for confirmation (" + res.error + ") — no reset consumed.";
	}
	_resetInFlight = true;
	goa.setTimeout(function() { codingPlanUseOnce(s, type.value); }, 0);
	return "Resetting your z.ai usage…";
}

// codingPlanClaimOnce performs one opportunity (grant) claim and reports it.
// The surface persists the server's next_try_at on a throttle, so a denied
// claim tells the user when to come back instead of inviting another poll.
function codingPlanClaimOnce(s) {
	var out = s.requestOpportunity(sessionContext("zai"));
	_resetInFlight = false;
	if (out && out.error) {
		if (out.throttled) {
			goa.output("z.ai Coding Plan claim throttled — next attempt " + cooldownText(out) + ".");
			return;
		}
		goa.output("Could not claim a Coding Plan reset (" + out.error + ").");
		return;
	}
	delete _cache.zai;
	refreshDue("zai", true);
	goa.ui.refreshSegment("quota");
	goa.output("Coding Plan reset granted — run /quota:resets to see your available resets.");
}

// codingPlanUseOnce performs one consume call and reports the outcome. A
// success invalidates the cache and re-fetches so the message and the segment
// carry the refreshed availability; a throttle reports the boundary; a
// transport error offers a retry that REUSES the retained idempotency key
// (the server dedupes a double-consume).
function codingPlanUseOnce(s, type) {
	var out = s.useReset(sessionContext("zai"), type);
	if (out && out.used) {
		_resetInFlight = false;
		delete _cache.zai;
		refreshDue("zai", true);
		goa.ui.refreshSegment("quota");
		goa.output("✔ z.ai " + (type === "WEEK" ? "weekly" : "5-hour") + " quota reset used.");
		return;
	}
	if (out && out.throttled) {
		_resetInFlight = false;
		goa.output("z.ai Coding Plan API throttled — next attempt " + cooldownText(out) + ".");
		return;
	}
	offerResetRetry(out && out.error ? out.error : "unknown error", function() {
		goa.setTimeout(function() { codingPlanUseOnce(s, type); }, 0);
	});
}

// --- Reset-credit helpers (resolution + completion) -------------------------

// availableCredits returns the cached codex detail rows still spendable,
// soonest expiry first (missing expiry last). Cache-only by design: both
// prefix resolution and completions run on the input path and must never
// trigger provider HTTP — a stale/empty cache degrades gracefully instead.
function availableCredits() {
	var entry = _cache.codex;
	if (!entry || entry.error || !entry.details || !Array.isArray(entry.details.credits)) {
		return [];
	}
	var out = [];
	for (var i = 0; i < entry.details.credits.length; i++) {
		var c = entry.details.credits[i] || {};
		if (c.status === "available" && c.id) {
			out.push(c);
		}
	}
	out.sort(function(a, b) {
		var ea = a.expiresAtMs == null ? Infinity : a.expiresAtMs;
		var eb = b.expiresAtMs == null ? Infinity : b.expiresAtMs;
		return ea - eb;
	});
	return out;
}

// resolveCreditId maps a typed /quota:reset argument onto one cached credit:
// an exact id wins; otherwise a UNIQUE available-credit prefix match resolves.
// Returns {credit} on success, {error} with a user-facing message on
// ambiguity/no-match, or {} (passthrough) when no details are cached so an
// explicit full id still reaches the server for validation.
function resolveCreditId(partial) {
	var credits = availableCredits();
	if (credits.length === 0) {
		return {};
	}
	for (var i = 0; i < credits.length; i++) {
		if (credits[i].id === partial) {
			return { credit: credits[i] };
		}
	}
	var matches = [];
	for (var j = 0; j < credits.length; j++) {
		if (credits[j].id.indexOf(partial) === 0) {
			matches.push(credits[j]);
		}
	}
	if (matches.length === 1) {
		return { credit: matches[0] };
	}
	if (matches.length === 0) {
		return { error: "No available reset credit matches `" + partial + "` — run /quota:resets to list them." };
	}
	var lines = ["`" + partial + "` matches more than one available credit — be more specific:"];
	for (var k = 0; k < matches.length; k++) {
		lines.push("- `" + matches[k].id + "`" + (matches[k].title ? " (" + matches[k].title + ")" : ""));
	}
	return { error: lines.join("\n") };
}

// resetCreditEntries builds completion candidates from the cached available
// credits, soonest-expiry first, so the soonest credit is the default pick.
function resetCreditEntries() {
	var out = [];
	var credits = availableCredits();
	for (var i = 0; i < credits.length; i++) {
		var c = credits[i];
		var desc = c.title || "reset credit";
		var t = format.durationUntil(c.expiresAtMs);
		if (t !== "") {
			desc += " · expires in " + t;
		}
		out.push({ value: c.id, description: desc });
	}
	return out;
}

// expiryText renders an ms-epoch expiry as relative time; missing values
// degrade to an em dash.
function expiryText(ms) {
	if (!ms) {
		return "—";
	}
	var t = format.durationUntil(ms);
	return t === "" ? "—" : t;
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
	if (fetcher.auth && fetcher.auth.type === "goa_oauth") {
		return "Use /login:openai:oauth to authenticate Codex through Goa.";
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
	if (_fetchers[id].auth && _fetchers[id].auth.type === "goa_oauth") {
		return "Use Goa's logout command to clear " + (_fetchers[id].name || id) + " credentials.";
	}
	oauth.logout(id);
	delete _cache[id];
	goa.ui.refreshSegment("quota");
	return "Logged out " + (_fetchers[id].name || id) + ".";
}

// --- Rate-limit hint (plugins plan §6) --------------------------------------

// The host emits "rate_limit_exceeded" on every classified LLM stream failure
// (once per scheduled retry, once terminal with will_retry=false). When the
// failing model is Codex-ish we force-refresh the codex usage entry — its
// snapshot carries resetsCount — and, at most once per debounce window, tell
// the user how many reset credits are available.
var _lastRateLimitHintAt = 0;
var RATE_LIMIT_HINT_DEBOUNCE_MS = 10 * 60 * 1000;

// isCodexishModel reports whether a model id looks like a Codex model
// ("gpt-5-codex", "codex-mini-latest", ...). A conservative substring match
// keeps the hint scoped to the provider whose reset credits this plugin can
// actually manage.
function isCodexishModel(model) {
	return typeof model === "string" && model.toLowerCase().indexOf("codex") >= 0;
}

// handleRateLimitEvent reacts to one rate_limit_exceeded payload. The refresh
// runs on a 0-delay timer goroutine: observer callbacks execute under the VM
// lock on the host's event goroutine, and provider HTTP must never run there
// (bugs.md "Quota command unresponsive" precedent).
function handleRateLimitEvent(payload) {
	if (!isCodexishModel(payload.model)) {
		return;
	}
	goa.setTimeout(function() {
		if (!_fetchers.codex) {
			return;
		}
		refreshDue("codex", true);
		goa.ui.refreshSegment("quota");
		var entry = _cache.codex;
		var n = entry && typeof entry.resetsCount === "number" ? entry.resetsCount : null;
		var now = Date.now();
		if (n != null && n > 0 && now - _lastRateLimitHintAt >= RATE_LIMIT_HINT_DEBOUNCE_MS) {
			_lastRateLimitHintAt = now;
			goa.output("You have " + n + " rate-limit reset" + (n === 1 ? "" : "s") +
				" available — run /quota:resets for details or /quota:reset to use one.");
		}
	}, 0);
}

goa.registerObserver(function(name, payload) {
	if (name !== "rate_limit_exceeded" || !payload) {
		return;
	}
	handleRateLimitEvent(payload);
});

// --- Startup reset-credit notice ---------------------------------------------

// The reverse case of the §6 rate-limit hint: reset credits sitting UNUSED at
// session start with nothing telling the user they exist or how to spend them.
// After the priming refresh, if the codex snapshot carries a positive count,
// say so once — as chat output, not a modal: non-blocking, survives in
// scrollback. Guarded by _startupResetNoticeShown (once per session) and by
// resetsCount > 0 (only set on a successful usage fetch, never auth/error).
var _startupResetNoticeShown = false;

// _startupPrimeDone flips true as the LAST step of the load-time prime
// callback. Tests drain on this rather than on cache population: the notice
// emits after refreshAllDue fills the cache, so cache-non-empty alone would
// race the notice.
var _startupPrimeDone = false;

function maybeStartupResetNotice() {
	if (_startupResetNoticeShown) {
		return;
	}
	var entry = _cache.codex;
	if (!entry || entry.error || typeof entry.resetsCount !== "number" || entry.resetsCount <= 0) {
		return;
	}
	_startupResetNoticeShown = true;
	goa.output("ℹ You have " + entry.resetsCount + " Codex rate-limit reset" +
		(entry.resetsCount === 1 ? "" : "s") + " available" +
		" — `/quota:resets` lists them · `/quota:reset` uses one.");
}

// resetUsageNote is the bare-/quota discoverability line: the resets table row
// reads as informational only unless the output says how to act on it.
// Empty when codex has no positive cached count.
function resetUsageNote() {
	var entry = _cache.codex;
	if (!entry || entry.error || typeof entry.resetsCount !== "number" || entry.resetsCount <= 0) {
		return "";
	}
	return "_Codex rate-limit resets: `/quota:resets` lists them · `/quota:reset` uses one._";
}

// --- Registration ---------------------------------------------------------

// --- Command completions (goa.registerCompletion) ----------------------------

// QUOTA_SUBS lists the static /quota subcommands offered by the completer.
// Values carry no leading colon: the TUI engine prepends "/quota:".
var QUOTA_SUBS = [
{ value: "refresh", description: "Force-refresh all provider quotas" },
{ value: "json", description: "Machine-readable JSON output" },
{ value: "auth-status", description: "Show per-provider auth state" },
{ value: "resets", description: "List available reset credits (Codex, z.ai Coding Plan)" },
{ value: "reset", description: "Use one available reset credit" }
];

// QUOTA_RESET_ARGUMENTS are the nested `/quota:<sub>:<arg>` candidates. They
// live at their own command level, never in the bare list (which stays
// colon-free), and the z.ai reset arguments are offered only for an active
// z.ai provider: they mean nothing for a Codex account, and offering them
// everywhere would teach a command that cannot run.
var QUOTA_RESET_ARGUMENTS = {
	resets: [{ value: "read", description: "Clear the z.ai Coding Plan reset-history marker" }],
	reset: [
		{ value: "week", description: "Use one z.ai weekly Coding Plan reset" },
		{ value: "claim", description: "Ask z.ai for a new Coding Plan reset grant" }
	]
};

// completeMatches filters candidate completions by prefix ("" keeps all).
function completeMatches(items, prefix) {
	if (!prefix) {
		return items;
	}
	var out = [];
	for (var i = 0; i < items.length; i++) {
		if (items[i].value.indexOf(prefix) === 0) {
			out.push(items[i]);
		}
	}
	return out;
}

// providerEntries builds one completion per quota-capable provider id
// (local fallback excluded). purpose labels the entry for the level it
// serves: "" → refresh, "login"/"logout" → OAuth action.
function providerEntries(purpose) {
	var out = [];
	for (var id in _fetchers) {
		if (id === _fallbackId || !_fetchers[id].quotaEndpoint) {
			continue;
		}
		var label = _fetchers[id].name || id;
		var desc;
		if (purpose === "login") {
			desc = "OAuth login for " + label;
		} else if (purpose === "logout") {
			desc = "Clear " + label + " OAuth tokens";
		} else {
			desc = "Force-refresh " + label + " quota";
		}
		out.push({ value: id, description: desc });
	}
	return out;
}

// resetArgEntries builds the `/quota:reset:` candidate list: the cached Codex
// credits first (soonest expiry first), then the z.ai Coding Plan arguments
// when z.ai is the active provider.
function resetArgEntries() {
	var out = resetCreditEntries();
	if (activeFetcherId() !== "zai" || !zaiResetSurface()) {
		return out;
	}
	for (var i = 0; i < QUOTA_RESET_ARGUMENTS.reset.length; i++) {
		out.push(QUOTA_RESET_ARGUMENTS.reset[i]);
	}
	return out;
}

// quotaComplete provides /quota argument completions. prefix is everything
// after "/quota:" — "", "re", "login:o", "reset:abc" — mirroring the engine's
// nested-level convention (a parent path plus colon re-queries its children).
function quotaComplete(prefix) {
	var p = String(prefix == null ? "" : prefix);
	var idx = p.indexOf(":");
	if (idx >= 0) {
		var sub = p.slice(0, idx);
		var rest = p.slice(idx + 1);
		if (sub === "login" || sub === "logout") {
			return completeMatches(providerEntries(sub), rest);
		}
		if (sub === "reset") {
			// /quota:reset:<partial> → available credit ids, soonest expiry
			// first, so the default pick is the credit about to expire; with
			// z.ai active the Coding Plan arguments join the level.
			return completeMatches(resetArgEntries(), rest);
		}
		if (sub === "resets") {
			return completeMatches(QUOTA_RESET_ARGUMENTS.resets, rest);
		}
		return [];
	}
	var items = QUOTA_SUBS.concat(providerEntries(""));
	if (p !== "") {
		// Credit ids join the top level ONLY once something is typed
		// ("/quota:r"): the candidates carry full `reset:<id>` values, so the
		// prefix filter naturally scopes them to the r-family — bare "/quota:"
		// keeps its short static list. Soonest-expiry first.
		var credits = resetCreditEntries();
		for (var i = 0; i < credits.length; i++) {
			items.push({ value: "reset:" + credits[i].value, description: credits[i].description });
		}
	}
	return completeMatches(items, p);
}

if (typeof goa.registerCompletion === "function") {
	goa.registerCompletion("quota", quotaComplete);
}

goa.registerCommand({
	name: "quota",
	shortHelp: "Show provider usage/quota breakdown",
	longHelp: "Usage: /quota[:sub]\n\n" +
		"  /quota                 Full session + provider quota breakdown\n" +
		"  /quota:refresh         Force-refresh all provider quotas\n" +
		"  /quota:json            Machine-readable JSON output\n" +
		"  /quota:auth-status     Show per-provider auth state\n" +
		"  /quota:resets          List available reset credits (Codex, z.ai Coding Plan)\n" +
		"  /quota:resets:read     Clear the z.ai Coding Plan reset-history marker\n" +
		"  /quota:reset[:<id>]    Consume one Codex rate-limit reset credit (unique id prefix ok)\n" +
		"  /quota:reset           Use a z.ai 5-hour Coding Plan reset (active provider z.ai)\n" +
		"  /quota:reset:week      Use a z.ai weekly Coding Plan reset\n" +
		"  /quota:reset:claim     Ask z.ai for a new Coding Plan reset grant\n" +
		"  /quota:login:<id>      OAuth login (plugin-owned providers only)\n" +
		"  /quota:logout:<id>     Clear plugin-owned OAuth tokens\n" +
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
// unresponsive"). Once the prime lands, surface the one-time startup notice
// when reset credits are available, then flag completion for the tests'
// deterministic drain.
goa.setTimeout(function() {
	refreshAllDue(false);
	maybeStartupResetNotice();
	_startupPrimeDone = true;
	goa.ui.refreshSegment("quota");
}, 0);