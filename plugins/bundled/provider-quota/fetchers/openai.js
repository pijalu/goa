// fetchers/openai.js — OpenAI quota via the usage dashboard API (API key auth).
//
// Reports monthly token/cost consumption for the billing period.

function fetch(ctx) {
	var apiKey = ctx.config.apiKey;
	if (!apiKey) {
		return { error: "no_api_key", plan: null, limits: [] };
	}
	var base = ctx.config.baseUrl || "https://api.openai.com";
	var now = new Date();
	var start = now.getFullYear() + "-" + pad2(now.getMonth() + 1) + "-01";
	var end = now.getFullYear() + "-" + pad2(now.getMonth() + 1) + "-" + pad2(now.getDate());
	var resp = goa.http.fetch(base + "/v1/dashboard/billing/usage?start_date=" + start + "&end_date=" + end, {
		method: "GET",
		headers: { "Authorization": "Bearer " + apiKey },
		timeoutMs: 15000
	});
	if (resp.error) {
		return { error: resp.error, plan: null, limits: [] };
	}
	if (resp.status === 401 || resp.status === 403) {
		return { error: "auth_required", plan: null, limits: [] };
	}
	if (resp.status !== 200) {
		return { error: "http_" + resp.status, plan: null, limits: [] };
	}
	var body = parseJSON(resp.body);
	if (!body) {
		return { error: "bad_response", plan: null, limits: [] };
	}
	// total_usage is in cents for the billing period.
	var limits = [];
	if (body.total_usage !== undefined) {
		limits.push({
			label: "Monthly (cost)",
			used: Math.round(num(body.total_usage)), // cents
			limit: 0, // no hard cap exposed; shown as accumulated cost
			resetsAt: endOfMonth(now)
		});
	}
	return { plan: null, limits: limits, costUnit: "cents" };
}

function endOfMonth(d) {
	return new Date(d.getFullYear(), d.getMonth() + 1, 1).getTime();
}
function pad2(n) { return n < 10 ? "0" + n : String(n); }
function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "OpenAI",
	auth: { type: "api_key" },
	refreshInterval: 300000,
	quotaEndpoint: true,
	fetch: fetch
};
