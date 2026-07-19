// fetchers/anthropic.js — Anthropic quota via the usage API (API key auth).
//
// Anthropic exposes account usage through a session/weekly windowed endpoint.
// Auth uses the same x-api-key as model access (no separate credential).

var hq = require("../lib/http-quota.js");

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		return (ctx.config.baseUrl || "https://api.anthropic.com") + "/v1/usage";
	},
	headers: function(ctx, token) {
		return { "x-api-key": token, "anthropic-version": "2023-06-01" };
	},
	map: hq.windowedUsageMapper({
		session: { label: "Session (5h)", periodMs: 5 * 3600000 },
		weekly: { label: "Weekly", periodMs: 7 * 86400000 }
	})
};

function fetch(ctx) {
	return hq.runFetch(desc, ctx);
}

module.exports = {
	name: "Anthropic",
	auth: { type: "api_key" },
	refreshInterval: 300000, // 5 min — usage changes slowly
	quotaEndpoint: true,
	fetch: fetch
};
