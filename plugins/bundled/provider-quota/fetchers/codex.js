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
	return hq.getJSON(USAGE_URL, headers, mapUsage);
}

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
	return { plan: root.plan_type || null, limits: limits, lines: extraLines(root) };
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

function extraLines(body) {
	var lines = [];
	var credits = body.credits || {};
	if (credits.balance !== undefined && credits.balance !== null) {
		lines.push({ label: "Credits", value: String(credits.balance) });
	}
	var resets = body.rate_limit_reset_credits || {};
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
	fetch: fetch
};
