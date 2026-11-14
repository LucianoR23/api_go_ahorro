// Cloudflare Email Worker for import by forwarded email (plan 3.5). One
// routing rule sends in@<INBOUND_DOMAIN> here; with subaddressing on, every
// in+<token>@<INBOUND_DOMAIN> matches it. The Worker reads nothing: it signs
// the raw mail and posts it to the API, which finds the person, reads the
// mail in memory and never stores it.
import { decodeSecret, sign } from "./sign.js";

// Same cap as the API (internal/inbound MaxMailBytes). Notifications are a
// few KB; bigger mails carry attachments the app does not read.
export const MAX_MAIL_BYTES = 768 * 1024;

export default {
  async email(message, env) {
    if (message.rawSize > MAX_MAIL_BYTES) {
      message.setReject("Message too large");
      return;
    }
    const raw = new Uint8Array(await new Response(message.raw).arrayBuffer());
    const ts = String(Math.floor(Date.now() / 1000));
    const signature = await sign(decodeSecret(env.INBOUND_HMAC_KEY), ts, message.to, message.from, raw);
    const res = await fetch(`${env.API_URL}/inbound/email`, {
      method: "POST",
      headers: {
        "Content-Type": "message/rfc822",
        "X-Inbound-To": message.to,
        "X-Inbound-From": message.from,
        "X-Inbound-Timestamp": ts,
        "X-Inbound-Signature": signature,
      },
      body: raw,
    });
    if (res.status === 413) {
      message.setReject("Message too large");
      return;
    }
    if (!res.ok) {
      // A failed delivery: the sending server retries later. Nothing about
      // the mail goes to the logs, only the status.
      throw new Error(`inbound API answered ${res.status}`);
    }
  },
};
