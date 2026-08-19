// fetchers/codex.js — Codex subscription quota via Goa-managed OAuth.
//
// Authentication is intentionally delegated to Goa. This fetcher never reads
// Codex files, keychains, or plugin OAuth storage.

var hq = require("../lib/http-quota.js");

var USAGE_URL = "https://chatgpt.com/backend-api/wham/usage";
var SESSION_MS = 5 * 60 * 60 * 1000;
var WEEKLY_MS = 7 * 24 * 60 * 60 * 1000;

function codexToken() {
	if (!goa.auth || typeof goa.auth.oauthToken !== "function") {
		return null;
	}
	var token = goa.auth.oauthToken("openai");
	return token && token.accessToken ? token : null;
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
	var headers = {
		Authorization: "Bearer " + token.accessToken,
		Accept: "application/json",
		"User-Agent": "Goa"
	};
	if (token.accountId) {
		headers["ChatGPT-Account-Id"] = token.accountId;
	}
	return hq.getJSON(USAGE_URL, headers, function(body) {
		// Merge the fresh snapshot into the retained one, then derive the
		// display lines from the merged result so preserved plan/credits still
		// render. On a transport/HTTP error getJSON returns an error object
		// instead, which never reaches this mapper.
		var merged = mergeRateLimitFields(_previous, mapUsage(body));
		_previous = merged;
		merged.lines = extraLines(merged, body);
		return merged;
	});
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

module.exports = {
	name: "Codex",
	auth: { type: "goa_oauth", provider: "openai" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch,
	// Exported for unit tests pinning the preserve-on-absent merge semantics.
	mapUsage: mapUsage,
	mergeRateLimitFields: mergeRateLimitFields
};
