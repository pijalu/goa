// fetchers/minimax.js — MiniMax quota via the token-plan API (API key auth).

var hq = require("../lib/http-quota.js");

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		return (ctx.config.baseUrl || "https://api.minimax.io") + "/v1/token_plan/remains";
	},
	headers: hq.bearerHeaders,
	map: function(body) {
		var data = body.data || body;
		var limits = [];
		if (data.session) {
			limits.push({
				label: "Session",
				used: hq.num(data.session.used),
				limit: hq.num(data.session.limit),
				resetsAt: data.session.reset_at || null,
				periodMs: 5 * 3600000
			});
		} else if (data.remains !== undefined) {
			limits.push({
				label: "Tokens remaining",
				used: hq.num(data.used),
				limit: hq.num(data.remains) + hq.num(data.used),
				resetsAt: data.reset_at || null
			});
		}
		return { plan: data.plan || null, limits: limits };
	}
};

function fetch(ctx) {
	return hq.runFetch(desc, ctx);
}

module.exports = {
	name: "MiniMax",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
