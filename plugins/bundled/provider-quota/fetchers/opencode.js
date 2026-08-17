// fetchers/opencode.js — OpenCode quota via the Zen usage API (API key auth).
//
// OpenCode Zen (opencode) and OpenCode Go (opencode-go) are curated-model
// gateways sharing the OPENCODE_API_KEY, passed as a Bearer token:
//
//	GET https://opencode.ai/zen/go/v1/usage
//
// The endpoint hangs off the same base URL as inference, so a config that
// overrides baseUrl/endpoint (e.g. the zen/v1 variant) resolves its usage
// route under that origin — matching how the Kimi fetcher hangs /usages off
// the coding/v1 inference base.
//
// Real response shape (captured live 2026-08-17):
//
//	{"usage":{"rolling":{"status":"ok","percent":12,"resetsAt":"…"},
//	          "weekly": {"status":"ok","percent":34,"resetsAt":"…"},
//	          "monthly":{"status":"ok","percent":85,"resetsAt":"…"}}}
//
// Three percent-based rate-limit windows: rolling is the 5h session window
// (an unused account's resetsAt lands exactly +5h out), weekly resets Monday
// 00:00 UTC, monthly on the billing day. Percentages normalize to used/100
// (same idiom as the z.ai monitor fetcher) so the shared bar/pct renderers
// work unchanged. The mapping MUST be driven by this real shape: the first
// version of this fetcher invented a credits payload the API does not return
// and the provider vanished from /quota and the status bar (zero limits →
// empty segment). A credit-balance variant ({data:{balance,used,limit}}) is
// still tolerated as a fallback for gateway deployments that report credits.

var hq = require("../lib/http-quota.js");

var DEFAULT_BASE = "https://opencode.ai/zen/go/v1";

// Window periods: rolling = 5h (verified: an unused account's resetsAt is
// exactly +5h), weekly = 7d, monthly ≈ 30d (only feeds the at-reset pace
// projection, which degrades to the raw percent when timing is unknown).
var WINDOWS = [
	{ key: "rolling", label: "Session (5h)", periodMs: 5 * 3600000 },
	{ key: "weekly", label: "Weekly", periodMs: 7 * 86400000 },
	{ key: "monthly", label: "Monthly", periodMs: 30 * 86400000 }
];

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		var base = trimSlash(ctx.config.baseUrl || ctx.config.endpoint || DEFAULT_BASE);
		// The usage API lives ONLY under the go base: /zen/v1/usage serves an
		// HTML 404 page (verified live 2026-08-17), while both variants share
		// the same account and key — so route the usage call through /go/.
		base = base.replace(/\/zen\/v1$/, "/zen/go/v1");
		return base + (/\/usage$/.test(base) ? "" : "/usage");
	},
	headers: hq.bearerHeaders,
	map: opencodeMap
};

// opencodeMap shapes the usage payload onto the shared {plan, limits} result.
// Priority order: (1) the real usage.{rolling,weekly,monthly} percent windows,
// (2) a windowed limits[] array, (3) the credit-balance fallback. Returning
// zero limits is the silent-failure mode this mapper must avoid — a provider
// with no rows vanishes from /quota AND hides the status segment.
function opencodeMap(body) {
	var data = (body && (body.data || body.usage)) || body || {};
	var windows = usageWindows(body);
	if (windows.length > 0) {
		return { plan: planLabel(body, data), limits: windows };
	}
	var limits = extractWindowedLimits(body);
	if (limits.length > 0) {
		return { plan: planLabel(body, data), limits: limits };
	}
	var credits = creditLimit(data);
	return {
		plan: planLabel(body, data),
		limits: credits ? [credits] : [],
		costUnit: "usd_credits"
	};
}

