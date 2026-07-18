// fetchers/opencode.js — OpenCode quota via OAuth + console API.
//
// OpenCode uses OAuth device-code auth for its quota console, separate from
// model access. Token lifecycle is owned by lib/oauth.js.

var oauth = require("../lib/oauth.js");

var AUTH = {
	type: "oauth",
	clientId: "goa-plugin",
	authUrl: "https://console.opencode.ai/auth/device/code",
	tokenUrl: "https://console.opencode.ai/auth/device/token",
	verificationUri: "https://console.opencode.ai/activate"
};

function fetch(ctx) {
	var token = oauth.getToken("opencode", AUTH);
	if (!token) {
		return { error: "auth_required", plan: null, limits: [] };
	}
	var resp = goa.http.fetch("https://console.opencode.ai/api/usage", {
		method: "GET",
		headers: { "Authorization": "Bearer " + token },
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
	var data = body.data || body;
	var limits = [];
	if (data.session) {
		limits.push({
			label: "Session (5h)",
			used: num(data.session.used),
			limit: num(data.session.limit),
			resetsAt: data.session.reset_at || null
		});
	}
	if (data.weekly) {
		limits.push({
			label: "Weekly",
			used: num(data.weekly.used),
			limit: num(data.weekly.limit),
			resetsAt: data.weekly.reset_at || null
		});
	}
	var plan = data.plan || (body.plan && body.plan.name) || null;
	return { plan: plan, limits: limits };
}

function parseJSON(s) { try { return JSON.parse(s); } catch (e) { return null; } }
function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "OpenCode",
	auth: AUTH,
	refreshInterval: 60000, // 1 min — OAuth polling during login
	quotaEndpoint: true,
	fetch: fetch
};
