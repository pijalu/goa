// fetchers/zai.js — Z.ai (Zhipu GLM) quota via the monitor API (API key auth).
//
// Reports session (5h) and weekly windows plus web-search credits.
//
// The monitor API hangs off the API HOST (api.z.ai), NOT the inference
// endpoint (api.z.ai/api/coding/paas/v4). Configs carry the inference
// endpoint in baseUrl/endpoint, so we strip any path down to the origin
// before appending the monitor route — otherwise the request 404s and the
// provider silently disappears from /quota.

var hq = require("../lib/http-quota.js");

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		return originOf(ctx.config.baseUrl || ctx.config.endpoint || "https://api.z.ai") +
			"/api/monitor/usage/quota/limit";
	},
	headers: hq.bearerHeaders,
	map: zaiMap
};

// zaiMap parses the real z.ai monitor response:
//   { "data": { "level": "pro", "limits": [
//       { "type": "TIME_LIMIT",   "percentage": 0, "nextResetTime": ms, ... },
//       { "type": "TOKENS_LIMIT", "percentage": 1, "nextResetTime": ms, "unit": u, "number": n },
//       ... ] } }
// Each limits[] entry is a quota window carrying its own percentage (0-100)
// and nextResetTime. The generic windowedUsageMapper cannot be used here: it
// expects a keyed {session, weekly} object, which this API does NOT return —
// mapping with it produced zero limits and the Z.ai row vanished from /quota
// (bugs.md: z.ai not showing quota).
//
// unit encodes the window period (3 = hours, 6 = days); number is the count,
// so periodMs = number*unit_ms. Labels are derived from the window length so
// the UI shows "Session (5h)" / "Weekly" style rows. usage is synthesized as
// percentage/100*limit with limit normalized to 100 so the bar/pct renderers
// work unchanged.
function zaiMap(body) {
	var data = (body && body.data) || body || {};
	var raw = data.limits;
	if (!raw || !raw.length) {
		return { plan: data.level || null, limits: [] };
	}
	var limits = [];
	for (var i = 0; i < raw.length; i++) {
		var w = raw[i];
		var pct = hq.num(w.percentage);
		limits.push({
			label: windowLabel(w),
			used: pct,          // already a 0-100 percentage
			limit: 100,         // normalized so used/limit = pct/100
			resetsAt: w.nextResetTime || null,
			periodMs: windowPeriodMs(w)
		});
	}
	return { plan: data.level || null, limits: limits };
}

// windowPeriodMs derives the window length in ms from the z.ai unit/number
// pair (unit 3 = hours, 6 = days; number = count of that unit).
function windowPeriodMs(w) {
	var n = hq.num(w.number) || 1;
	if (w.unit === 3) {
		return n * 3600000;
	}
	if (w.unit === 6) {
		return n * 86400000;
	}
	return 0;
}

// windowLabel names a window from its period so it renders like the other
// providers ("Session (5h)", "Weekly", or a day/hour count fallback).
function windowLabel(w) {
	var ms = windowPeriodMs(w);
	if (ms === 5*3600000) {
		return "Session (5h)";
	}
	if (ms === 7*86400000) {
		return "Weekly";
	}
	if (w.unit === 6) {
		return hq.num(w.number) + "d window";
	}
	if (w.unit === 3) {
		return hq.num(w.number) + "h window";
	}
	return w.type === "TIME_LIMIT" ? "Time window" : "Token window";
}

// originOf reduces a URL to scheme://host[:port], dropping any path. Values
// without a scheme (shouldn't happen) are returned with trailing slashes and
// any known API path trimmed.
function originOf(u) {
	u = String(u).replace(/\/+$/, "");
	var m = /^(https?:\/\/[^/]+)/.exec(u);
	if (m) {
		return m[1];
	}
	return u.replace(/\/api\/.*$/, "");
}

function fetch(ctx) {
	return hq.runFetch(desc, ctx);
}

module.exports = {
	name: "Z.ai",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
