// fetchers/openai.js — OpenAI quota via the usage dashboard API (API key auth).
//
// Reports monthly token/cost consumption for the billing period.

var hq = require("../lib/http-quota.js");

var desc = {
	auth: hq.apiKeyAuth().auth,
	authError: "no_api_key",
	url: function(ctx) {
		var base = ctx.config.baseUrl || "https://api.openai.com";
		var now = new Date();
		var start = now.getFullYear() + "-" + pad2(now.getMonth() + 1) + "-01";
		var end = now.getFullYear() + "-" + pad2(now.getMonth() + 1) + "-" + pad2(now.getDate());
		return base + "/v1/dashboard/billing/usage?start_date=" + start + "&end_date=" + end;
	},
	headers: hq.bearerHeaders,
	map: function(body, ctx) {
		var limits = [];
		if (body.total_usage !== undefined) {
			limits.push({
				label: "Monthly (cost)",
				used: Math.round(hq.num(body.total_usage)), // cents
				limit: 0, // no hard cap exposed; shown as accumulated cost
				resetsAt: endOfMonth(new Date())
			});
		}
		return { plan: null, limits: limits, costUnit: "cents" };
	}
};

function endOfMonth(d) {
	return new Date(d.getFullYear(), d.getMonth() + 1, 1).getTime();
}
function pad2(n) { return n < 10 ? "0" + n : String(n); }

function fetch(ctx) {
	return hq.runFetch(desc, ctx);
}

module.exports = {
	name: "OpenAI",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