// usageWindows maps the real {"usage":{"rolling":…,"weekly":…,"monthly":…}}
// payload: each window reports percent (0-100) and resetsAt; used/limit are
// normalized to percent/100. Windows are emitted shortest-period first.
function usageWindows(body) {
	var usage = (body && body.usage) || {};
	var out = [];
	for (var i = 0; i < WINDOWS.length; i++) {
		var w = usage[WINDOWS[i].key];
		if (!w || w.percent === undefined || w.percent === null) {
			continue;
		}
		out.push({
			label: WINDOWS[i].label,
			used: hq.num(w.percent),
			limit: 100,
			resetsAt: w.resetsAt || w.reset_at || null,
			periodMs: WINDOWS[i].periodMs
		});
	}
	return out;
}

// extractWindowedLimits maps a limits[] array (when the gateway returns
// rate-limit windows alongside the balance) onto the shared shape, shortest
// window first.
function extractWindowedLimits(body) {
	var rows = (body && body.limits) || [];
	var out = [];
	for (var i = 0; i < rows.length; i++) {
		var item = rows[i] || {};
		var q = windowQuota(item.detail || item);
		if (!q) {
			continue;
		}
		out.push({
			label: item.label || item.window || "Window",
			used: q.used,
			limit: q.limit,
			resetsAt: q.resetsAt,
			periodMs: q.periodMs
		});
	}
	out.sort(function(a, b) { return (a.periodMs || 1e15) - (b.periodMs || 1e15); });
	return out;
}

// windowQuota normalizes a windowed {limit, used|remaining, reset} row, or
// null when it carries no usable bound.
function windowQuota(row) {
	var limit = hq.num(row.limit);
	if (limit <= 0) {
		return null;
	}
	var used = hq.num(row.used);
	if (row.used === undefined || row.used === null) {
		used = limit - hq.num(row.remaining);
	}
	return {
		used: used,
		limit: limit,
		resetsAt: row.resetTime || row.reset_at || row.resetsAt || row.nextResetTime || null,
		periodMs: hq.num(row.periodMs) || null
	};
}

// creditLimit synthesizes the prepaid credit balance as a used/limit pair (in
// cents, so bar/pct renderers stay integer-friendly). used comes from lifetime
// usage when reported, else from balance deducted against a known limit; when
// only a bare balance is present the row reports the remaining balance as a
// 0-used credit pool.
function creditLimit(data) {
	var balance = firstPresent(data.balance, data.credits, data.credit_balance, data.remaining);
	var used = firstPresent(data.used, data.usage, data.total_used, data.spent);
	var limit = firstPresent(data.limit, data.total, data.credit_limit, data.granted);
	if (limit > 0) {
		if (used === null && balance !== null && balance <= limit) {
			used = limit - balance; // derive usage from the remaining balance
		}
		return {
			label: "Credits",
			used: Math.round((used || 0) * 100),
			limit: Math.round(limit * 100),
			resetsAt: null,
			periodMs: null
		};
	}
	if (balance !== null && balance > 0) {
		// Only a remaining balance is known: present it as the full pool so
		// the row shows the balance without inventing usage.
		return {
			label: "Credits remaining",
			used: 0,
			limit: Math.round(balance * 100),
			resetsAt: null,
			periodMs: null
		};
	}
	return null;
}

// planLabel surfaces the account tier when the payload carries one.
function planLabel(body, data) {
	var p = (body && body.plan) || data.plan || data.level || data.tier || null;
	if (p && typeof p === "object") {
		p = p.name || p.level || null;
	}
	return p;
}

// firstPresent returns the first argument that is a present, finite number
// (including 0), else null. Distinguishing 0 from absent lets a zero balance
// or zero usage derive correctly instead of being skipped as "missing".
function firstPresent() {
	for (var i = 0; i < arguments.length; i++) {
		var v = arguments[i];
		if (v === undefined || v === null) {
			continue;
		}
		var n = Number(v);
		if (!isNaN(n)) {
			return n;
		}
	}
	return null;
}

function trimSlash(s) { return String(s).replace(/\/+$/, ""); }

function fetch(ctx) {
	return hq.runFetch(desc, ctx);
}

module.exports = {
	name: "OpenCode",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
