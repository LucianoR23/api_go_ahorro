# inbound-email (Cloudflare Email Worker)

Import by forwarded email (plan 3.5). Every person gets an address
`in+u_<token>@<INBOUND_DOMAIN>`. Cloudflare has no catch-all on a subdomain, so one routing
rule for `in@<INBOUND_DOMAIN>` sends everything to this Worker; with subaddressing on,
`in+anything@` matches that rule and keeps the `+anything` in the recipient.

The Worker does not read the mail: it rejects mails over 768 KB, signs the raw mail and
posts it to `POST /inbound/email` on the API (see `src/sign.js` and
`internal/inbound/keys.go`). If the API fails, the Worker throws and the sending server
retries later. Invocation logs are off because they would keep senders and recipients.

No dependencies. Tests use Node's own runner:

```bash
npm test        # Node 26, from this folder
```

`test/sign.test.js` and `TestSignVector` in `internal/inbound` check the same signature, so
the Worker and the API cannot drift apart.

## Setting up staging (once)

Values: domain `in-next.valuo.ar`, API `https://api-next.valuo.ar`.

1. **Keys.** Generate two values and keep them in your password manager:
   ```bash
   openssl rand -base64 32   # INBOUND_HMAC_KEY
   openssl rand -base64 32   # INBOUND_ENC_KEY
   ```
2. **API (Coolify, staging API app, Environment Variables).** Add `INBOUND_DOMAIN=in-next.valuo.ar`,
   `INBOUND_HMAC_KEY`, `INBOUND_ENC_KEY` (`INBOUND_LOCAL_PREFIX` defaults to `in+`) and redeploy.
   Migration 000048 runs on start.
3. **Cloudflare, zone `valuo.ar`, Compute > Email Service > Email Routing > Settings.**
   - Under **Subdomains**, add `in-next.valuo.ar`. Cloudflare adds its MX and TXT records on
     that subdomain; `hola@valuo.ar` and the apex records are not touched.
   - Turn on **Subaddressing**.
4. **Deploy the Worker** (from this folder; the login opens the browser and is yours to do):
   ```bash
   npx --yes wrangler@4.158.0 login
   npx --yes wrangler@4.158.0 secret put INBOUND_HMAC_KEY --env staging   # paste the same value as the API
   npm run deploy:staging
   ```
5. **Routing rule.** Email Routing > Routing rules > Create address: custom address
   `in@in-next.valuo.ar`, action **Send to a Worker**, destination `inbound-email-staging`.
6. **Check.** With a session token from staging:
   ```bash
   curl -H "Authorization: Bearer $TOKEN" https://api-next.valuo.ar/inbound/address
   ```
   Send a mail from the account's Gmail to that address, then
   `GET /inbound/messages` should list it (a Gmail forwarding confirmation shows its code).
   If the mail bounces with "Address does not exist", subaddressing does not work on the
   subdomain: stop and decide another address format (the API takes the bare token too,
   so only `INBOUND_LOCAL_PREFIX` and the routing change).

Live debugging, nothing stored: `npx --yes wrangler@4.158.0 tail --env staging`.

## Rotating `INBOUND_HMAC_KEY`

Avoid it: addresses are found by an HMAC of this key, so a new key loses every address
(see `DEPLOY.md`). If it leaked, follow the rotation row in `DEPLOY.md` and set the new value
with `secret put` here too.
