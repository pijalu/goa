// lib/ids.js — client-generated request identifiers.
//
// Idempotency keys must be stable across retries of the SAME logical request
// (the server dedupes on them) and unique across distinct requests. Both
// provider reset surfaces (Codex redeem_request_id, z.ai coding-plan
// idempotency_key) mint theirs here so the format lives in exactly one place.
//
// uuidV4 uses Math.random bytes: these ids are dedupe tokens for a provider
// request, not security tokens, so a CSPRNG would be overkill — and the plugin
// runtime exposes no crypto binding.

function uuidV4() {
	var bytes = [];
	for (var i = 0; i < 16; i++) {
		bytes.push(Math.floor(Math.random() * 256));
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
	bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10xx
	var hex = [];
	for (var j = 0; j < 16; j++) {
		hex.push((bytes[j] + 0x100).toString(16).slice(1));
	}
	return hex.slice(0, 4).join("") + "-" +
		hex.slice(4, 6).join("") + "-" +
		hex.slice(6, 8).join("") + "-" +
		hex.slice(8, 10).join("") + "-" +
		hex.slice(10, 16).join("");
}

exports.uuidV4 = uuidV4;
