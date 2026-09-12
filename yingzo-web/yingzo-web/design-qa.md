# Yingzo Web Design QA

- Source visual: approved soft modernist Yingzo landing style and transparent logo.
- Preview: `make dev` / `http://localhost:4173/`.
- Verified: new user frontend is the default Make target; old admin frontend remains available via `make dev-admin`.
- Verified: `/auth/login`, `/auth/register`, `/keys`, `/usage/stats`, `/usage`, `/user/profile`, `/user`, `/payment/checkout-info`, `/payment/orders/my`, and `/payment/orders` are wired through the shared request layer.
- Verified: responsive layout, sign-in/sign-up navigation, small top-left logo, API proxy to `BACKEND_URL` or `http://127.0.0.1:8080`.
- Build checks: `npm run build`; `npm run test:sites` (4 passed).

final result: passed
