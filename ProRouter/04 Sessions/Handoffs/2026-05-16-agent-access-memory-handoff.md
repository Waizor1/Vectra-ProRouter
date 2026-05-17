---
type: handoff
project: Vectra-ProRouter
created: 2026-05-16
tags:
  - handoff
  - access
  - memory
  - agents
security: sanitized-no-raw-secrets
---

# Vectra ProRouter — Agent Access & Memory Handoff

> Purpose: give a new local agent enough project context and access routing to work without starting from zero.
>
> Security rule: this file intentionally does **not** contain raw passwords, private keys, cookies, bearer tokens, DPAPI blobs, database URLs, or router tokens. Use the local-only files and commands below from this machine. Do not paste secrets into chat, tracked docs, commits, tests, or memory.

## 0. Mandatory first reads

A new agent should start here, in order:

1. `AGENTS.md`
2. `RTK.md`
3. `ProRouter/Home.md`
4. `ProRouter/00 Dashboard/Agent Workflow.md`
5. `ProRouter/00 Dashboard/Stage Board.md`
6. `ProRouter/00 Dashboard/Repo Map.md`
7. Relevant module notes under `ProRouter/02 Modules/`
8. This handoff file

For live-router or PassWall/OpenWrt work, also read:

- `ai_docs/develop/features/passwall2-ops-cheatsheet.md`
- `ai_docs/develop/features/passwall2-openwrt24-knowledge-base.md`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/06-cheatsheet.md`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/07-router-intake-template.md`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md` before live writes/sysupgrade/recovery on Filogic

## 1. Where local access material lives

Local private access docs are intentionally under a gitignored folder:

- `ProRouter/98 Local/Access Registry.md` — local access registry and current private handoff notes.
- `ProRouter/98 Local/Server Access.md` — SSH alias and key-routing notes for the production VPS.
- `ProRouter/98 Local/VectraAccess.dpapi.txt` — encrypted local access blob; do not copy into tracked docs.
- `ProRouter/98 Local/Read-VectraLocalAccess.ps1` — local helper for the encrypted access blob.

Other local-only runtime/session files:

- `.codex-runtime/vectra-panel/session.json`
- `.codex-runtime/vectra-panel/operator-cookie.txt`
- `.codex-runtime/andreyvk-recovery-credential.json`
- `apps/web/.env`
- root `.env` on the live deploy host, if inspecting the VPS

These paths are ignored by git. If a new agent needs actual secret values, it should read them locally from those files only when strictly necessary and must redact them in all outputs.

## 2. Known access surfaces and safe verification commands

### Production VPS

- SSH alias: `vectra-prod`
- Resolved target in local docs: `root@72.56.14.52`
- Dedicated local SSH key is configured in `~/.ssh/config`; see `ProRouter/98 Local/Server Access.md` for the current alias/key mapping.
- Safe verification:

```bash
ssh -o BatchMode=yes vectra-prod 'hostname; date -u; docker compose --env-file /opt/vectra-prorouter/.env -f /opt/vectra-prorouter/docker-compose.yml ps'
```

Do not print `/opt/vectra-prorouter/.env` values. Check variable names or service health instead.

### Production domains

- Operator UI: `https://router.vectra-pro.net`
- Router API: `https://api.vectra-pro.net`
- Artifact base: `https://api.vectra-pro.net/artifacts`
- Health checks:

```bash
curl -fsS https://router.vectra-pro.net/api/health
curl -fsS https://api.vectra-pro.net/healthz
```

### Operator panel CLI

Use the repo wrapper; it relies on local session/cookie state under `.codex-runtime/`:

```bash
bash ./scripts/VectraPanelCli.sh status
bash ./scripts/VectraPanelCli.sh catalog
bash ./scripts/VectraPanelCli.sh fleet overview
bash ./scripts/VectraPanelCli.sh --json fleet list
```

Useful subcommands seen in prior live work:

