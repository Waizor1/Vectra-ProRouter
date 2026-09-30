# Panel-authored operator configs

These files are **emitted by the panel**, not hand-written. They are the Go half
of a cross-language contract test: `panel_contract_test.go` proves this decoder
accepts exactly what `apps/web/src/server/vectra/xray-operator-config.ts`
produces, and `xray-operator-config.test.ts` on the panel side proves the
builder still produces exactly these bytes.

Either half drifting fails a test instead of a router.

## Why this exists

The pre-pivot panel schema modelled `dns`/`nodes`/`routing` with zod
`.default()`, so it emitted those keys unconditionally, while `config.Load` runs
with `DisallowUnknownFields`. Every xray apply job would have failed at decode
with `unknown field "dns"`.

Neither side's tests caught it: the Go e2e test feeds a Go-authored config, and
the panel test only validated against the panel's own zod schema. The bug lived
exactly in the seam that nothing tested.

## Fixed inputs

| | value |
|---|---|
| `instanceName` | `golden-router` |
| `subscriptionUrl` | `https://subscription.example.test/api/sub/GOLDEN_TOKEN` |
| profile remark (profile variant only) | `⚡Extreme Польша 🇵🇱` |

The URL is a placeholder — no real subscription token belongs in the repo.

## Regenerating after an intentional panel change

From `apps/web`, emit with the same fixed inputs and re-run both suites:

```sh
# panel side must be green first
SKIP_ENV_VALIDATION=1 DATABASE_URL=... VECTRA_SECRETS_KEY=... npx vitest run --root .
# then the Go side must still accept the new bytes
cd ../../router/vectra-controller-pro && go test ./internal/config/...
```

If the Go side rejects the regenerated golden, the panel change broke the
contract — fix the panel, do not loosen the decoder.
