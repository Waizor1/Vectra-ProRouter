# Xiaomi AX3000T Safe Test Harness

Purpose: define the default protocol for testing future programs on the live Xiaomi AX3000T without turning every test into a package install, service change, or persistent router mutation.

Status: this harness was prepared on `2026-04-04`. The supporting scripts were added locally, but no upload/start/stop/cleanup action was executed on the router while preparing this document.

## 1. Design Goal

The goal is not to "develop directly on the router". The goal is to create a bounded temporary execution lane for future experiments so that:

- the program is staged under `/tmp`, not `/overlay`
- the program is never installed as a package during early tests
- the program is never registered as a service during early tests
- the program can die automatically after a bounded time window even if SSH contact is lost
- a reboot naturally clears the staged test payload

This is the correct safety posture for the current AX3000T because:

- overlay free space is limited
- PassWall2 is already active in production mode
- the router already shows component drift between package DB and runtime binaries
- the box uses a stock dual-firmware layout, so we should avoid casual persistent changes

## 2. Harness Components

### Live inventory

- Script: [Get-OpenWrtRouterInventory.py](../../../scripts/Get-OpenWrtRouterInventory.py)
- Role: read-only live inventory and optional PassWall2 plan
- Safe because: no writes, no restarts, pinned host key

### Tmp session harness

- Script: [Manage-OpenWrtTmpProgramSession.py](../../../scripts/Manage-OpenWrtTmpProgramSession.py)
- Actions:
  - `baseline`
  - `start`
  - `status`
  - `stop`
  - `cleanup`
- Role: bounded staging and execution lane under `/tmp/codex-test/<session>`

### Router profile

- Current live router KB: [router-xiaomi-ax3000t-live-kb.md](router-xiaomi-ax3000t-live-kb.md)
- Current dated snapshot: [xiaomi-ax3000t-2026-04-04-inventory.txt](snapshots/xiaomi-ax3000t-2026-04-04-inventory.txt)
- Current dated PassWall2 plan: [xiaomi-ax3000t-2026-04-04-passwall-plan.json](snapshots/xiaomi-ax3000t-2026-04-04-passwall-plan.json)

## 3. Safety Constraints Built Into The Harness

The tmp session harness is opinionated on purpose.

By default it:

- requires a pinned SSH host key
- stages only under `/tmp/codex-test`
- rejects dangerous command families such as:
  - `opkg`
  - `uci`
  - `fw4`
  - `iptables`
  - `nft`
  - `sysupgrade`
  - reboot/reset/boot-env tooling
  - writes into `/etc/config`, `/overlay`, `/usr/bin`, `/usr/sbin`, `/etc/init.d`
- defaults the listen address to `127.0.0.1`
- rejects privileged or router-critical ports unless explicitly overridden
- bounds process lifetime with a watchdog sleep/kill path

Current reserved/risky ports for this router include at least:

- `22` SSH
- `53` dnsmasq
- `80` and `443` web management
- `7681` ttyd
- `1070` current local xray listener
- `11400` current PassWall-managed dnsmasq listener

## 4. Why This Is Safe After Loss Of Contact

This is the core model:

- payload lives under `/tmp`, so reboot removes it
- there is no package install, so package DB is untouched
- there is no service registration, so the app does not come back on boot
- there is no config write, so persistent control-plane state remains unchanged
- the harness starts a watchdog that kills the test process after `DurationSeconds`

This does not make arbitrary programs "safe". It makes the execution lane bounded.

If a future test binary crashes itself or hogs memory/CPU, the fallback remains:

- wait for watchdog timeout to expire
- reconnect and run `status`/`stop`/`cleanup`
- if the router becomes unresponsive, power cycle returns the box to the pre-test persistent state because `/tmp` is volatile

## 5. Default Development Workflow

### Step 1: fresh baseline

Use the read-only baseline action or full inventory first.

Example:

```bash
python3 ./scripts/Manage-OpenWrtTmpProgramSession.py \
  --action baseline \
  --router-host <ip> \
  --router-user <user> \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519 \
  --port 18080 \
  --process-pattern myapp
```

### Step 2: stage and start under `/tmp`

Use a high unprivileged port and loopback bind first.

Example:

```bash
python3 ./scripts/Manage-OpenWrtTmpProgramSession.py \
  --action start \
  --router-host <ip> \
  --router-user <user> \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519 \
  --local-path ./dist/myapp \
  --remote-command './myapp --listen 127.0.0.1:18080' \
  --listen-address 127.0.0.1 \
  --port 18080 \
  --duration-seconds 600
```

### Step 3: inspect status

```bash
python3 ./scripts/Manage-OpenWrtTmpProgramSession.py \
  --action status \
  --session-id <session-id> \
  --router-host <ip> \
  --router-user <user> \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519
```

### Step 4: stop and cleanup

```bash
python3 ./scripts/Manage-OpenWrtTmpProgramSession.py \
  --action cleanup \
  --session-id <session-id> \
  --router-host <ip> \
  --router-user <user> \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519
```

PuTTY password-based fallback remains supported with `--router-password` and `--router-host-key`.

## 6. Testing Rules For Future Programs

Allowed first-pass tests:

- loopback-only HTTP or TCP service on a high port
- standalone CLI that reads files and writes logs under `/tmp`
- read-only inspection tools
- parsers, exporters, or local daemons that do not own router control-plane ports

Not allowed in first-pass tmp tests:

- binding to `0.0.0.0` unless there is a clear need and explicit override
- binding to ports already used by router management or PassWall2
- writing into `/usr/bin`, `/etc`, `/overlay`
- shipping an init script before the app itself is proven stable under tmp tests
- modifying routing, DNS, firewall, Wi-Fi, switch, or VPN state

## 7. Transition Criteria To Packaging

Only move from tmp harness to package work when all of these are true:

- the binary is stable in `/tmp`
- startup arguments are known and reproducible
- log path and runtime directory expectations are known
- memory and CPU behavior are acceptable for this AX3000T
- port choice and bind scope are settled
- rollback behavior is known

Then the next phase belongs in the OpenWrt app-dev KB:

- [OpenWrt app-dev KB](openwrt24-app-development-knowledge-base/README.md)

## 8. Router-Specific Guardrails

For this exact AX3000T:

- read the live KB first: [router-xiaomi-ax3000t-live-kb.md](router-xiaomi-ax3000t-live-kb.md)
- read the recovery/write-safety runbook before any step beyond `/tmp`: [08-filogic-recovery-write-safety.md](openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md)
- remember the router is already running PassWall2 in active mode
- do not treat package DB as the whole truth for components; runtime drift already exists
- prefer loopback listeners over LAN listeners until an app proves stable
- keep early experiments away from management and proxy ports

## 9. Residual Limits

The harness reduces risk, but it does not solve everything.

It cannot guarantee safety if a future binary:

- intentionally reconfigures the router on its own
- binds a different port than declared
- saturates CPU or RAM enough to delay SSH responsiveness
- interacts with kernel/network subsystems in unexpected ways

So the operating rule remains:

- tmp harness first
- package/service integration later
- before package/service integration, run the preflight from [08-filogic-recovery-write-safety.md](openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md)
- persistent changes only after explicit approval and rollback preparation
