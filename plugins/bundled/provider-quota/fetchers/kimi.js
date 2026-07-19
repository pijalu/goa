// fetchers/kimi.js — Kimi (Moonshot) quota via the coding usages API.
//
// Verified against the live endpoint (2026-07): GET
// https://api.kimi.com/coding/v1/usages accepts the SAME API key used for
// inference (sk-kimi-...), so no separate OAuth dance is required. Response:
//
//	{
//	  "user":   { "membership": { "level": "LEVEL_ADVANCED" } },
//	  "usage":  { "limit": "100", "used": "21", "remaining": "79",
//	              "resetTime": "2026-07-24T07:41:33Z" },
//	  "limits": [ { "window": { "duration": 300, "timeUnit": "TIME_UNIT_MINUTE" },
//	                "detail": { "limit": "100", "used": "4", "remaining": "96",
//	                            "resetTime": "..." } } ]
//	}
//
// All numeric fields are STRINGS. "usage" is the long (weekly-class) window;
// "limits[]" carries the short session window (300 min = 5h) and any extra
// windows the account has. Labels are derived from the window duration so a
// plan change (5h→6h, 7d→30d) still renders correctly.

var hq = require("../lib/http-quota.js");

var DEFAULT_BASE = "https://api.kimi.com/coding/v1";

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		var base = trimSlash(ctx.config.baseUrl || ctx.config.endpoint || DEFAULT_BASE);
		// The inference endpoint is .../coding/v1; the quota API hangs off it.
		return base + (/\/usages$/.test(base) ? "" : "/usages");
	},
	headers: hq.bearerHeaders,
	map: function(body) {
		return { plan: planLabel(body), limits: extractLimits(body) };
	}
};

function fetch(ctx) {
	return hq.runFetch(desc, ctx);
}

// extractLimits maps the usages payload onto the shared {label, used, limit,
// resetsAt} shape. The session (shortest) window comes first, then the
// longer usage window; a monthly-class window is labeled when present.
function extractLimits(body) {
	var out = [];
	var candidates = [];
	var rows = body.limits || [];
	for (var i = 0; i < rows.length; i++) {
		var item = rows[i] || {};
		var q = quota(item.detail || item);
		if (!q) {
			continue;
		}
		candidates.push({ quota: q, periodMs: windowMs(item.window) });
	}
	// Shortest window first (session), then progressively longer windows.
	candidates.sort(function(a, b) { return (a.periodMs || 1e15) - (b.periodMs || 1e15); });
	for (var j = 0; j < candidates.length; j++) {
		var c = candidates[j];
		out.push({
			label: windowLabel(c.periodMs),
			used: c.quota.used,
			limit: c.quota.limit,
			resetsAt: c.quota.resetsAt,
			periodMs: c.periodMs || null
		});
	}
	// The top-level "usage" object is the long (weekly) window. Append it when
	// it is not a duplicate of a limits[] entry.
	var uq = quota(body.usage);
	if (uq && !sameQuotaList(out, uq)) {
		out.push({ label: "Weekly", used: uq.used, limit: uq.limit, resetsAt: uq.resetsAt, periodMs: 7 * 86400000 });
	}
	return out;
}

// quota normalizes {limit, used | remaining, resetTime} (all string-typed by
// the API) into {used, limit, resetsAt}, or null when unusable.
function quota(row) {
	if (!row) {
		return null;
	}
	var limit = num(row.limit);
	if (limit <= 0) {
		return null;
	}
	var used = num(row.used);
	if (row.used === undefined || row.used === null) {
		var remaining = num(row.remaining);
		used = limit - remaining;
	}
	return { used: used, limit: limit, resetsAt: row.resetTime || row.reset_at || null };
}

// windowMs converts {duration, timeUnit} to milliseconds (0 when unknown).
function windowMs(w) {
	if (!w) {
		return 0;
	}
	var d = num(w.duration);
	if (d <= 0) {
		return 0;
	}
	var unit = String(w.timeUnit || w.time_unit || "").toUpperCase();
	if (unit.indexOf("MINUTE") >= 0) {
		return d * 60000;
	}
	if (unit.indexOf("HOUR") >= 0) {
		return d * 3600000;
	}
	if (unit.indexOf("DAY") >= 0) {
		return d * 86400000;
	}
	if (unit.indexOf("SECOND") >= 0) {
		return d * 1000;
	}
	return 0;
}

// windowLabel names a window from its duration: 5h → "Session (5h)",
// 7d → "Weekly", 30d → "Monthly". Parenthesized durations feed shortTag().
function windowLabel(ms) {
	if (ms <= 0) {
		return "Session";
	}
	var min = ms / 60000;
	if (min < 24 * 60) {
		var h = Math.round(min / 60 * 10) / 10;
		return "Session (" + h + "h)";
	}
	var days = Math.round(min / (24 * 60));
	if (days >= 28) {
		return "Monthly (" + days + "d)";
	}
	if (days >= 7) {
		return "Weekly (" + days + "d)";
	}
	return "Session (" + days + "d)";
}

// planLabel maps "LEVEL_ADVANCED" → "Advanced".
function planLabel(body) {
	var level = body && body.user && body.user.membership && body.user.membership.level;
	if (!level || typeof level !== "string") {
		return null;
	}
	var cleaned = level.replace(/^LEVEL_/, "").replace(/_/g, " ").toLowerCase();
	return cleaned.replace(/\b[a-z]/g, function(c) { return c.toUpperCase(); });
}

// sameQuotaList reports whether q duplicates an entry already in out.
function sameQuotaList(out, q) {
	for (var i = 0; i < out.length; i++) {
		if (out[i].used === q.used && out[i].limit === q.limit && out[i].resetsAt === q.resetsAt) {
			return true;
		}
	}
	return false;
}

function trimSlash(s) { return String(s).replace(/\/+$/, ""); }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "Kimi",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
