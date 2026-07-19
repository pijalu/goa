// fetchers/zai.js — Z.ai (Zhipu GLM) quota via the monitor API (API key auth).
//
// Reports session (5h) and weekly windows plus web-search credits.

var hq = require("../lib/http-quota.js");

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		return (ctx.config.baseUrl || "https://api.z.ai") + "/api/monitor/usage/quota/limit";
	},
	headers: hq.bearerHeaders,
	map: hq.windowedUsageMapper({
		session: { label: "Session (5h)", periodMs: 5 * 3600000 },
		weekly: { label: "Weekly", periodMs: 7 * 86400000 },
		web_search: { label: "Web searches", periodMs: 30 * 86400000 }
	})
};

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
