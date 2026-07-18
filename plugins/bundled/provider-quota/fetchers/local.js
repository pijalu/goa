// fetchers/local.js — local/inferred quota for providers with no quota API.
//
// Reads Goa's accumulated session token counters via ctx.session (populated
// from goa.sessionUsage) and reports them as an unbounded "used" figure.
// Registered as the fallback so every session shows something.

function fetch(ctx) {
	var s = ctx.session || {};
	var input = num(s.input);
	var output = num(s.output);
	return {
		plan: null,
		limits: [{
			label: "Session tokens",
			used: input + output,
			limit: null, // unlimited — show accumulated only
			resetsAt: null
		}],
		local: true
	};
}

function num(v) { var n = Number(v); return isNaN(n) ? 0 : n; }

module.exports = {
	name: "Local",
	auth: { type: "none" },
	refreshInterval: 0, // 0 = every turn (no API cost)
	quotaEndpoint: false,
	fetch: fetch
};
