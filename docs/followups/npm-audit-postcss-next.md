# Follow-up: npm audit — transitive postcss advisory under next

**Severity:** P3 (moderate advisory, no demonstrated exploit path in our usage)
**Found:** 2026-07-12 full-project audit (`npm audit --omit=dev` at repo root)

## Finding

Two moderate advisories, both the same root:

- `postcss` < 8.4.31 — "XSS via unescaped `</style>` in CSS stringify output"
  (GHSA-qx2v-qp2m-jg93), vendored transitively inside `node_modules/next`.
- `next` flagged only because it depends on that postcss.

`npm audit fix --force` proposes downgrading to `next@9.3.3` — a breaking,
absurd resolution. Do NOT run it.

## Impact assessment

The advisory concerns stringifying attacker-controlled CSS. Our apps do not
stringify untrusted CSS through postcss at runtime; postcss here is a
build-time dependency of Next. Practical risk: low.

## Recommended work order

Resolve as part of the next planned Next.js minor/patch upgrade (a normal
`next` version bump pulls a patched postcss). Do not upgrade solely for this;
bundle with the next scheduled frontend dependency refresh and verify with
`npm audit --omit=dev`, tsc, and production builds on both apps.
