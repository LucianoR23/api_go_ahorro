// Signature of one mail for POST /inbound/email (plan 3.5). Must match
// internal/inbound/keys.go: the webhook key is HMAC-SHA256(secret,
// "inbound:webhook"), and the signature is HMAC-SHA256(webhook key,
// timestamp + "\n" + to + "\n" + from + "\n" + raw mail), hex encoded.

const enc = new TextEncoder();

async function hmac(keyBytes, data) {
  const key = await crypto.subtle.importKey("raw", keyBytes, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return new Uint8Array(await crypto.subtle.sign("HMAC", key, data));
}

function concat(a, b) {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

function hex(bytes) {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

/** Decodes INBOUND_HMAC_KEY (base64), the same secret the API has. */
export function decodeSecret(base64) {
  const bin = atob(base64.trim());
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

/**
 * @param {Uint8Array} secret raw INBOUND_HMAC_KEY bytes
 * @param {string} timestamp unix seconds
 * @param {string} to envelope recipient
 * @param {string} from envelope sender
 * @param {Uint8Array} raw the whole mail
 */
export async function sign(secret, timestamp, to, from, raw) {
  const webhookKey = await hmac(secret, enc.encode("inbound:webhook"));
  return hex(await hmac(webhookKey, concat(enc.encode(`${timestamp}\n${to}\n${from}\n`), raw)));
}