```bash
bash ./scripts/VectraPanelCli.sh router show <router-selector>
bash ./scripts/VectraPanelCli.sh --json fleet list
bash ./scripts/VectraPanelCli.sh terminal history <router-selector>
bash ./scripts/VectraPanelCli.sh logs history <router-selector>
bash ./scripts/VectraPanelCli.sh update controller <router-selector> -- --channel stable
```

Important CLI gotcha: subcommand flags for `update controller` go after `--`, for example `... update controller totchto-filiciy -- --channel stable`.

### Web app local env

- Example: `apps/web/.env.example`
- Local real env: `apps/web/.env` (gitignored)
- Key names expected by the app include: `DATABASE_URL`, `VECTRA_OPERATOR_USER`, `VECTRA_OPERATOR_PASSWORD`, `VECTRA_SECRETS_KEY`, `VECTRA_DEFAULT_CONTROL_DOMAIN`, `VECTRA_ROUTER_API_BASE_URL`, `VECTRA_ARTIFACT_BASE_URL`, Telegram/rescue/web-push keys, and onboarding/rescue feature flags.

Never copy the real values into docs.

### Git/GitHub SSH notes

The local access note currently distinguishes the production VPS key from other local project keys. If GitHub access is needed, verify with:

```bash
ssh -T git@github.com
```

Do not assume keys named for other VPN projects belong to Vectra unless confirmed by repo evidence.

## 3. Project shape and source-of-truth files

This repo is a mixed operations/control-plane workspace:

- `apps/web` — Next.js operator panel and router-facing API routes.
- `packages/contracts` — shared job/payload/result schemas.
- `packages/db` — Drizzle schema/migrations.
- `router/vectra-controller-agent` — Go router agent.
- `router/luci-app-vectra-controller` — OpenWrt LuCI/controller package.
- `deploy/` — VPS deploy runbooks, scripts, systemd helpers.
- `ai_docs/develop/features/` — project KB/runbooks for OpenWrt, PassWall2, safe router operations.
- `ProRouter/` — Obsidian-style project memory, module status, decisions, daily session notes.
- `passwall2/`, `openwrt-24.10-src/`, `procd-src/` — optional local source mirrors; keep read-only unless explicitly asked.

For version-sensitive external facts, re-check current upstream release/API metadata before answering.

## 4. Current operational memory snapshot

As of the latest repo memory/status notes read on 2026-05-16:

- Latest prominent controller/LuCI production lane is around `0.1.13-r23` for typed onboarding jobs.
- `yuranrod-msk` completed the panel-owned onboarding pilot with green five-slot route proof.
- `collect_optimization_baseline` exists as a read-only diagnostic job for router evidence gathering. It collects inventory/resources/service health, Xray-like RSS/thread stats, conntrack pressure, PassWall global config surface, bounded logs, and optional fleet route verification without writing router config.
- For optimization work, gather baseline evidence first. Do not tune Xray/PassWall blindly.
- The router safety/resource guard is important: low RAM, low `/overlay`, low `/tmp`, service degradation, OOM/crash evidence can block heavy jobs by design.
- `fleet.monitoring` is the live truth lane for current router health/reachability. `update.versionDriftWorkspace` is for rollout/version drift, not outage triage.
- Controller self-update success should be runtime-confirmed by the restarted binary’s `controllerRuntimeVersion`, not only package metadata.
- For low-memory routers, empty/failed terminal/self-update jobs can mean the router-side resource guard rejected execution before shell commands ran.

## 5. Known router/route-policy memories to preserve

Do not treat these as current live truth without rechecking, but keep them as important prior context:

- Normal non-`hh` fleet route policy: standard `myshunt` slots for `WorldProxy`, `YouTube`, `Special`, `Tiktok`, and `DiscordVoiceUdp`; Discord voice uses UDP ports `19294-19344,50000-50100` plus `mux=1`, `mux_concurrency=-1`, `xudp_concurrency=16` on the selected node.
- `hh` is a no-touch exception in fleet route policy work.
- `totchto-filiciy`: prior stable bindings included `WorldProxy=5t41GjFB`, `YouTube=OimBMZcM`, `Special=LrFNLcHV`, `Tiktok=aPQVHIgJ`, `DiscordVoiceUdp=O9r7ieQq`; the plain-NL canonical `Special` candidate had been unhealthy, so preserving the live-good fallback mattered.
- `Kirill-MSK`: prior final state was active/approved/live-import with Discord UDP rule/tuning present and low-memory warnings as a watch item.
- `1111111111`: historically important AX3000T canary; had Xray OOM / proxy runtime missing incidents and Discord voice tuning work. Verify live state before acting.
- `denisvitalevichtescha`: prior r20 holdback due to critically low memory; do not force update until live resources recover.
- `testrouter`: often stale/offline in previous reports; queued/offline status can be expected rather than a live rollout failure.

