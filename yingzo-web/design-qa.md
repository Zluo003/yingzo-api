# Yingzo Web Design QA

- Source visual: `frontend/public/design-pages/home.png` and the approved soft modernist style.
- Rendered preview: `http://localhost:4173/` at the default desktop viewport.
- Verified: small top-left logo, bone/sand palette, serif display hierarchy, restrained editorial spacing, no nested design screenshot, public CTA navigation, sign-up and sign-in transitions.
- Verified build: `npm run build`.
- Verified Sites packaging: `npm run test:sites` (4 passed).

## 2026-09-23 — 充值页与公告系统对接（功能对齐原版 frontend）

- Mock-backend walkthrough: `BACKEND_URL=http://127.0.0.1:9999 npm run dev` + `node scripts/mock-backend.mjs`（mock 仅用于本地 QA）。
- Verified recharge page: balance card with ×multiplier badge, amount presets/custom input, payment-method list with fee/limits, fee + credited preview, subscription plan cards, redeem card with history/contact, help-text markdown, orders table with status filter / cancel modal / refund-request (eligible providers), QR pay panel with countdown + save/copy QR + cancel, polling to success state, `/recharge/result` landing (resume_token resolve → 支付成功 with base/fee/total split), announcement popup (markdown body, 标记已读, serial queue), bell list modal (unread count, 全部已读, detail auto-mark-read).
- Visual: announcement popup / list modal / recharge / plans / result pages all follow the paper-ink editorial style; screenshots reviewed at 1280×720.
- Verified build: `npm run build`; Sites packaging `npm run test:sites` (4 passed).

final result: passed


## 2026-09-29 — Public product landing page

- Scope: only the public homepage implementation. Existing palette, logo, serif hierarchy and rounded primary actions retained. `src/styles.css` and all authentication, download, account, recharge, payment and announcement components remain unchanged. `App.jsx` only imports and selects the new homepage in place of `Home`.
- Content: film/story creation, commercial content, three-step interactive workflow, Agent/direct-generation modes, local projects/assets, reusable methods, FAQ and download conversion. Marketing source: the supplied `YingZo/docs/marketing/yingzo-landing-page-copy.md`; internal planning instructions are not rendered. Generated concepts and the illustrative workflow are explicitly labeled.
- Art direction: full-bleed paper-toned creative still-life; cinematic rain scene and warm product photography. Three original images created with the built-in image_gen tool and encoded as WebP (474,728 bytes combined). Exact prompts and asset inventory: `public/assets/landing/README.md`.
- Motion: staged hero entrance, scroll-linked background depth and reading indicator, intersection reveals, image hover depth, interactive workflow and mode transitions, sticky creative-control copy, FAQ and mobile navigation. Reduced-motion mode verified to have no running animations.
- Browser QA: Chromium at widths 320, 390, 768, 1280 and 1440; no horizontal overflow. Header and hero fit at 1280 × 720. Images load correctly. Verified mobile menu and Escape, anchor navigation, workflow steps, selection state, keyboard mode tabs, FAQ accordion, download/login/register routes and browser back navigation.
- Signed-in homepage QA: isolated browser context with local fixture user and mocked API responses. Account CTA resolves to `/keys`; existing API Key heading and shell render; homepage DOM and metadata clean up on navigation; no runtime exceptions. No real account or payment operations performed. Source comparison confirms existing authenticated components/routing and shared CSS were preserved.
- Build: `npm run build` passed. `npm run test:sites`: all 4 tests passed. No hosting or deployment was performed.
- Preview: http://127.0.0.1:4173/ (Vite dev server). Screenshots are under `output/playwright/`.


## 2026-09-29 — User screenshot and FAQ refinements

- Replaced the constructed software mock with the actual user-supplied 2760 × 1440 Yingzo writing-workspace screenshot. Lossless WebP encoding (143,344 bytes) is pixel-identical to the supplied image. Full aspect ratio preserved; click the screenshot or caption link to open its full-resolution version. Removed the obsolete mock interface styles and demonstration disclaimer. Workflow buttons now select explanatory captions below the actual screenshot.
- Removed the website-versus-desktop FAQ; six remaining questions renumber automatically. Platform answer now explicitly says Windows and macOS. FAQ action links are associated with their content rather than positional indices.
- Changes are confined to homepage source, assets and documentation; signed-in pages remain unchanged.
- Verified desktop (1440px) and mobile (390px) screenshot layout, full-resolution dimensions, workflow caption switching, six FAQ entries, Windows/macOS wording and retained download link. `npm run build` and `git diff --check` passed.

