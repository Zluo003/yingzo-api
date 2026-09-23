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