## 6. Standard verification lanes

### Web/control-plane local validation

Use targeted checks first, then broader checks when changing shared behavior:

```bash
pnpm --filter @vectra/web test --run
pnpm --filter @vectra/web typecheck
pnpm --filter @vectra/web lint
pnpm --filter @vectra/web build
```

### Router agent validation

```bash
cd router/vectra-controller-agent
go test ./... -count=1
go vet ./...
```

### Feed/package smoke

```bash
bash -n scripts/build-vectra-openwrt-feed.sh
git diff --check
```

### Production proof for releases

Do not call a release done from local tests alone. Combine, as applicable:

1. Local tests/typecheck/lint/build.
2. Feed/artifact availability.
3. Production health endpoints.
4. Panel version workspace or `fleet.monitoring`.
5. At least one bounded real-router proof when controller/router behavior changed.

## 7. Deploy lanes and guardrails

- Web release lane: `bash ./scripts/build-web-release-slice.sh` -> upload tarball -> run `deploy/scripts/deploy-web-release.sh` on the VPS.
- Do **not** rsync the whole repo root into `/opt/vectra-prorouter` with `--delete`; it can destroy runtime state under `deploy/runtime`.
- Artifact publishing should use `deploy/scripts/sync-runtime-artifacts.sh`, not the web release sync.
- Production deploy root contains live mutable state; protect `deploy/runtime/postgres`, `deploy/runtime/backups`, `deploy/runtime/artifacts`, and Caddy runtime dirs.
- Narrow `scp` hotfixes were used historically only for strictly in-place web file edits with an explicit backup first; prefer the release-slice lane.

## 8. Safe live-router rules

Before live writes, sysupgrade, recovery, or package changes:

1. Read the relevant OpenWrt/PassWall runbooks.
2. Confirm router identity, model, arch, OpenWrt release, package manager, free RAM, `/tmp`, `/overlay`, and current controller/LuCI versions.
3. Prefer panel-controlled guarded jobs over ad hoc shell writes.
4. For custom tmp tests, use `scripts/Manage-OpenWrtTmpProgramSession.py`.
5. For inventory, use `scripts/Get-OpenWrtRouterInventory.py` where direct router access is appropriate.
6. For pasted router facts, run `scripts/Resolve-Passwall2RouterPlan.py` before recommending packages.

Never claim router-side runtime behavior was tested if it was only inferred from source.

## 9. Finish protocol for agents

Before final handoff on meaningful work:

1. Update impacted module note(s) in `ProRouter/02 Modules/` if module state changed.
2. Update `ProRouter/00 Dashboard/Stage Board.md` if readiness/status changed.
3. Add a daily status entry:

```bash
python3 ./scripts/Add-ProRouterStatusEntry.py --summary "<what changed>" --modules "<module note name>" --next-steps "<remaining risk or next action>"
```

4. Run `python3 ./scripts/Sync-ProRouterVault.py` after structural repo changes.
5. Report exact tests/proofs run and any known gaps.

## 10. Secret hygiene checklist for the next agent

- Do not write raw secrets into tracked docs, code, commits, tests, fixtures, or memory.
- Prefer variable names, file paths, and verification status over values.
- Redact HTTP headers, cookies, tokens, passwords, DSNs, private keys, router tokens, and customer data.
- If inspecting local secret files, read the smallest necessary slice and never echo values.
- If a secret appears in tool output, do not repeat it in chat; summarize the finding and rotate/clean up if needed.
