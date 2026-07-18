// format.js — formatting helpers for the provider-quota plugin.
// Pure functions, no goa.* dependencies, so they are trivially testable.

// pct renders a percentage integer, clamped to [0, 100] for bars but shown
// raw for values (a provider may report >100% during overage).
function pct(used, limit) {
	if (!limit || limit <= 0) {
		return 0;
	}
	return Math.round((used / limit) * 100);
}

// bar renders a fixed-width unicode progress bar, e.g. "████████░░".
function bar(percent, width) {
	width = width || 10;
	var clamped = Math.max(0, Math.min(100, percent));
	var filled = Math.round((clamped / 100) * width);
	var out = "";
	for (var i = 0; i < width; i++) {
		out += i < filled ? "█" : "░";
	}
	return out;
}

// pad right-pads a string to width with spaces.
function pad(s, width) {
	s = String(s);
	while (s.length < width) {
		s += " ";
	}
	return s;
}

// padLeft left-pads a string to width.
function padLeft(s, width) {
	s = String(s);
	while (s.length < width) {
		s = " " + s;
	}
	return s;
}

// trunc shortens s to max chars, adding an ellipsis when cut.
function trunc(s, max) {
	s = String(s);
	if (s.length <= max) {
		return s;
	}
	return s.substring(0, Math.max(0, max - 1)) + "…";
}

// tokens formats a token count compactly: 142300 → "142.3K", 1250000 → "1.3M".
function tokens(n) {
	if (n === null || n === undefined || isNaN(n)) {
		return "0";
	}
	if (n >= 1000000) {
		return (n / 1000000).toFixed(1) + "M";
	}
	if (n >= 1000) {
		return (n / 1000).toFixed(1) + "K";
	}
	return String(Math.round(n));
}

// cost formats USD, e.g. 0.89 → "$0.89".
function cost(usd) {
	if (usd === null || usd === undefined || isNaN(usd)) {
		return "$0.00";
	}
	return "$" + Number(usd).toFixed(2);
}

// durationUntil renders a human countdown from now to a future timestamp
// (ms epoch or ISO string), e.g. "+1h 48m", "+4d 12h". Returns "" for past or
// unparseable input.
function durationUntil(timestamp) {
	var ms = toMs(timestamp);
	if (!ms) {
		return "";
	}
	var diff = ms - nowMs();
	if (diff <= 0) {
		return "soon";
	}
	return "+" + humanize(diff);
}

// toMs converts a ms-epoch number, seconds-epoch number, or ISO string to ms.
function toMs(timestamp) {
	if (timestamp === null || timestamp === undefined) {
		return 0;
	}
	if (typeof timestamp === "number") {
		// Heuristic: seconds-epoch values are ~1e9, ms-epoch ~1e12.
		return timestamp < 1e12 ? timestamp * 1000 : timestamp;
	}
	var parsed = Date.parse(timestamp);
	if (isNaN(parsed)) {
		return 0;
	}
	return parsed;
}

// humanize renders a millisecond duration as "1h 48m" / "4d 12h" / "13d".
function humanize(ms) {
	var sec = Math.floor(ms / 1000);
	var min = Math.floor(sec / 60);
	var hr = Math.floor(min / 60);
	var day = Math.floor(hr / 24);
	if (day >= 2) {
		var remH = hr % 24;
		return remH > 0 ? day + "d " + remH + "h" : day + "d";
	}
	if (hr >= 1) {
		var remM = min % 60;
		return remM > 0 ? hr + "h " + remM + "m" : hr + "h";
	}
	if (min >= 1) {
		return min + "m";
	}
	return sec + "s";
}

// nowMs is injectable for tests (modules can't see the test's Date mock
// otherwise). Defaults to Date.now.
var nowFn = function() { return Date.now(); };
function nowMs() { return nowFn(); }
function _setNow(fn) { nowFn = fn || function() { return Date.now(); }; }

exports.pct = pct;
exports.bar = bar;
exports.pad = pad;
exports.padLeft = padLeft;
exports.trunc = trunc;
exports.tokens = tokens;
exports.cost = cost;
exports.durationUntil = durationUntil;
exports.humanize = humanize;
exports.toMs = toMs;
exports._setNow = _setNow;
