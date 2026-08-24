// fetchers/codex.js — Codex subscription quota via Goa-managed OAuth.
//
// Authentication is intentionally delegated to Goa. This fetcher never reads
// Codex files, keychains, or plugin OAuth storage.

var hq = require("../lib/http-quota.js");
var format = require("../lib/format.js");

// WHAM_BASE is the ChatGPT backend-api root; every Codex quota URL derives
// from it (usage + the two rate-limit-reset-credits endpoints).
var WHAM_BASE = "https://chatgpt.com/backend-api";
var USAGE_URL = WHAM_BASE + "/wham/usage";
var RESET_DETAILS_URL = WHAM_BASE + "/wham/rate-limit-reset-credits";
var RESET_CONSUME_URL = WHAM_BASE + "/wham/rate-limit-reset-credits/consume";
var SESSION_MS = 5 * 60 * 60 * 1000;
var WEEKLY_MS = 7 * 24 * 60 * 60 * 1000;

function codexToken() {
	if (!goa.auth || typeof goa.auth.oauthToken !== "function") {
		return null;
	}
	var token = goa.auth.oauthToken("openai");
	if (!token || !token.accessToken || token.error) {
		return null;
	}
	return token;
}

// codexHeaders builds the auth/accept headers shared by the usage GET and
// both reset endpoints (previously inlined in fetch()).
function codexHeaders(token) {
	var headers = {
		Authorization: "Bearer " + token.accessToken,
		Accept: "application/json",
		"User-Agent": "Goa"
	};
	if (token.accountId) {
		headers["ChatGPT-Account-Id"] = token.accountId;
	}
	return headers;
}

// _previous retains the last merged quota snapshot at module scope, mirroring
// how Codex keeps rate-limit state in its session and merges each new snapshot
// into it (state/session.rs merge_rate_limit_fields). The require cache keeps
// this alive across fetch() calls within a session, so a sparse /wham/usage
// response inherits the fields it omits from the prior snapshot.
var _previous = null;

function fetch() {
	var token = codexToken();
	if (!token) {
		return { error: "auth_required", plan: null, limits: [] };
	}
	var out = hq.getJSON(USAGE_URL, codexHeaders(token), function(body) {
		// Merge the fresh snapshot into the retained one, then derive the
		// display lines from the merged result so preserved plan/credits still
		// render. On a transport/HTTP error getJSON returns an error object
		// instead, which never reaches this mapper.
		var merged = mergeRateLimitFields(_previous, mapUsage(body));
		_previous = merged;
		merged.lines = extraLines(merged, body);
		// resetsCount is display metadata (NOT part of the merge state): the
		// rate-limit-reset credit count rides along so /quota:resets can
		// degrade to count-only when the details endpoint fails.
		var resets = body && body.rate_limit_reset_credits;
		if (resets && typeof resets.available_count === "number") {
			merged.resetsCount = resets.available_count;
		}
		return merged;
	});
	if (out && !out.error) {
		// Ride the reset-credit DETAILS along with every successful usage
		// refresh so the global /quota breakdown can render them inline
		// without a second blocking fetch on the command path. Errors degrade
		// silently here: /quota falls back to the count-only note and
		// /quota:resets re-fetches explicitly with its own error surface.
		var details = resetCredits();
		if (details && !details.error) {
			out.details = details;
		}
	}
	return out;
}

// mapUsage maps a raw /wham/usage body into a structured quota snapshot:
// { limit_id, plan, credits, limits }. Fields the body omits stay absent
// (undefined) here — distinguishing absent from explicit zero is what lets the
// merge preserve-on-absent without masking an authoritative exhausted state.
function mapUsage(body) {
	var limits = [];
	var root = body || {};
	var rate = root.rate_limit || {};
	addWindow(limits, "Session", rate.primary_window, SESSION_MS);
	addWindow(limits, "Weekly", rate.secondary_window, WEEKLY_MS);
	var extras = Array.isArray(root.additional_rate_limits) ? root.additional_rate_limits : [];
	for (var i = 0; i < extras.length; i++) {
		var item = extras[i] || {};
		var name = shortName(item.limit_name || item.metered_feature || "Model");
		var rl = item.rate_limit || {};
		addWindow(limits, name, rl.primary_window, SESSION_MS);
		addWindow(limits, name + " Weekly", rl.secondary_window, WEEKLY_MS);
	}
	var review = root.code_review_rate_limit && root.code_review_rate_limit.primary_window;
	addWindow(limits, "Reviews", review, WEEKLY_MS);
	return {
		limit_id: root.limit_id, // absent unless the backend names a bucket
		plan: root.plan_type != null ? root.plan_type : undefined,
		credits: root.credits != null ? root.credits : undefined,
		limits: limits
	};
}

