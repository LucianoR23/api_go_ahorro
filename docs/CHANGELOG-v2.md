# Changelog — v2 (`next` branch)

Entries reference item IDs from `AHORRA_V2_PLAN.md` (workspace repo).

## Unreleased

### 1.11 (API part): superadmin panel
- New `internal/admin` package, superadmin only (401 signed out, 403 for anyone else). Following plan §8b it returns counts and account metadata only: no amounts, no descriptions, nothing from inside a space.
- `GET /admin/overview`: accounts (total, new in the last 7 days, new this month, verified, active in the last 30 days by session use), active and deleted hogares, confirmed waitlist entries, and expenses and incomes created per month for the last 6 months (by creation date, app time zone).
- `GET /admin/users?q&offset`: accounts, newest first, 50 per page (`nextOffset`). `q` searches email and name, ignoring case; `%` and `_` are literal. Each row: email, name, sign-up, verified, onboarded, deleted, last seen, superadmin, number of hogares.
- `GET /admin/users/{id}`: the same plus pending email, password or Google sign-in, active sessions and the account's spaces (name, kind, role, members).
- `GET /admin/audit?userId&group&cursor`: the audit log of every account, newest first, 50 per page with a keyset cursor. Each event carries the email of the account it happened to and of whoever did it. `userId` matches both; `group` is `sessions`, `failed`, `account`, `household` or `admin`.
- Reads only, no migration. Access to a person's financial data for support was left for later, tied to the support tickets.

### 10.1 (API part): notification preferences and quiet hours
- `GET /me/notification-preferences` and `PUT` (signed in, per person, every space): which kinds of notice reach the person on which channel, plus quiet hours. Kinds: shared expenses and settlements (app, push), card closing and due dates (app, push), installments due (app, push), goals, recurring price rises and the weekly summary (app), the monthly summary (email). Only the channels the API really uses are offered; no new emails. Invites and security emails can't be muted.
- In the app, a muted kind leaves Notificaciones and the unread count (also the notices already there); the notice is still created, so turning it on again brings it back.
- Quiet hours (on by default, 23:00 to 08:00, app timezone): a push that falls inside waits for the end of the window as a scheduled River job (`push_delivery`) and is checked again before sending, in case the kind was muted meanwhile. This also moves the card reminder pushes that the 01:00 insights job sent at night to 08:00. The test push is never held.
- Migration 000044 adds `notification_preferences` (one row per person; no row means the defaults) and the `insight_kind(type, metadata)` SQL function that maps a notice to its kind. Each push now carries its kind; the monthly email skips whoever turned it off.

### 9.1 (API part): move expenses and incomes between Mi plata and a hogar
- `POST /expenses/{id}/move {householdId}`: the author takes an expense to another space they belong to; the id stays the same. Into a hogar it splits by the hogar's rules and the other members get the usual "new shared expense" notice; back to Mi plata it stops splitting (its shares go, so it leaves "Cuentas entre ustedes"; recorded payments are not touched) and the members of the old split get a push.
- Installments move with the expense and keep their dates and paid state. The category becomes the one with the same name in the target space ("Súper" matches "super"; a subcategory matches "Parent > Child", then the parent alone, else none). A draft must be confirmed first (422). The link to a recurring series is dropped, since the series belongs to the old space.
- `POST /incomes/{id}/move {householdId}`: the member who received an income takes it to another space of theirs. Incomes don't split, so only the space changes; the recurring link is dropped.
- Only the author (or receiver) moves, not even the hogar owner (403); a target space the user isn't in is a 404; the same space or no `householdId` is a 422.
- Plan 9.1 rescoped with the user: no `visibility` column, since private things already live in Mi plata, a space nobody else sees.
- Migration 000043 makes the installments' `(expense_id, household_id)` foreign key `ON UPDATE CASCADE`, so they follow the expense. The name comparison moved to `internal/textnorm`, shared with the subscription suggestions.

### 11.1 (API part): Excel export
- `GET /exports/expenses.xlsx?from&to` (space member): the space's confirmed expenses in the period as an Excel file, oldest first. Takes the same filters as `GET /expenses` (`categoryId` with its subcategories, `paymentMethodId`, `kind=fixed|variable`, `q`). Columns: date, description, category ("Servicios > Luz"), payment method, amount, currency, rate, amount in pesos, installments, fixed or variable, shared, who loaded it.
- `GET /exports/incomes.xlsx?from&to`: the space's incomes in the period (date, description, type, payment method, amount, currency, rate, amount in pesos, who received it).
- `from` and `to` are required and cover at most 366 days; a file holds at most 20.000 rows (a 422 asks for a shorter period). The file is named after the period: `gastos-2026-11.xlsx`, `gastos-2026.xlsx`, or both dates. `Cache-Control: no-store`.
- A payment method's name is only filled in for its owner, as in the app; other members' rows leave it blank.
- Dates and amounts are real Excel dates and numbers in Excel's built-in formats, so Excel shows them in the reader's language (03/11/2026, 19.450,50 in Spanish). Header row frozen with filters.
- Written with the standard library (`internal/xlsx`, a zip of XML parts): no new dependency. Read-only, no migration. CSV was left out: Excel is what people open.

### 7.3 (API part): subscription suggestions
- `GET /recurring-expenses/suggestions` (space member): expenses loaded by hand that look recurring. A suggestion is a description (case, accents and punctuation ignored) in one currency that shows up once a month in at least 3 of the last 4 months (this one included), with the biggest amount at most 25% over the smallest. Only confirmed one-payment expenses outside a series count; groceries bought several times a month do not.
- Each suggestion carries a `key`, the description as written last, currency, last amount, smallest and biggest, months seen, day of the month and payment method of the last one, so the app can prefill a recurring expense. Descriptions already used by a recurring expense of the space (active or paused) are left out.
- `POST /recurring-expenses/suggestions/dismiss {key}`: the suggestion stops showing for every member of the space. Twice is fine; an empty key is a 422.
- Migration 000042 adds `recurring_suggestion_dismissals` (space, key, who, when).

### 7.2 (API part): due-date calendar
- `GET /calendar?month=YYYY-MM` (signed in, space member; this month by default): what is due or comes in, day by day. Events are the user's card closing and due dates (as in Tarjetas, in every space) and the space's recurring expenses and incomes on each day they fall in the month (paused series and days outside start/end are left out).
- A card's due date is listed only when its statement has something to pay, with its totals per currency, status and installment count; its closing always. A recurring expense or income carries its amount (the estimate when variable), currency, payment method and series id.
- Card installments are not listed one by one: they are already inside each statement's total.
- Same day order: income, card due, recurring expense, card closing, then by name. Read-only, no migration.

