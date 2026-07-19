// fetchers/openrouter.js — OpenRouter quota via the key-info API (API key auth).
//
// OpenRouter exposes credit balance on the API key itself.

var hq = require("../lib/http-quota.js");

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		return (ctx.config.baseUrl || "https://openrouter.ai") + "/api/v1/auth/key";
	},
	headers: hq.bearerHeaders,
	map: function(body) {
		if (!body.data) {
			return { error: "bad_response", plan: null, limits: [] };
		}
		var d = body.data;
		var limits = [];
		// limit_remaining / limit are in USD credits.
		if (d.limit !== undefined && d.limit !== null) {
			limits.push({
				label: "Credits",
				used: Math.round(hq.num(d.usage) * 100), // cents
				limit: Math.round(hq.num(d.limit) * 100), // cents
				resetsAt: null
			});
		} else if (d.limit_remaining !== undefined) {
			limits.push({
				label: "Credits remaining",
				used: 0,
				limit: 0,
				remaining: hq.num(d.limit_remaining),
				resetsAt: null
			});
		}
		return { plan: d.label || null, limits: limits, costUnit: "usd_credits" };
	}
};

function fetch(ctx) {
	return hq.runFetch(desc, ctx);
}

module.exports = {
	name: "OpenRouter",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