// mergeRateLimitFields mirrors Codex's merge_rate_limit_fields: preserve prior
// plan/credits ONLY when the new snapshot omits them (absent — undefined or
// null), and treat an explicit authoritative value — including zero/exhausted
// — as a real state change that REPLACES the old value. A missing limit_id
// falls into the default "codex" bucket. Never preserve-on-zero: a backend
// that reports balance 0 means the credits are genuinely exhausted.
function mergeRateLimitFields(previous, snapshot) {
	var prior = previous || {};
	var merged = {
		limit_id: snapshot.limit_id != null ? snapshot.limit_id : "codex",
		plan: snapshot.plan != null ? snapshot.plan : prior.plan,
		credits: snapshot.credits != null ? snapshot.credits : prior.credits,
		limits: snapshot.limits || []
	};
	return merged;
}

function addWindow(out, label, window, fallbackMs) {
	if (!window || typeof window.used_percent !== "number") return;
	var seconds = Number(window.limit_window_seconds);
	var periodMs = seconds > 0 ? seconds * 1000 : fallbackMs;
	out.push({
		label: label,
		used: window.used_percent,
		limit: 100,
		resetsAt: resetAt(window),
		periodMs: periodMs
	});
}

// extraLines derives the non-window display lines. The Credits line reads the
// MERGED snapshot so a balance preserved from a prior sparse fetch still
// renders (and an explicit authoritative 0 renders as 0); the rate-limit-reset
// count is not part of the merge state, so it reads the raw body directly.
function extraLines(snapshot, body) {
	var lines = [];
	var credits = (snapshot && snapshot.credits) || {};
	if (credits.balance !== undefined && credits.balance !== null) {
		lines.push({ label: "Credits", value: String(credits.balance) });
	}
	var resets = (body && body.rate_limit_reset_credits) || {};
	if (resets.available_count !== undefined && resets.available_count !== null) {
		lines.push({ label: "Rate Limit Resets", value: String(resets.available_count) + " available" });
	}
	return lines;
}

function resetAt(window) {
	if (window.reset_at === undefined || window.reset_at === null) return null;
	return Number(window.reset_at) * 1000;
}

function shortName(name) {
	return String(name).replace(/^GPT-[\d.]+-Codex-/, "") || "Model";
}

// --- rate-limit reset credits (Codex parity) --------------------------------
//
// Contract mirrors Codex's rate-limit-reset-credits state machine: a consume
// request carries a client-generated `redeem_request_id`; the server dedupes
// double-redeems by that key. Outcomes: reset | nothing_to_reset |
// no_credit | already_redeemed.

// OUTCOME_ALIASES maps backend result codes onto the canonical outcome
// vocabulary. Unknown/absent codes surface as an error so the caller can
// offer "Try again" with the SAME idempotency key (the server dedupes).
var OUTCOME_ALIASES = {
	reset: "reset",
	ok: "reset",
	success: "reset",
	redeemed: "reset",
	already_redeemed: "already_redeemed",
	no_credit: "no_credit",
	no_credits: "no_credit",
	nothing_to_reset: "nothing_to_reset",
	nothing_to_do: "nothing_to_reset"
};

// _pendingResetKey is the module-scope redeem_request_id (UUID-v4 from
// Math.random bytes — fine for redeem ids). Retained from the first attempt
// until a TERMINAL outcome (all four mapped outcomes); "try again" reuses it
// so the server dedupes double-redeems. Deliberately NOT cleared when the
// user cancels a retry — only a terminal outcome clears it.
var _pendingResetKey = null;

// pendingResetKey returns the retained redeem id, minting one on first use.
function pendingResetKey() {
	if (!_pendingResetKey) {
		_pendingResetKey = uuidV4();
	}
	return _pendingResetKey;
}

// clearPendingResetKey drops the retained id after a terminal outcome.
function clearPendingResetKey() {
	_pendingResetKey = null;
}

