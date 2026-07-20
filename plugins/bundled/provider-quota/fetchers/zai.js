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
	map: hq.windowedUsageMapper({
		session: { label: "Session (5h)", periodMs: 5 * 3600000 },
		weekly: { label: "Weekly", periodMs: 7 * 86400000 },
		web_search: { label: "Web searches", periodMs: 30 * 86400000 }
	})
};

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