## 2026-09-29 — Signed-in Yingzo model plaza and RMB display

- Added `/models` to the account navigation and a compact mobile workspace selector. Retained the existing paper/sand palette, brown ink, serif page heading, account shell, and other page layouts. The plaza has text/image/video categories, counts, provider/model search, cache-price disclosure, resolution prices, loading/error/retry, empty states, and reduced-motion support.
- Added JWT-only `GET /api/v1/yingzo/models`. Selects the exact active `kind=agent, system_code=yingzo` group and reads its configured catalogue without performing a sync or modifying configuration. Only enabled, non-excluded entries are returned; a temporary availability flag does not hide an enabled model. Account/channel identifiers and supplier pricing metadata are not exposed.
- Text prices use the existing Agent channel resolver and billing calculator with the configured per-model multiplier, including current time pricing. Token display prices are quoted per token and scaled to one million, avoiding an accidental high-context sample. Distinct eligible channel prices produce a range. Images/videos use enabled resolution prices directly. Missing configuration is explicitly unavailable; zero prices stay zero.
- Usage totals, chart labels/tooltips, usage rows, profile balance, and shared payment/plan amount formatting use ¥. Existing numeric amounts are retained (no FX conversion); payment payload and SDK currency fields are unchanged.
- Tests passed: targeted Go service/handler/routing/server checks for the new plaza and Agent pricing/catalogue, plus existing general plaza regressions; production frontend build; all 4 Sites tests; both price-formatting tests (including missing, zero, very small and ranged prices).
- Browser QA: Chromium at 320/390/768/1280/1440px, no horizontal overflow. Verified categories, search and empty search, price ranges, free media prices, cache disclosure, usage/chart RMB, mobile navigation, failed request/retry. Price examples used isolated API fixtures; real endpoint payload was also rendered for image-price checks. No payment or generation requests made.
- Local integration: rebuilt/restarted only the local Yingzo API development container. Real authenticated endpoint returns CNY and exactly matches the configured enabled model identities; anonymous access returns 401. At verification time, the catalogue had 2 text, 2 image, and 3 video models. Both image models have ¥0.20/image for 1K/2K/4K. The text models have no per-model multiplier and the videos have no resolution price rows, so those five models correctly display unavailable pricing. No model or price configuration was changed.
- Preview: http://127.0.0.1:4173/models (login required). QA captures remain in ignored `output/playwright/`.

## 2026-09-29 — Download page redesign

- Replaced the split hero, oversized Chinese heading, cropped homepage illustration and below-fold platform cards with a centered Yingzo wordmark, restrained headline and two immediate download actions. Retained the paper/sand palette, brown ink, serif typography and rounded actions.
- The user removed the download-page screenshot block and both captions after review; the final page is focused on download actions and guidance, with the screenshot retained only on the homepage. Installation guidance uses three simple columns; version information and release notes use compact disclosures. Extracted the page into `DownloadPage.jsx` and scoped `download.css`, removing obsolete download-only global styles.
- Preserved the live update API and installer-preference contract, added independent platform loading, bounded request timeouts, network-error retry and safe download-link protocols. One platform's failure does not prevent the other from downloading.
- Real update service returned desktop v0.1.2 for both targets; the macOS ARM64 DMG and Windows x64 EXE each returned HTTP 200 to HEAD checks. No installer downloads or account mutations were performed.
- Browser QA passed at 320, 390, 768, 1280 and 1440px: both download actions are above the fold; no horizontal overflow. Verified release disclosures, navigation anchors, login and browser-back routing, partial-service failure and retry recovery. Entrance/reveal/button motion is disabled under reduced-motion preference.
- Frontend production build, all 4 Sites tests and both pricing-format regression tests passed. Reference captures: ignored `output/playwright/download-final-mobile.png`, `download-final-desktop.png`.
