import assert from "node:assert/strict";
import { test } from "node:test";
import worker, { MAX_MAIL_BYTES } from "../src/index.js";
import { decodeSecret, sign } from "../src/sign.js";

const enc = new TextEncoder();

// Same vector as TestSignVector in internal/inbound: the Worker and the API
// must sign alike.
test("signs like the API", async () => {
  const secret = enc.encode("0123456789abcdef0123456789abcdef");
  const got = await sign(secret, "1763049600", "in+u_abc@in.example.test", "ana@gmail.com", enc.encode("Subject: hola\r\n\r\ncuerpo"));
  assert.equal(got, "f177b1952de961cef12086989b66fcb36b6c8c1978ef82931f042afa69590cd7");
  assert.deepEqual(decodeSecret(btoa("0123456789abcdef0123456789abcdef")), secret);
});

function fakeMessage(raw, size = raw.length) {
  const m = {
    from: "ana+caf_=in+u_abc=in.example.test@gmail.com",
    to: "in+u_abc@in.example.test",
    rawSize: size,
    raw: new Response(raw).body,
    rejected: null,
    setReject(reason) {
      m.rejected = reason;
    },
  };
  return m;
}

const env = { API_URL: "https://api.example.test", INBOUND_HMAC_KEY: btoa("0123456789abcdef0123456789abcdef") };

test("posts the raw mail signed", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, init) => {
    calls.push({ url, init });
    return new Response(null, { status: 204 });
  });
  const raw = enc.encode("Subject: hola\r\n\r\ncuerpo");
  const m = fakeMessage(raw);
  await worker.email(m, env);
  assert.equal(calls.length, 1);
  const { url, init } = calls[0];
  assert.equal(url, "https://api.example.test/inbound/email");
  assert.equal(init.headers["X-Inbound-To"], m.to);
  assert.equal(init.headers["X-Inbound-From"], m.from);
  const want = await sign(decodeSecret(env.INBOUND_HMAC_KEY), init.headers["X-Inbound-Timestamp"], m.to, m.from, raw);
  assert.equal(init.headers["X-Inbound-Signature"], want);
  assert.deepEqual(new Uint8Array(init.body), raw);
  assert.equal(m.rejected, null);
});

test("rejects big mails without posting, and fails on API errors", async (t) => {
  const fetchMock = t.mock.method(globalThis, "fetch", async () => new Response(null, { status: 500 }));
  const big = fakeMessage(new Uint8Array(1), MAX_MAIL_BYTES + 1);
  await worker.email(big, env);
  assert.equal(big.rejected, "Message too large");
  assert.equal(fetchMock.mock.callCount(), 0);

  await assert.rejects(worker.email(fakeMessage(enc.encode("x")), env), /answered 500/);
});