### 5.6 (API part): reports in other currencies and crypto
- `GET /reports/monthly` and `GET /reports/trends` take `currency` in USD, EUR, BRL, CLP, UYU, USDT, BTC or ETH (view only: movements still take ARS, USD or EUR). The dollar keeps both modes; every other currency converts at today's rate. A movement loaded in the currency shown keeps its amount.
- A fiat currency goes through the household's dollar, like a movement would (the official real moved by MEP / official). A crypto has one market price: USDT is DolarAPI's crypto dollar, BTC and ETH come from CoinGecko.
- Responses add `rate` (today's pesos per unit) and `scale` (decimals amounts keep: 8 for BTC and ETH, 2 otherwise). `usdRate` stays, only in dollars. No quote yet for the currency asked for: 409. `adjust=cpi` is only in pesos.
- `fx_refresh` stores the official real, Chilean and Uruguayan peso from DolarAPI and USDT at the crypto dollar. With `COINGECKO_API_KEY` (a free demo key) it also stores bitcoin and ether in pesos, asking CoinGecko at most once every 55 minutes; without it they are skipped. A CoinGecko failure never drops the dollars.
- Migration 000041 widens `exchange_rates.currency` to those currencies, grows the rate columns to `NUMERIC(18,4)` (a bitcoin costs more than 99.999.999 pesos), and replaces `report_amount` with a version that takes the currency shown and today's rate. No movement table changes.
- `GET /exchange-rates/current` now also lists the new currencies. `GET /exchange-rates/history` still takes only USD and EUR.

### 5.5 (API part): the base currency is always pesos
- A space always sums in pesos. Expenses and incomes still take ARS, USD or EUR and keep their own amount; only the space's base is locked. Changing it used to leave old amounts in the old currency.
- `POST /households` and `PATCH /households/{id}`: `baseCurrency` is optional and, if sent, must be `ARS`; anything else is a 422 on `baseCurrency`.
- Migration 000040 adds `CHECK (base_currency = 'ARS')` on `households` (`NOT VALID`, so an old test space in another currency does not break the deploy). The column stays: opening another country means dropping the check.
- The reports' "space kept in another currency" branches stay, unreachable for new spaces.

### 5.4 (API part): spending in today's pesos
- New table `cpi_index` (migration 000039): INDEC's national CPI, nivel general, base December 2016, one row per month. Global, not per household.
- Daily job `cpi_refresh` (05:00 and on start) reads INDEC's own CSV (`serie_ipc_divisiones.csv`, Codigo 0, Region Nacional), which has a month the day INDEC publishes it. The datos.gob.ar Series API (`148.3_INIVELNAL_DICI_M_26`) is the fallback: same series, republished weeks later. A file with fewer than 12 months or a changed layout is rejected and the fallback is used; when both fail, what is stored stays. Revised months are updated.
- `GET /reports/trends?adjust=cpi`: every point (spent, due, income, net) times the last published index over the month's. Months after the last published one count as that one (no inflation is assumed). The response adds `adjust: "cpi"` and `cpiThrough` (YYYY-MM). Only in pesos: with `currency=USD` or in a space kept in another currency it is a 422; without any index stored, a 409.
- `GET /reports/monthly` adds `spentRealThisMonth` and `cpiThrough` for a month older than the last published index (in pesos only).

### 5.3 (API part): reports in dollars
- `GET /reports/monthly` and `GET /reports/trends` take `currency=USD` (default pesos) and `rate=today` (default `day`). In dollars, a movement loaded in dollars keeps its amount; any other is its pesos at the household's dollar (`fx_source`) of the movement's day (an installment, its purchase day), or of today with `rate=today`. A day before the first stored quote uses the oldest one.
- Responses add `currency`, and in dollars `rateSource`, `rateMode` and `usdRate` (today's price of that dollar). A household whose dollar has no quotes at all gets 409.
- The monthly report adds `incomeThisMonth`, so Inicio needs one request less.
- Migration 000038 adds two SQL functions and touches no table: `fx_usd_rate(source, day, tz)` and `report_amount(...)`, which every report sum goes through.
- Statements and debts between members stay in pesos.

### 5.2 (API part): rate history and stale rates
- `GET /exchange-rates/history?currency=USD&source=bolsa&days=30` (signed in; USD and MEP by default, 1 to 365 days): one quote per day, the day's last in the app's timezone, oldest first.
- `exchange_rates` already keeps every new quote, so it is the history table: storing unchanged fetches again would only duplicate rows. A quote returned again now updates `fetched_at`, which means "last confirmed by an upstream".
- `GET /exchange-rates/current` adds `stale`: no upstream confirmed the rate in 24 hours (fetcher down, or DolarAPI down for a source only it has). Based on the confirmation, not on the quote's date, so the official dollar is not stale on weekends.

### 5.1 (API part): rates from DolarAPI, a source per household and per movement
- Rates come from DolarAPI (`/v1/dolares`: oficial, blue, bolsa = MEP, contadoconliqui, tarjeta, cripto, mayorista; `/v1/cotizaciones`: the official euro). Bluelytics stays as the fallback and is the only source of the blue euro. A refresh fails only when both upstreams fail; when one does, it is logged and the other's rates are kept.
- Migration 000037: every DolarAPI source in `exchange_rates.source`; `households.fx_source` (default `bolsa`, the MEP dollar, decided with the user); `expenses.rate_source` and `incomes.rate_source` (the source of `rate_used`, null without conversion and for older rows, which used blue).
- `PATCH /households/{id}` takes an optional `fxSource`; households return `fxSource`. `POST /expenses` and `POST /incomes` take an optional `rateSource` (empty: the household's) and return it, e.g. the card dollar for a purchase abroad or CCL for dollars sold. Recurring items, draft confirmations, goals and the purchase simulator use the household's source (a confirmed draft keeps its own).
- Conversion uses the source's selling price (venta), the one DolarAPI publishes, instead of the buy/sell average; the average only when there is no selling price. A euro without that source goes through the dollar: euro oficial x dollar at the source / dollar oficial.
- `fxrates.Convert` takes the source explicitly.

### 4.8 (API part): "¿Me lo puedo comprar?"
- `POST /simulations/purchase` (household member) simulates a purchase without saving it: `paymentMethodId`, `amount`, `currency`, `installments`, optional `categoryId`, `spentAt` (default today) and `months` (1 to 12, default 6). The installments come from the same checks and card-period math as creating an expense (`expenses.Service.Plan`).
- Per month, keyed like "Comprometido" by statement period: what the user's cards already have (`cardsBefore`), the purchase's installment, `cardsAfter`, recurring expenses (`fixed`; one on a credit card counts only after today, since the past ones are already in a statement), recurring incomes (`income`), `left` and `pctOfIncome` (cards as a share of income, null without income). Plus the chosen card's statements before and after, and the monthly limits the purchase counts against (total, or its category or the category's parent): this period's spending with and without it, and how much of the limit each installment takes.
- Everything in the household's base currency; other currencies use today's rate (`todayRate`). Cards are the user's own; recurring items and goals are the current space's. Open to every plan for now: the Plus/Pro Hogar gate arrives with pricing.
- The recurrence rule shared by recurring expenses and incomes moved to `domain.RecurrenceMatches` (it was copied in both packages).

### 1.10c: amounts in API texts read es-AR
- New `money.Format`: `$ 19.450`, `US$ 500,50`, `€ 1.200`, `-$ 20`. Thousands with ".", cents with ",", and ",00" dropped when the amount is whole. Same output as the app's `formatAmount`.
- Used by every text the API writes for people: the shared expense and settlement notifications, the recurring spike alert, the weekly summary, goal alerts, card and installment reminders, the monthly summary email and the "excede la deuda actual" validation error. Before they read `19450.00 ARS` or `$19450.00`.
- The weekly summary and goal alerts use the household's base currency instead of always writing "$".
- A limit alert names its category and period instead of the raw goal type: "Llevás $ 950 gastados de $ 500 en Súper este mes." (before "... (category_limit)."). A total limit leaves the category out; a weekly goal says "esta semana".
- The AI export prompt keeps plain numbers (`19450.00 ARS`): a model reads `1.234` as one point two, and the prompt is not shown as a notification.
- Notifications already stored keep their old text. The app's discreet mode masks both formats.

### 1.10d: month-end card purchases no longer skip a statement
- A credit card purchase on the 29th to 31st went to the wrong statement: moving its date by whole months overflowed (Oct 31 + 1 month = Dec 1), so a purchase after the closing landed two statements later, and installments could skip a month (Jan 30 with a closing on the 31st gave January, March and March). Installment months are now counted from the first day of the month.
- Existing installments keep their dates (no recompute): production starts empty in v2 and staging holds test data. Clearing an installment's manual billing date already uses the fixed calculation.

### 1.10b (API part): the month's total in Movimientos
- `GET /expenses` gains `totalAmountBase`: what the whole filtered list adds up to in the base currency (not just the page), next to `totalCount`, for "Gastaste $ 486.320 · 38 gastos".

### 1.10b (API part): what each member paid
- `GET /reports/monthly` gains `byPayer`: what each member paid in the month (`userId`, `total` in the base currency), largest first, for "Pusiste vos / Puso Ana" in Inicio · Hogar. Whoever loads an expense is the one who paid it. Same rules as `spentThisMonth`: confirmed expenses by purchase date.

### 4.3 (API part): card reminders and the open period
- Two fixed reminders per card, as a notification and a push, to the card owner in Mi plata only (before, a card owner with a hogar got the old reminder once per space): "Mañana cierra tu Visa" the day before the closing, and "En 3 días vence tu Visa" three days before the due date when the statement has something to pay and is not paid. The push never carries the amount (plan 1.9); the in-app text and the opt-in push body do ("El resumen de tu Visa vence el sábado 7/3: 300.00 ARS."). Dates follow the loaded period or the card's default days.
- They replace the old "Cargá el próximo período" reminder on the closing day (no longer generated) and the per-installment "Cuota próxima a vencer" alert for credit cards (the statement reminder covers them).
- Migration 000036: `card_closing_soon` and `card_due_soon` join the once-per-day unique index of insights. No table changes.
- `GET /payment-methods/{id}/credit-card/periods/status` gains `openPeriod`: the period open today with its closing and due dates and `loaded` (false when the dates come from the card's default days), for the app's "Cargá el cierre de este mes" nudge.

### 4.2 (API part): Comprometido
- `GET /credit-cards/committed?months=6` (1 to 12): for every active card of the user, the statement totals from the card's open period on, per currency, plus the closed statement still to pay (unpaid and not empty, as `toPay`). The months start at the earliest open period among the cards; a card that already closed this month counts from next month. `months` adds every card per month and currency.
- `GET /payment-methods/{id}/credit-card/installment-plans`, owner only ("Cuotas en curso"): the card's expenses in two or more installments with an installment from the open period on, with the next installment's number and amount and the closing month of the last one.
- Fix (4.1): drafts (variable recurring expenses waiting for their amount) no longer count in statements, and paying a statement leaves their installments unpaid. They count once confirmed.

### 4.1 (API part): credit card statements
- Migration 000035: `credit_card_statements` (minimum payment in pesos, payment: date, amount and currency, all or none) and `credit_card_statement_adjustments` (interest, tax, fee, credit, other). Both cascade with the card.
- A statement is the card's installments billed in its closing month, from every space (Mi plata and Hogar: a card has one owner and one bill), plus adjustments; a credit subtracts. Totals are computed on every read, one per currency, so editing an expense never leaves a stale total. Status comes from the dates: open until the closing day, closed, overdue after the due date, paid.
- Endpoints under `/payment-methods/{id}/credit-card/statements`, owner only: list (`?from=&to=` up to 36 months; by default 6 back and 11 ahead of the open period, always including the open period and the one to pay, plus `openPeriodYm`), detail with installments and adjustments, `PATCH` minimum payment, `POST pay` (total by default, or a partial amount; marks every installment of the statement paid; an open statement can't be paid), `POST unpay`, adjustments create, update and delete.
- The period date rule (loaded period or the card's default days, with the month-end clamp) now lives in `domain.CardPeriodDates`, shared by expenses and statements.

### 1.9 (API part): push without amounts
- A push can show on a locked screen, so its `body` no longer carries an amount. The three pushes that did (shared expense, installment due in 3 days, payment registered) now say "Ana cargó "Súper". Tocá para ver tu parte.", "En 3 días vence la cuota 2/6 de "Heladera".", "Ana registró un pago.".
- The payload gains `bodyWithAmounts` with the full text; the app's service worker shows it only on devices where the user opted in (app part). In-app notifications keep the amount.
- The shared expense text drops its dash: "Ana cargó "Súper". Te toca 1234.50 ARS."

### 1.4 (API part): Argentine categories
- Migration 000034: `categories.parent_id` for subcategories, one level deep. The parent must belong to the same household (composite foreign key), a subcategory cannot have subcategories and a category with subcategories cannot become one (trigger). Deleting a parent keeps its subcategories as top-level categories, so no expense loses its category.
- Migration 000034: icons are Lucide keys (`shopping-cart`) instead of emojis. Stored emojis are translated with the map the app used until now; anything else becomes `tag`, the new default. The API accepts lowercase words joined by hyphens, up to 32 characters, and answers 422 on `icon` otherwise.
- `POST /categories` takes `parentId`; `PATCH /categories/{id}` takes it too (`null` makes it top-level, leaving it out keeps it). Every category response carries `parentId`. A parent that is missing, from another household or itself a subcategory answers 422 on `parentId`.
- New households and personal spaces start with 19 categories: Súper, Almacén, Delivery, Restaurantes, Transporte (SUBE, Taxi y apps), Combustible, Expensas, Alquiler, Servicios (Luz, Gas, Agua, Internet, Celular), Salud (Prepaga, Farmacia), Educación, Suscripciones, Mascotas, Ropa, Hogar, Regalos, Viajes, Impuestos, Otros. Existing households keep theirs: the database is emptied at launch.
- A subcategory adds up in its parent in the monthly report by category and in category limits: Servicios counts Luz, Gas, etc. A limit on Luz counts only Luz.
- `GET /expenses?categoryId=` on a parent also lists the expenses of its subcategories.

### 1.3 (API part): onboarding support
- Migration 000033: `credit_cards.network`, nullable, one of `visa`, `mastercard`, `amex`, `cabal`, `naranja`, `maestro`, `other`. Cards take it on create (`creditCard.network`) and on `PATCH /payment-methods/{id}/credit-card` (`null` clears it; leaving it out keeps it). Any other value answers 422 on `network`.
- Migration 000033: `users.onboarded_at`. Every user response carries `onboardedAt` once set; the app sends users without it to `/onboarding`. No backfill: every existing account is a test account and will see onboarding once.
- `POST /me/onboarding/complete` records that onboarding was finished or skipped. Calling it again keeps the first timestamp.

### 1.2 (API part 2): "Mi plata" for every user
- Registering (email or Google) creates the user's "Mi plata": a personal household named "Mi plata", in ARS, with the default categories. With email it happens inside the registration transaction; with Google a failure is logged and the next step below fixes it.
- `GET /households` creates "Mi plata" when it is missing (accounts from before 1.2) and lists it first. Two concurrent first calls still end with one.
- Account deletion no longer counts "Mi plata" as a household to hand over; it is soft-deleted with the account, its data kept.

### 1.2 (API part 1): space rules ("Mi plata" + one Hogar)
- Migration 000032: `households.kind` (`personal` = "Mi plata", `shared` = "Hogar"; existing rows are `shared`), one personal space per user, and two triggers. A personal space only admits its owner; a user belongs to at most one active shared space (soft-deleted ones don't count; the user row is locked so two simultaneous joins can't both win); restoring a hogar fails if a member joined another one meanwhile; `kind` never changes.
- `POST /households` creates a hogar and answers 409 "Ya sos parte del hogar X. Para crear otro, primero salí de X." when the user already has one. Accepting an invite while in another hogar answers 409 "Ya sos parte del hogar X. Para unirte a Y, primero salí de X." and leaves the invite pending.
- "Mi plata" cannot be deleted or invited to (422). Households carry `kind` in every response, admin listings included.
- Not here: creating "Mi plata" for each user (part 2). The plan's "pick your main hogar" flow is skipped on purpose: every current account is a test account; one with several hogares keeps them.

### 1.1 (API part): brand switch to Valuo
- `BRAND_NAME` defaults to `Valuo` (was `Ahorro`): emails, push titles, from-names and the startup banner say Valuo unless the variable is set. On staging, delete `BRAND_NAME` or set it to `Valuo`.
- Every other trace of the old brand is gone (the user confirmed every current account is a test account, so nothing is kept for compatibility):
  - Repo renamed to `valuo_api`; Go module path `github.com/LucianoR23/valuo_api`.
  - JWT issuer `valuo-api`. Access tokens issued before the deploy are rejected; the app renews them with the refresh cookie.
  - Deleted accounts get `deleted+<id>@deleted.invalid` (reserved TLD). Migration 000031 updates the existing rows (data only; the down migration restores them).
  - `VAPID_SUBJECT` lost its `mailto:admin@ahorra.app` default (not our domain). With `VAPID_PUBLIC_KEY` set, the API refuses to start unless the subject is a `mailto:` or `https://` URL.
  - Request logger name `api`; local dev Postgres: container `valuo-postgres-dev`, volume `valuo_pgdata18`, role and DB `valuo` (a fresh volume: recreate local data).
  - README, DEPLOY and code comments. Kept: applied migrations, this changelog's history and the real name of today's production DB (`ahorra`).

### 0.14: performance (migration 000030)
- Indexes on the 18 foreign keys that had none: the 10 in the audit (§11) and 8 more the catalog check found. New test `TestForeignKeysAreIndexed` reads the catalog and fails when any foreign key lacks an index, so later migrations keep it.
- `expense_installments.household_id`, copied from the expense and backfilled. A composite foreign key `(expense_id, household_id) → expenses(id, household_id)` keeps it equal to the expense's household. Reports, goals, balances and the due-soon insight now filter installments by household and date with household-first indexes (`(household_id, billing_date)`, `(household_id, COALESCE(due_date, billing_date))`, unpaid `(household_id, due_date)`) instead of reading every household's installments in the date range.
- The due-soon insight reads one household's installments per run, not every household's once per household.
- The card-closing reminder reads a household's active cards with their latest period in one query (`LATERAL`), instead of one query per member plus one per card.

### 0.12: error monitoring (GlitchTip)
- New dependency `github.com/getsentry/sentry-go` v0.50.0 (approved by the user). With `GLITCHTIP_DSN` set, every ERROR log line becomes a GlitchTip event: unhandled 500s, failed jobs, startup failures. Events carry the message, the error chain, a stack trace and a few allowlisted tags (`method`, `path`, `kind`, `job`, `queue`). Request, user, breadcrumbs and any other log attribute are dropped, and every string is scrubbed of e-mails, IDs, tokens, quoted values and numbers (`internal/monitoring`).
- The issue title is the log message (e.g. `migrations failed`), not the Go error type (`*fmt.wrapError`).
- Handler panics are logged through slog (new `httpx.Recoverer`, replacing chi's, which printed to stderr), so they are reported too.
- New variables `GLITCHTIP_DSN` and `GLITCHTIP_ENVIRONMENT`. DEPLOY.md section 8 sets up GlitchTip on Coolify (all in one, own Postgres 18, no Valkey).

### 0.8 (part 2): transactions where they were missing
- Account deletion (`DELETE /me`) is one transaction: memberships, push subscriptions, sessions and the user's soft delete commit together or not at all. It locks the user's memberships, so an ownership transfer to them waits. Soft-deleted households no longer count as owned: an owner who deleted their only household can now delete the account (the 0.8 known bug is fixed).
- Settlements: the debt check and the insert run under a per-household lock (`SELECT ... FOR NO KEY UPDATE` on the household), so two payments sent at once can no longer both pass the check and overpay.
- Coverage floor raised to 80.0 (total 81%).
- Registration (`POST /auth/register`) is one transaction: the user, their "Efectivo" payment method and, with an invite, the membership, split rule and verified email. A failure midway now returns 500 and leaves nothing behind (before, the account stayed created with a 201). An invite that cannot be accepted (expired, used, for another email) still does not block the registration. The Google sign-up path is unchanged.

### 0.8 (part 1): background jobs on River
- New dependency `github.com/riverqueue/river` v0.51.0 with its pgx v5 driver (approved by the user). Migration 000029 adds River's tables (`river_job`, `river_queue`, `river_leader`, `river_notification`, `river_migration`): River's own migrations 001 to 008 copied as one golang-migrate file. River's migrator does not run at startup.
- The seven in-process loops (FX refresh, recurring incomes, recurring expenses, insights, monthly email, audit retention, waitlist cleanup) are now River jobs in `internal/jobs`. Same times as before, in the app timezone. With several replicas only the leader enqueues periodic jobs and each job runs once; a failed job retries with backoff.
- Daily jobs and the monthly email are unique per day (or month). On start the API enqueues the days missed while it was down, from the last completed job: up to 31 days for recurring generation, 7 for insights, only the latest month for the email. Completed jobs are kept 62 days. Generation jobs run one at a time, so backfilled days apply in order.
- `reports.Worker` is now `reports.Mailer`; `App.Jobs().MonthlyReports` returns an error when a run could not start.

### 0.7 (part 3): balances, settlements, goals, insights and reports in decimal
- Balances, settlements, goal progress, insight texts and monthly reports (totals, trends, AI export, email) add and subtract money as exact decimals. Percentages are computed from the exact values and stay floats.
- Settlements: paying more than the current debt is rejected exactly. The old 1-cent tolerance (there for float noise) is gone, so 300.01 against a 300.00 debt is now a 422 instead of leaving a 1-cent debt the other way.
- Goals: an out-of-range target is a 422.
- Still floats on purpose: exchange rates and split weights (4-decimal rates converted exactly when used), percentages and alert thresholds.

### 0.7 (part 2): expenses, incomes, recurring expenses and FX in decimal
- Expenses, installments, shares, incomes, recurring incomes and recurring expenses keep money as `decimal.Decimal` end to end (database, arithmetic, JSON). Percentages stay floats.
- `fxrates.Convert` works in decimal; callers round the converted amount to cents when storing it. 0.10 USD at 1200 is exactly 120 ARS.
- Request bodies still decode amounts as JSON numbers (a string still gets a 422 naming the field) and convert them exactly. An amount beyond what the column holds (over 9,999,999,999.99) is now a 422 `monto fuera de rango` instead of a 500.
- Spike insights and shared-expense notifications print amounts with two decimals from the exact value.

### 0.7 (part 1): money package, exact splits
- New dependency `github.com/shopspring/decimal` v1.6.0 (approved by the user; it was already an indirect v1.2.0). `internal/money`: NUMERIC conversions, JSON as a number (the contract with the app does not change), and `Allocate`/`Even`.
- Splitting rule for installments and shares (`buildShares`, `buildInstallments`, draft confirmation): every part but the last is rounded down to cents and the last takes the rest. Sums are exact and no part is negative (before, rounding up could leave the last share or installment below zero, failing the `CHECK >= 0`). Some splits move a cent: 200.00 in 3 installments is now 66.66 / 66.66 / 66.68 (was 66.67 / 66.67 / 66.66).
- Confirming a draft keeps each installment's share proportions; a draft whose shares were all zero now splits evenly instead of giving everything to the last member.

### 0.16 (API part): security headers and CSRF
- Every response carries HSTS (2 years, preload), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, COOP, CORP and a CSP that allows nothing (`default-src 'none'`). The waitlist unsubscribe pages set their own CSP (inline styles, form posts to itself).
- `/auth/refresh` and `/auth/logout`, the endpoints that use the refresh cookie, reject a browser request from an origin outside `ALLOWED_ORIGINS` (or a cross-site one with no `Origin`) with 403 `forbidden`. Requests without browser headers still pass.

### B.5 (part 2): waitlist operations
- `GET /admin/waitlist.csv` (superadmin): confirmed entries as CSV, with spreadsheet formulas neutralized. `DELETE /admin/waitlist?email=` (superadmin): deletes an address on request (Ley 25.326, within 5 business days); 204 even when it was not on the list. Both are recorded in the audit log, without the deleted address.

### B.5 (part 1): landing waitlist
- Migration `000028`: `waitlist_entries` (email, monotributo flag, form, first-touch source/medium/campaign, referrer path, notice version, timestamps, hashed confirmation token). No name, IP or user agent at rest.
- `POST /waitlist` per the contract (`valuo_web/docs/waitlist-contract.md`): 202 `{"status":"pending_confirmation"}` for new and known addresses alike; the confirmation mail goes in the background (similar latency), at most every 10 minutes, never to a confirmed address. 422 with the field for invalid data, attribution sanitized and never rejected, 4 KB body cap. Turnstile is verified with Cloudflare (single use, the landing's hostname); with `ENV=prod` and no `TURNSTILE_SECRET_KEY` every sign-up gets 403.
- `POST /waitlist/confirm`: 200 (also when already confirmed) or 410.
- Every mail has a one-click unsubscribe: a link to a page with a button (GET never deletes, mail scanners prefetch) and RFC 8058 `List-Unsubscribe` headers. Unsubscribing deletes the entry.
- Own CORS on `/waitlist` (landing origins, POST, no credentials); 5 requests per 10 minutes per IP with a JSON 429 and `Retry-After`. A daily job deletes unconfirmed sign-ups older than 30 days.
- New variables in `DEPLOY.md`: `WAITLIST_SITE_URL`, `WAITLIST_ALLOWED_ORIGINS`, `WAITLIST_BRAND_NAME`, `WAITLIST_FROM_EMAIL`, `WAITLIST_CONTACT_EMAIL`, `TURNSTILE_SECRET_KEY`, `API_PUBLIC_URL`.

### 0.6 (parts 2 to 4): permissions, ownership, push
- Edit and delete of others' records (decided with the user): expenses by their author or the household owner; incomes by the member who received them or the owner; settlements by the two members in the payment or the owner. Others get 403. Confirming a variable bill draft stays open to any member.
- Personal insights are listed, counted, read, marked and deleted only by their owner; household-level insights stay shared.
- `internal/ownership`: a category must belong to the household (expenses, goals, recurring expenses; 422), a payment method to the receiver of an income (incomes, recurring incomes) or the creator of a recurring expense (404, as expenses already did).
- Push: an endpoint must be https on a known push service (FCM, Mozilla, Apple, Windows), else 422 (blind SSRF); an endpoint registered to another user is a 409 instead of being reassigned. `PUSH_EXTRA_HOSTS` is for tests only.

### 0.6 (part 1): client IP, body cap, insight generation
- `httpx.RealIP`: `X-Forwarded-For` is read only from `TRUSTED_PROXIES` (Traefik's network) and `CF-Connecting-IP` only when the connection came from a Cloudflare edge (`CLOUDFLARE_RANGES`, defaults to Cloudflare's published list). Before, any client could claim any IP, getting around the sign-in rate limits and writing a fake IP into the audit log. Staging needs `TRUSTED_PROXIES=10.0.0.0/8`.
- Every JSON route caps the request body at 1 MB ("el pedido es demasiado grande"); support uploads keep their own limits.
- `POST /insights/generate` is for superadmins only, and honors `?at=` only with `ENV=dev`.
- Decided with the user: rate limits stay in memory while the API runs a single instance (same as stream tickets).

### 0.17: security audit log
- Migration `000027`: `audit_events` (account, actor, action, target, IP, user agent, device label, metadata without amounts).
- Recorded: sign-in (password, Google, register), failed sign-in on an existing account (in that account's activity), logout, logout-all, password change and reset, email change requested and confirmed, account deletion, household joined, member removed, ownership transfer, and every superadmin action (household restore and purge today).
- A sign-in from a device (browser and OS) not used in 90 days, other than the first sign-in ever, records `login.new_device` and e-mails "Nuevo inicio de sesión" with a link to Seguridad.
- `GET /me/activity`: the user's latest 50 events (the app's "Actividad reciente").
- Retention (decided 2026-11-06, privacy policy): a daily job clears IPs older than 7 days and deletes events older than 12 months; deleting the account deletes its events (one event without a user records that an account was deleted).
- Recording never fails a request; errors are logged.

### 0.5 (part 4): verified-email gate
- `auth.Middleware.RequireVerifiedEmail`: after `RequireAuth`, lets through only users with a verified email; others get 403 `{"code":"email_unverified","message":"Verificá tu email para usar esta función."}`; fails closed if not wired. No route uses it yet: AI features, bots and paid checkout (later phases) go behind it.
- The app side of 0.5 (sign out everywhere, pending email notice) ships in `ahorra_app`.

### 0.5 (part 3): email change with verification
- Migration `000026`: `users.pending_email` and `email_verifications.new_email`.
- `PATCH /me` with a new email keeps the current one: the new address waits as `pendingEmail` (in the user JSON) and a "Confirmá tu nuevo email" link goes to the new mailbox. The verify link applies it, verified. Before, the email changed at once and stayed marked verified, so anyone with a session could move the account to an address nobody had proven.
- An address taken by another account is a 409 when requested, and again at confirmation if someone took it meanwhile (`domain.ConflictError`, Spanish message).

### 0.5 (part 2): Google sign-in and verified emails
- `POST /auth/google` links to an existing account only if its email is verified. Before, anyone could register someone else's email, never verify it, and own the account the day the real owner signed in with Google. Now it answers 409: "Ya existe una cuenta con este email sin verificar. Ingresá con tu contraseña (o recuperala con "Olvidé mi contraseña") y verificá tu email; después vas a poder entrar con Google." The app shows that message as is.
- A password reset marks the email verified (the link reached the mailbox). The real owner recovers such an account with "Olvidé mi contraseña": the intruder's sessions end and Google links.
- 409 messages no longer end in the internal `: domain: conflicto con el estado actual`; `domain.ConflictError` carries a message written for the user.

### 0.5 (part 1): server-side sessions
- Migration `000025`: `user_sessions`, one row per signed-in device. The refresh token is an opaque random value; only its SHA-256 is stored, with the user agent. No IP column on purpose (the privacy policy keeps IPs 7 days at most). Stale sessions of a user are deleted when they sign in.
- Refresh rotates the token; an already-rotated token presented again revokes the session (theft), except within 30 s of the rotation (two tabs refreshing at once), where it is only refused. Sessions slide: 7 days from the last use.
- Logout revokes the session on the server (a copied refresh token used to keep working). `POST /auth/logout-all` signs out every device.
- A password reset revokes every session; a password change every session but the current one; account deletion every session. Refresh refuses a deleted user.
- Access tokens carry the session id (`sid`). The refresh token is no longer a JWT: `JWT_REFRESH_SECRET` is gone (remove it from Coolify; ignored if left).
- The refresh cookie is brand-neutral: `refresh` in dev, `__Host-refresh` in prod (was `ahorra_refresh`). Everyone signs in again once after this deploy.

### 0.9: insights stream
- **Breaking (paired with `ahorra_app` 0.9):** the stream opens with a single-use ticket. `POST /insights/stream-ticket` (auth + household) returns `{ticket, expiresIn: 60}`; `GET /insights/stream?ticket=` redeems it. `?access_token=` is no longer accepted: the access token in the URL ended up in any log that records URLs.
- The stream is no longer cut every 30 seconds: it is mounted outside the timeout group and pushes the server's write deadline forward on each write. Ping every 25 s stays.
- The stream answered 500 in dev (the dev logger's writer is not an `http.Flusher`); it flushes through `http.ResponseController` now.
- Query strings no longer reach the request logs (`httpx.RedactQueryInLogs`).
- Tickets are kept in memory, which is fine with one API instance; with more, they move to the database.
- `DEPLOY.md`: `DATABASE_URL` must be a direct connection (no transaction pooler) for `LISTEN/NOTIFY`. Staging connects to the Coolify Postgres directly.

### 0.4: drafts are not spending
- Unconfirmed drafts (variable recurring bills waiting for the real amount) no longer count anywhere: monthly report and trends, goal progress, insight sums and due-soon alerts, and balances between members all filter `status = 'confirmed'`.
- `GET /expenses` lists confirmed expenses by default, exposes `status` (it was always empty in the list) and takes `?status=confirmed|draft|all`. The app keeps showing drafts in its "Pendientes de confirmar" card (`/expenses/drafts`), so it needs no change.
- Confirming a draft rescales its installments and every member's share in the same transaction. Before, only the expense row changed: the installment and the shares kept the estimate, so balances and goals used the wrong amount.
- Found on the way: balances compared billing dates with the database's `CURRENT_DATE` (UTC); they now use today in the app timezone (0.2).

### 0.3b: recurring incomes linked to their series
- Migration `000024`: `incomes.recurring_income_id` (nullable, `ON DELETE SET NULL`, partial index), like `expenses.recurring_expense_id`. Incomes generated before it stay `NULL` (no reliable match to their series).
- Recurring income generation records the series and checks for an income on that date first: a run that booked the income but failed to mark the series no longer books it twice on the retry.
- The income JSON carries `recurringIncomeId` (additive).
- Verified: `000024` up, down and up again on a scratch Postgres 18.

### 0.3: insight dedupe
- Migration `000023`: the `daily_insights` unique indexes use `NULLS NOT DISTINCT` and split refs by kind. No ref: one per household, user, day and type. Scheduled insights with a ref (goal alerts, installment due-soon alerts, card closing reminders): one per ref and day. Event insights (shared expense, invite, settlement, spike): one per ref. Existing duplicates under the new keys are deleted first, keeping the oldest row; the down migration restores the 000020 indexes.
- Fixed: household-level insights were duplicated when the job ran twice a day; a second goal over its limit the same day got no alert (goal alerts now carry the goal id as `ref_id`); only the first installment of an expense got a due-soon alert; a card got its closing reminder only once in its life.
- Recurring expense generation is idempotent: if a run created the expense but did not mark the series, the next run marks it instead of booking a duplicate (no shared transaction between the two services, so it checks for the expense first).
- Verified on a scratch Postgres 18: all migrations up, `000023` down and up again. Needs Postgres 15+ (`NULLS NOT DISTINCT`); staging runs 18, production 17.

### 0.2: timezone
- "Today", the current month and the hour of the daily jobs follow `APP_TIMEZONE` (default `America/Argentina/Buenos_Aires`) instead of UTC. Before, from 21:00 in Buenos Aires a card due that day already counted as overdue, a settlement, income or recurring series without a date started on the next day, the default report/goal month could jump ahead on the last evening of the month, and the jobs ran three hours early.
- `internal/dates` is the single place that decides which day it is; it returns calendar days as midnight UTC, comparable with the `DATE` columns. Workers convert their tick to a calendar day once and pass it to the services; the zone database is embedded in the binary.
- `APP_TIMEZONE` in config (an invalid zone stops startup), `.env.example` and `DEPLOY.md`; the image sets `TZ` so log timestamps match.
- Decided with the user: no per-household timezone column yet; it comes with the first non-Argentine market (the plan notes it there).

### 0.1 (part 5): Spanish decoder errors
- One shared `httpx.DecodeJSON` replaces 11 copies of `decodeJSON`. Its errors are Spanish and safe to show: "falta el cuerpo del pedido", "JSON inválido", "tipo de dato incorrecto" (with the field), "campo desconocido: <name>", "el pedido es demasiado grande", "algún campo tiene un valor inválido". The raw Go text (`json: unknown field ...`) no longer reaches the client; the push endpoints stop appending it too.

### 0.1 (part 4): goal currency
- A goal in USD or EUR is measured against its target converted to the household's base currency with today's rate (the blue rate expenses use). Before, ARS spending was compared with the raw USD number (USD 100 against ARS 90,000 showed 90,000%, exceeded, and fired the alert).
- Goal progress adds `targetAmountBase`, `baseCurrency` and `fxRate` (null when no conversion is needed); `targetAmount` stays in the goal's currency and `currentAmount` in the base currency. Goal alerts use the converted target.
- `goals.NewService` takes the FX service.

### 0.1 (part 3): totalCount on insights and settlements
- **Breaking:** `GET /insights` answers a page `{items, totalCount, limit, offset}` (same shape as `/expenses` and `/incomes`) instead of a bare array. The app changes in the same release (`ahorra_app` 0.1 part 3).
- `GET /settlements` adds `totalCount`, `limit` and `offset` to its `{items}` (additive).

### 0.1 (part 2): partial PATCH
- `PATCH /expenses/{id}` only changes the fields it receives. Before, it was a full replace: a description-only edit was rejected, and an omitted `categoryId` wiped the category. `categoryId: null` removes it on purpose.
- `PATCH /payment-methods/{id}/credit-card` only changes the fields it receives; `lastFour` and `debitPaymentMethodId` are cleared only with an explicit `null` (an unrelated edit used to wipe both).
- `PATCH /expenses/{id}/installments/{n}` with `billingDate: null` puts the installment back on its automatic date: the card period it falls in, or `spent_at` for non-credit methods. Before, the null was silently ignored.
- An empty PATCH body is a 422 on these endpoints. `httpx.Optional[T]` tells an absent field, `null` and a value apart.

### 0.1 (part 1): contract fixes without breaking changes
- The expense JSON carries `recurringExpenseId` (`null` for one-off expenses), so the app can tell fixed expenses apart and find a draft's series.
- `GET /expenses?kind=fixed|variable` filters on the server (fixed = generated by a recurring series); any other value is a 422.
- CORS exposes `Retry-After`, so the app can show how long to wait after a 429.
- `offset` above 2^31-1 on expenses, incomes and settlements (and an installment number that large) is a 422 instead of wrapping around or failing with 500; on insights, where bad values are ignored, it is ignored too.
- An upstream 401 from the support service becomes a 502: it means our API key is wrong, not that the user's session ended, and the app was logging the user out.
- Expense list `status` stays for 0.4, where it is pinned.

### 0.18: encrypted backups
- `scripts/backup/`: daily `pg_dump` of the Valuo databases (`ahorra`, `valuo_next`), encrypted with age to a public key and uploaded to a separate R2 bucket with `curl`; the private key is not on the server. Retention is an R2 lifecycle rule (30 days). `install.sh` sets it up (key pair, R2 check, systemd timer); `restore-test.sh` restores the newest backup into a throwaway Postgres and compares row counts with the live database.
- Found while checking: Coolify's schedule backed up only the empty `postgres` database of the production server, so `ahorra` had no backup; retention was unlimited. `DEPLOY.md` section 7 covers the new setup and the Coolify schedules kept for the other projects.

### 0.20: server hardening (Coolify server, staging)
- `DEPLOY.md` section 6: checklist, findings and the exact steps; the server scripts live in `valuo_workspace/server-ops/`.
- Security updates were blocked for about 7 months by an `apt-get update` hung since April; unblocked and applied, plus the remaining updates (Docker 29.9.3) and a reboot into kernel 7.0.
- fail2ban for SSH; SSH `MaxAuthTries 3`, `X11Forwarding no`, root by key only.
- Logs kept 7 days for every project on the server: Docker logs to journald with a 7-day limit, system logs rotate daily and keep 7. Traefik access logs stay off.
- Traefik 3.6 (end of life) to `traefik:v3.7.17`, pinned.
- Oracle firewall down to 22, 80 and 443 (3000, 8000 and 6001 closed); Coolify 2FA on.
- Pending outside 0.20: production database users get the staging split with the first v2 deploy (6.9).

### 0.15b: brand config and dead code (API)
- `BRAND_NAME` (default `Ahorro`, the name users see today) is the single source for the product name: verification, password reset, invite and monthly summary emails (subject and body), the `*_FROM_EMAIL` defaults (`<BRAND_NAME> <onboarding@resend.dev>`) and the startup banner. The emails used to say "Ahorra" while the app says "Ahorro"; they now follow the config. 1.1 sets `Valuo`. Tests run with `BrandName: "TestBrand"` and check every email uses it.
- Unchanged on purpose: the refresh cookie name (renamed in 0.5), the JWT issuer, the logger name and the deleted-account email domain (internal), the Go module path (renamed with the repos).
- Dead code removed: `resolveCreditPeriod`, three unused helpers in `insights/repository.go`, the duplicated `reports.ResendSender`, and 7 unused sqlc queries (`CountExpensesByHousehold`, `ListExpensesByHousehold`, `ListSharesByInstallment`, `ListDailyInsightsByHousehold`, `ListSettlementsByHousehold`, `ListUserIdentitiesByUser`, `UpdateUserName`); sqlc regenerated.
- Lint: the 0.15b exclusions are gone. S1016 and QF1008 are now disabled permanently with the reason in `.golangci.yml` (explicit field mapping between layers is intentional; explicit embedded selectors read better).
- Coverage 79.0% → 79.4% (less dead code); floor raised to 78.4.

### 0.21i: coverage baseline and ratchet gate
- `scripts/coverage.sh`: cross-package statement coverage of `internal/` (generated sqlc code and test helpers excluded), per-package and per-`service.go` summary, and the gate: fails below `.coverage-floor` or when the floor is lowered compared with `origin/next`. Run by `scripts/check.sh` and CI (with `-race`; CI checks out full history for the comparison).
- Baseline 79.0% (3.5% before 0.21); floor 78.0%. Every `service.go` at 80% or more except push (79.5%). Details and known gaps in `docs/testing/coverage-baseline.md`.

### 0.21: remaining audit contract bugs pinned (API side)
- Contract mismatches from the audit (section 10) that had no test yet, pinned as `KnownBug` for 0.1: the expense JSON lacks `recurringExpenseId` (1); CORS does not expose `Retry-After` (5); `PATCH /expenses/{id}` is a full replace, and `PATCH .../credit-card` wipes `lastFour` and `debitPaymentMethodId` when the edit does not resend them (6, previously characterized as normal); `billingDate: null` on `PATCH installments/{n}` is ignored (7); `GET /insights` and `GET /settlements` have no `totalCount` (9).
- `internal/app/cors_test.go`: allowed origin with credentials and `X-Request-ID` exposed, preflight with `X-Household-ID`, other origins get nothing.
- The front-end side (2, 3, 8) is pinned with `it.fails` in `ahorra_app`.

### 0.21f (part 2): goals, insights and reports tests
- `internal/goals`: CRUD with defaults, list filters, validation, household scoping; progress for household and personal goals (own expenses plus the share of shared ones), category limits, savings (income minus spending), monthly and Monday-to-Sunday weekly ranges, credit installments counted in the month they are due, ok/warning/exceeded thresholds, `?at=` (an invalid one falls back to today).
- `internal/insights`: the generators run through `App.Jobs` for fixed dates (goal alerts at 80% and 100%, savings alert from day 20, Sunday weekly review against the previous week, credit period reminder on the closing day to the card owner, installment due in 3 days); endpoints (get, unread count, mark read, mark all read, delete, list filters with malformed values ignored, manual generation, household scoping); the SSE stream (auth and membership errors, the `ready` event, and a new personal insight delivered live through LISTEN/NOTIFY).
- `internal/reports`: monthly report (spent by date, billed and due by installment, categories with "Sin categoría", fixed vs variable), empty month, default month, validation, household scoping; trends (one point per month, net = income - due, default 6 and cap 24 months); AI export with its prompt; the monthly summary email to every member.
- Wiring, no behavior change: `App.StartRealtime` starts only the insights listener (apitest `WithRealtime()`); `Jobs.MonthlyReports`; the reports worker takes a `Sender` interface and the app passes `opts.NewEmailSender(cfg.ReportFromEmail)`, the same Resend implementation as before. `reports.ResendSender` is now unused and goes in 0.15b. apitest options now carry harness settings instead of only `Config` and `app.Options`.
- Known bugs pinned (`KnownBug`):
  - 0.3: household-level insights (no user) are duplicated when the job runs twice in a day; a second personal goal over its limit gets no alert the same day; installment due-soon alerts dedupe on the expense, so only the first installment is announced.
  - 0.4: the monthly report counts unconfirmed drafts.
  - 0.6: personal insights are visible and editable by every member; `/insights/generate` is open to any member with any `?at=`; a goal's category is not checked against the household.
  - 0.9: the SSE stream is cut by the 30 s request timeout; **new, not in the audit:** in dev the stream always answers 500, because the dev request logger's writer has no `Flush` and the handler type-asserts `http.Flusher` (0.9's switch to `http.ResponseController` fixes it).
  - **New, not in the audit (assigned to 0.1):** goal progress ignores the goal's currency, so ARS spending is measured against a target set in USD or EUR.
- Coverage: goals 80.2%, insights 76.8%, reports 72.7%; module total 49.5% → 61.1%.

### 0.21f (part 1): incomes, recurring incomes and recurring expenses tests
- `internal/app`: `App.Jobs()` exposes the units of work behind the workers (recurring expenses, recurring incomes, insights) so tests run a given day's generation without the schedulers; `apitest.Server.Jobs`. Wiring only, no behavior change.
- `internal/incomes`: create (on behalf of another member, foreign currency), read, edit (amount is not editable), delete, validation, list filters, `GET /totals/income` (explicit and default range), household scoping; recurring incomes CRUD, activation, every recurrence rule, the latest occurrence booked on creation (no backfill), and the daily job (matching days only, day 31 clamps to February, once per day, inactive templates skipped).
- `internal/recurringexpenses`: CRUD, validation (recurrence, variable amounts, threshold), first occurrence on creation, the daily job (clamping, once per day, shared split, inactive), variable amounts as drafts (template amount first, then the last confirmed amount), confirmation (by any member, only once), `lastAmount`, the spike insight for the creator above the threshold, and series stats (history, average, variation).
- `scripts/check.sh` starts one throwaway Postgres with `docker run` and shares it through `TEST_DATABASE_URL`. Letting every package start its own container is flaky on Docker Desktop for Windows, and testcontainers caches the failed probe per process, so the retry added in the previous PR could not help and was removed.
- Known bugs pinned (`KnownBug`):
  - 0.4: the expense list includes drafts by default and never exposes `status`; balances count draft shares; **new, not in the audit:** confirming a draft updates the expense amount but not its installment and shares, so a shared draft keeps splitting the draft amount.
  - 0.6: any member edits or deletes another member's income; an income's or a recurring expense's payment method is not checked against the user (a recurring expense on someone else's method is accepted and then never generates).
  - 0.1: incomes `offset` above 2^31-1 answers 500; a malformed UUID in the body reaches the client as the raw Go error on field `body`.
- Coverage: incomes 78.4%, recurringexpenses 71.2%; module total 39.9% → 49.5%.

### 0.21: test infrastructure hardening
- `./scripts/check.sh` sets `TESTDB_REQUIRED=1`: without a database the integration tests now fail there (as in CI) instead of skipping. Before, a local check could pass with every database test skipped, which is how a Docker hiccup went unnoticed.
- `auth.SetBcryptCostForTests`, called by `apitest` with bcrypt's minimum cost. Production keeps cost 12. CI's auth tests took 160 to 224 s and one `change-password` request crossed the 30 s request timeout under `-race` (an intermittent 500); local auth tests went from about 25 s to under 30 s total with far fewer slow requests.

### Fix: discarded FX rates no longer reach the cache (found in 0.21d)
- When Bluelytics returned a rate of 0, refresh skipped it in the database but still put it in the in-memory cache, so `GET /exchange-rates/current` could show a currency at 0 until the next good refresh. Only valid rates reach the cache now; a discarded one keeps the last good value. Conversions were not affected (they already ignored a 0 and read the database).
- The 0.21d `KnownBug` is now a regular regression test (good snapshot, then one with a 0: the cache keeps the last good rate).

### 0.21e: expenses, balances, settlements and split rules tests
- `internal/expenses`: cash expenses (one paid installment, `installments` forced to 1), shared expenses (equal split, split-rule weights, weight 0 left out, explicit amounts with every override rule), the in-app notice for the other members, credit card installments following the loaded period and then the card defaults (last installment absorbs the remainder, spent after closing goes to the next period), foreign currency with the stored blue rate (and the 404 when there is none), every create validation, list order and filters (category, payment method, dates, text, paging, `totalCount`), edit, installment edits (paid, dates, clearing the due date), delete, confirm refused for non-drafts, and isolation between households.
- `internal/settlements` (also covers balances and split rules): debts netted per pair across shared expenses, future installments not counted, `/balances/me`, partial and full payments with note trimming and default date, the one-cent tolerance, every settlement validation, list filters, delete restoring the debt, household scoping; split rules start at 1, owner-only partial updates, validation.
- `apitest`: `CreateExpense`, `Expense`, `ExpenseDetail`, `Installment`.
- Known bugs pinned (`KnownBug`):
  - 0.6: any member edits or deletes another member's expense; `category_id` from another household accepted on create and edit; any member deletes a settlement between two others.
  - 0.1: an `offset` above 2^31-1 wraps around: settlements answer 500, expenses clamp it to 0 and echo a negative offset.
- Balances and split rules are exercised from the settlements tests, so their own packages still report 0% until 0.21i measures coverage across packages.
- Coverage: expenses 75.4%, settlements 86.2%; module total 30.9% → 39.9%.

### 0.21d: banks, payment methods, cards, periods, categories and FX tests
- `internal/paymethods`: banks (create, rename, deactivate/activate, revive by name, duplicate name, per-user privacy), payment methods (Efectivo bootstrap, `allowsInstallments` forced by kind except wallets, credit cards with current and next period and a debit account, every card validation rule, revive by name including cards, kind clash on revive, last active method cannot be deactivated, `includeInactive`), update rules (PATCH replaces `bankId` and `lastFour`), card update, and IDOR checks (another user's methods, bank or debit account).
- `internal/creditperiods`: upsert/overwrite/list/delete, status (passed, upcoming, no periods), validation (period format, dates, closing date outside the period), only the caller's credit cards.
- `internal/categories`: defaults on a new household, any member can create/edit/delete, defaults for icon and color (PATCH resets them when omitted), 40-character limit counted in characters, duplicate name, isolation between households.
- `internal/fxrates`: the Bluelytics mapping against a local fake server, upstream errors, refresh into the database (idempotent), cache hydration, the cold-cache path of `Convert`, a failed refresh keeps the cache; `GET /exchange-rates/current`.
- `apitest`: `CreatePaymentMethod`, `CreateCreditCard`, `Cash`.
- Known bugs pinned (`KnownBug`):
  - 0.2: card status takes "today" in UTC, so from 21:00 in Buenos Aires on the due date the period already shows as overdue.
  - **New, not in the audit:** a rate that refresh discards as invalid (0) is still put in the cache and served by `GET /exchange-rates/current` (fixed right after, see above).
- Coverage: paymethods 77.8%, creditperiods 80.1%, categories 83.0%, fxrates 74.7%; module total 21.6% → 30.9%.

### 0.21c: households, invites, roles, admin, push and support tests
- `internal/households`: create (defaults: categories and the owner's split rule), list isolation, read/update/delete authorization (owner, member, outsider), the `X-Household-ID` middleware, leaving, removing members, ownership transfer, membership removed on account deletion, soft delete, and the superadmin list/get/restore/purge endpoints.
- Invites: email and link, public preview, register with an invite (joins, email verified, no verification mail), register or accept with someone else's invite, accept while logged in (seeds the split rule), single use, inviting a current member, owner-only management, list pending, revoke, resend (new token, old one dead), expiry, failed delivery still returns a shareable token.
- `internal/push`: VAPID key endpoint, subscribe/upsert/list/unsubscribe/delete, per-user isolation, validation, devices removed on account deletion, the superadmin test push, and real delivery against a local push server with generated VAPID keys (VAPID auth header, 410 removes the subscription).
- `internal/support`: the proxy against a local fake upstream: identity headers and API key (never the user's bearer token), query, JSON and multipart bodies, status/body/`Retry-After` passthrough, 503 when not configured, 502 when the upstream is down.
- `apitest`: `Invite`, `AddMember`, `MakeSuperadmin`, `Res.WantError`; headers passed in a request now replace the default `Content-Type` instead of being added next to it.
- Known bugs pinned (`KnownBug`):
  - 0.6: the push upsert hands an endpoint to whichever user subscribes it last; any URL is accepted as a push endpoint (blind SSRF).
  - 0.1: the support proxy passes an upstream 401 through, which the app reads as an expired session.
  - **New, not in the audit (assigned to 0.8):** an owner who soft-deleted their only household still cannot delete the account, because the owned-households count includes deleted households.
- **Bug fixed (found by the race detector):** sending a push to a user with two or more devices raced on one shared payload buffer. webpush-go v1.4.0 (latest) writes its padding into the slice it receives, and `deliver` handed the same slice to every device goroutine. Each send now gets its own copy. The two-device delivery test under `-race` is the regression test; it failed in CI before the fix.
- Coverage: households 79.2%, push 77.6%, support 80.6%; module total 11.6% → 21.6%.

### 0.21b: auth and users characterization tests
- `internal/auth/*_test.go` (77 subtests, real router + fresh database through `apitest`). Covered: register (normalization, cookie flags, verification email, Efectivo bootstrap, duplicate email, every validation rule, malformed or unknown JSON), login (anti-enumeration: same body for wrong password and unknown email; Google-only accounts), every way an access token can be missing or invalid (scheme, refresh-as-access, wrong secret, expired, wrong `typ`) on all authenticated auth routes, refresh rotation and cookie clearing, logout, Google sign-in (new user, repeat login, linking, `Usuario` fallback, soft-deleted account, bad or empty token), forgot/reset (single-use, the newer link invalidates the older one, expiry, weak password keeps the token, Google-only account sets a password), change password (also burns pending reset links), email verification and resend, `GET/PATCH/DELETE /me` (soft delete anonymizes and frees the email, owners must transfer first), and the login and forgot-password rate limits.
- `apitest`: `Res.Cookie`, `Res.RefreshCookieValue`, `WithRefreshCookie`, `Server.Login`, `Server.Refresh`.
- Known bugs pinned (`KnownBug`):
  - 0.5: logout, password reset, password change and account deletion leave existing refresh tokens valid; Google auto-links to an account whose email was never verified; changing the email keeps the verified flag.
  - 0.6: a client rotating `X-Forwarded-For` gets around the auth rate limits.
  - 0.1: decoder errors reach the client in English (`json: unknown field ...`).
- Coverage: `internal/auth` + `internal/users` at 77.3% from these tests; module total 4.9% → 11.6%.

### 0.21a — API test harness
- `internal/app`: the wiring (repos, services, handlers, router, background jobs) moved out of `main()` with no behavior change. `app.New(ctx, cfg, pool, logger, opts)` returns the real router (`Router()`) and `Start(ctx)` for the jobs (same order, stopped in reverse). `Options` swaps only external dependencies: email sender, Google verifier, FX HTTP client, auth rate limiter; zero values are production's. `cmd/api/main.go` keeps config, migrations, pool, server and signals.
- `internal/testutil/testdb`: Postgres 18 via testcontainers v0.44.0 (or `TEST_DATABASE_URL`), migrations applied once to a template, each test gets its own database cloned from it. Skips locally without Docker, fails in CI.
- `internal/testutil/apitest`: runs the real router on `httptest` against a fresh database; fake mailer (captures emails, extracts `?token=`), fake Google verifier (same error as the real one), auth rate limits off unless `WithRealRateLimits()`, outbound HTTP blocked. Factories: `Register` (optionally verified), `VerifyEmail`, `CreateHousehold`.
- The migrations integration test now runs on `testdb` (no env var); CI uses testcontainers instead of a Postgres service.
- `github.com/moby/go-archive` v0.2.0 → v0.3.0 (GO-2026-6253, reachable from the test harness through testcontainers).
- **Bug fixed (0.10):** with two instances starting at once, the one waiting for the lock could read the transient dirty flag of the other's in-flight migration before locking and abort startup. The pre-lock read is now only a log hint; the dirty check that counts is the one under the advisory lock. Caught by the concurrent integration test once it ran on testcontainers.

### 0.11 + 0.19 — CI, first unit tests, security scanning
- CI (`.github/workflows/ci.yml`, push to `next` + PRs into it): `go mod verify`, build, vet, golangci-lint v2.15.0, `sqlc diff` (sqlc 1.30.0, the version that generated the code), `go test -race` with a Postgres 18.6 service for the migrations integration test, coverage summary (reported, gate starts in 0.21), govulncheck v1.9.0, gitleaks on full history. Actions pinned by commit SHA.
- Dependabot weekly for gomod, Dockerfile, docker-compose and Actions, targeting `next`.
- `scripts/check.sh`: local mirror of CI. Tools run pinned through `go run pkg@version` (no `tool` directives: golangci-lint advises against them, and this keeps `go.mod` free of tool deps).
- `gofmt` applied to the whole module (separate formatting-only commit).
- `.golangci.yml`: standard linters + gosec + gofmt. Triage of the 85 findings on existing code, each exclusion scoped to existing files and tied to its plan item:
  - REAL BUG (low): `offset` query params in expenses, incomes, settlements and insights handlers only check `n >= 0`; values above 2^31-1 wrap to a negative int32 and the query fails (500). To pin as KnownBug in 0.21.
  - `middleware.RealIP` deprecated/spoofable → 0.6. S1016/QF1008 style and 4 unused funcs → 0.15b. int→int32 conversions bounded by validation/DB checks → reviewed in 0.21.
  - False positives silenced inline with a reason: `.env` file read (G304), refresh cookie Secure only with `ENV=prod` (G124), detached push delivery goroutine (G118). Fake credentials in test URLs (G101) excluded for `_test.go`. Unchecked `Close`/print errors via the `std-error-handling` preset.
- `internal/testutil`: `KnownBug` (passes while a known bug reproduces, fails once fixed), `Location` (Buenos Aires TZ), `FixedClock`.
- Clock seam: `now func() time.Time` in the incomes, insights, recurring expenses and reports workers (default `time.Now`). No other behavior change.
- Characterization tests: `clampDay`, `resolveForClosingMonth`, `buildShares`, `buildInstallments` (expenses); `Convert` (fxrates, warm cache); `recurrenceMatches` + `clampDay` (incomes and recurringexpenses, same table for both copies); `alertText` (insights); worker schedules (`nextTick`, defaults).
- Known bugs pinned (`KnownBug`, fixed by 0.7): `buildShares(0.04, 6 equal members)` gives the last member -0.01; `buildInstallments(0.50, 60 installments)` makes the last installment -0.09.
- Noted, not changed: insight copy formats money as `$1234.50` (not es-AR) and shows the raw goal type (`category_limit`) to users.
- Baseline: total coverage 3.5% (before 0.21). gitleaks: no leaks in any repo's history.

### 0.10 — Migrations on deploy
- New dependency: `github.com/golang-migrate/migrate/v4` v4.20.1 (pgx/v5 driver + `iofs` source). govulncheck: 0 reachable.
- Migrations are embedded in the binary (`migrations/embed.go`); the Dockerfile no longer copies the folder.
- `internal/dbmigrate.Up`: applies pending migrations under a Postgres advisory lock (2 min wait), refuses a dirty database, never logs the password.
- `MIGRATE_ON_START` (default `false`) + `MIGRATE_DATABASE_URL` (required when on, no fallback to `DATABASE_URL`). On failure the API exits 1 before serving, so Coolify keeps the previous container.
- Deploys use two roles: `valuo_migrate` (schema owner, DDL) for migrations and `valuo_app` (DML only) at runtime. New `DEPLOY.md` documents roles, env vars and the dirty-DB procedure.
- Tests: config parsing (`internal/config`), URL/redaction/embedding units, and an integration test (`DBMIGRATE_TEST_DATABASE_URL`, skipped otherwise; testcontainers in 0.11) covering concurrent startup (exactly one instance migrates), idempotent re-run and dirty refusal. Verified against Postgres 18.6 with staging-like roles: app role can read/write but not `CREATE`/`DROP`; Docker image migrates an empty DB and registration returns 201.
- Note: govulncheck reports GO-2026-5932 (`x/crypto/openpgp`, unmaintained) in a required module; not imported by this code. Only fixed in an untagged pseudo-version; revisit when x/crypto tags a release.

### 0.15c — Toolchain refresh & vulnerable dependencies
- Go 1.25 (EOL 2026-08-19) → `go 1.27.0` + `toolchain go1.27.3` in `go.mod` (Go auto-downloads the exact toolchain on every machine).
- Docker: build `golang:1.27.3-alpine3.24`, runtime `alpine:3.24.3` (3.20 was EOL).
- Dev DB: `supabase/postgres:17` → official `postgres:18.6-alpine3.24` (same major as staging/prod); new volume `ahorra_pgdata18` (old dev volume left untouched).
- Patched 13 reachable vulnerabilities (govulncheck now clean): pgx v5.12.0 (SQL injection via dollar-quoted placeholders), chi v5.5.0 (RealIP spoofing advisories), x/net v0.62.0 (HTTP/2 DoS), x/text v0.43.0, grpc v1.85.0, plus transitive bumps.
- Note: `middleware.RealIP` is deprecated in chi v5.5 and remains spoofable by design; replaced in 0.6 (`TRUSTED_PROXIES` + `ClientIPFromXFFTrustedProxies`).
- Verified: build, vet, govulncheck, 22 migrations up/down/up on Postgres 18, API boots with `/health/ready` 200, Docker image builds (44 MB).

### 0.15a — Hygiene, no behavior change
- Stop tracking per-machine `.claude/settings.local.json` (git-ignored).
- Document `GOOGLE_OAUTH_CLIENT_ID` in `.env.example` and README.
- Dev defaults match the front's dev port: `ALLOWED_ORIGINS` / `APP_BASE_URL` → `http://localhost:3005` (config defaults + `.env.example`); `.env.example` `DATABASE_URL` uses the compose port 5433.
- README: remove claims about non-existent tests and broken links; fix Docker/soft-delete/Go-version statements; commit convention now English Conventional Commits.

Known, left for later items: `staticcheck` reports 9× S1016 (style) and 4× U1000 unused funcs (`resolveCreditPeriod`, `textPtr`, `boolPtr`, `datePtr`) → linter config in 0.11, dead code in 0.15b.