// uuidV4 renders a random v4 UUID string without crypto dependencies.
function uuidV4() {
	var bytes = [];
	for (var i = 0; i < 16; i++) {
		bytes.push(Math.floor(Math.random() * 256));
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
	bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10xx
	var hex = [];
	for (var j = 0; j < 16; j++) {
		hex.push((bytes[j] + 0x100).toString(16).slice(1));
	}
	return hex.slice(0, 4).join("") + "-" +
		hex.slice(4, 6).join("") + "-" +
		hex.slice(6, 8).join("") + "-" +
		hex.slice(8, 10).join("") + "-" +
		hex.slice(10, 16).join("");
}

// resetCredits GETs the details endpoint and returns the tolerant detail map
// {availableCount, credits:[{id,title,resetType,status,expiresAtMs}]}. Any
// error returns {error} and the caller degrades to count-only (the count
// already arrives via the usage snapshot's resetsCount).
function resetCredits() {
	var token = codexToken();
	if (!token) {
		return { error: "auth_required" };
	}
	return hq.getJSON(RESET_DETAILS_URL, codexHeaders(token), mapResetDetails);
}

// mapResetDetails tolerantly maps the details body: unknown reset_type/status
// strings degrade to "unknown", RFC3339/epoch expiry values become ms epoch,
// available credits sort first ordered by earliest expiry.
function mapResetDetails(body) {
	var root = body || {};
	var raw = root.credits;
	if (!Array.isArray(raw)) {
		raw = root.reset_credits;
	}
	if (!Array.isArray(raw)) {
		raw = [];
	}
	var credits = [];
	for (var i = 0; i < raw.length; i++) {
		credits.push(mapResetCredit(raw[i]));
	}
	credits.sort(availableFirstByExpiry);
	var count = typeof root.available_count === "number" ? root.available_count : countAvailable(credits);
	return { availableCount: count, credits: credits };
}

// mapResetCredit normalizes one credit row; absent ids keep the row renderable.
function mapResetCredit(c) {
	c = c || {};
	return {
		id: c.id != null ? String(c.id) : (c.credit_id != null ? String(c.credit_id) : ""),
		title: c.title || c.description || "",
		resetType: enumOrUnknown(c.reset_type != null ? c.reset_type : c.type),
		status: enumOrUnknown(c.status),
		expiresAtMs: format.toMs(c.expires_at) || null
	};
}

// enumOrUnknown lowercases a backend enum string; anything absent or blank
// becomes "unknown" instead of crashing the renderer.
function enumOrUnknown(v) {
	if (typeof v !== "string") {
		return "unknown";
	}
	var s = v.trim().toLowerCase();
	return s === "" ? "unknown" : s;
}

// countAvailable counts credits still in the available state.
function countAvailable(credits) {
	var n = 0;
	for (var i = 0; i < credits.length; i++) {
		if (credits[i].status === "available") {
			n++;
		}
	}
	return n;
}

// availableFirstByExpiry sorts available credits first, earliest expiry first;
// everything else follows in the same expiry order (missing expiry last).
function availableFirstByExpiry(a, b) {
	var av = (a.status === "available" ? 0 : 1) - (b.status === "available" ? 0 : 1);
	if (av !== 0) {
		return av;
	}
	var ea = a.expiresAtMs == null ? Infinity : a.expiresAtMs;
	var eb = b.expiresAtMs == null ? Infinity : b.expiresAtMs;
	return ea - eb;
}

// consumeReset POSTs {redeem_request_id, credit_id} to the consume endpoint
// and maps the backend code onto the canonical outcome. An explicit
// redeemRequestId pins the idempotency key (tests); otherwise the
// module-scope pending key is used AND RETAINED across transport errors so a
// retry re-sends the identical request (server dedupes double-redeems).
function consumeReset(redeemRequestId, creditId) {
	var token = codexToken();
	if (!token) {
		return { error: "auth_required" };
	}
	var key = redeemRequestId || pendingResetKey();
	var payload = { redeem_request_id: key };
	if (creditId) {
		payload.credit_id = String(creditId);
	}
	var headers = codexHeaders(token);
	headers["Content-Type"] = "application/json";
	return hq.postJSON(RESET_CONSUME_URL, headers, payload, function(body) {
		var out = mapOutcome(body);
		// Terminal outcome (any of the four): the pending key has served its
		// purpose — clear it so the NEXT reset mints a fresh id. Errors keep
		// it for the retry path.
		if (!out.error) {
			clearPendingResetKey();
		}
		return out;
	});
}

// mapOutcome maps a consume response body onto {outcome} using the alias
// table; unrecognized codes are errors (bad_response), never guesses.
function mapOutcome(body) {
	var root = body || {};
	var code = root.code;
	if (code == null) {
		code = root.outcome != null ? root.outcome : root.result;
	}
	var outcome = OUTCOME_ALIASES[String(code == null ? "" : code).toLowerCase()];
	if (!outcome) {
		return { error: "bad_response" };
	}
	return { outcome: outcome };
}

module.exports = {
	name: "Codex",
	auth: { type: "goa_oauth", provider: "openai" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch,
	// Exported for unit tests pinning the preserve-on-absent merge semantics.
	mapUsage: mapUsage,
	mergeRateLimitFields: mergeRateLimitFields,
	// Rate-limit-reset surface (plan §5): commands + harness tests.
	resetCredits: resetCredits,
	consumeReset: consumeReset,
	mapOutcome: mapOutcome,
	mapResetDetails: mapResetDetails,
	pendingResetKey: pendingResetKey,
	clearPendingResetKey: clearPendingResetKey,
	urls: {
		usage: USAGE_URL,
		details: RESET_DETAILS_URL,
		consume: RESET_CONSUME_URL
	}
};
