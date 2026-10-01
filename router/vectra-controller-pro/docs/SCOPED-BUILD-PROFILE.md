# Optional scoped-v1 build profile

The default is still plain. Explicit `VECTRA_BUILD_PROFILE=scoped-v1` changes only
vault/state/logging names and literal expressions. All application tests and wire,
JSON, firewall-template, UI/RPC and Xray contracts remain unchanged. This adds
limited reverse-engineering friction, not root extraction resistance. Local keys,
live config/memory/pipe and full credential-bearing recovery backups remain usable
by owning root. No anti-debug/tiny/control-flow switches or custom crypto.

The profile uses Go1.26.3 and locally patched Garblev0.17.0; it is not an unchanged
official release. Registry provenance/base commit/module sums/tool SHA/patch and
regression hashes/scope/public fixed seed are pinned in scripts/profiles/scoped-v1.
Reviewed base source:39c484d3007e9a608ac8692dab0b9bb5f71dfc2a. The two narrow fixes
canonicalize test display import suffixes and allow Go's implicit standard race
runtime only for instrumented mains. They retain actual race detection. Do not
silently accept a tool upgrade or rebuild yielding another tool hash. Tool caches
are separate, auto Go upgrades/downloads disabled during profile use.

Run `scripts/prepare-scoped-tool.sh` in a fresh private tool root. It defaults
to offline cached registry data; `VECTRA_SCOPED_ALLOW_DOWNLOADS=1` explicitly
permits checksum-verified Go registry downloads. Source sums/commit/patch/tool
hash are checked. The reviewed host is darwin/arm64; Go1.26.3/CGO0/trimpath and
fixed linker flags reproduce the tool across source directories. Preserve the
tool build ID: Garble uses it for toolexec identity; clearing it fails before
compilation. Clearing the target vctl build ID remains supported. This tool hash
differs from the previous review binary because build metadata is stripped;
source patch remains identical. Global installation/signing is not required. Set both variables explicitly:

```
export VECTRA_SCOPED_TOOL_ROOT=/path/to/isolated-reviewed-tool-root
export VECTRA_SCOPED_SOURCE_COMMIT=<approved-full-commit>
VECTRA_BUILD_PROFILE=scoped-v1 scripts/build-local-ipk.sh /private/output/scoped-v1
scripts/profile-scoped.sh test -count=1 ./...
scripts/profile-scoped.sh test -race -count=1 ./internal/vault ./internal/state ./internal/logging ./internal/supervisor ./internal/coreengine/xray ./cmd/vctl
scripts/profile-scoped.sh test -c -o /private/output/vctl-scoped.test ./cmd/vctl
```

Tool root layout:tools/garble-partial-race-trial,modcache,gocache,
garblecache-partial-race-fix. Exact Go/module source plus pinned patch and added
regression reproduce the reviewed tool source; recorded tool hash must match.
The builder refuses dirty/source/version/tool/manifest/patch drift. Build outputs
must use a separate directory: both profiles retain package0.7.0-r1; that candidate
version is not declared globally reserved. IPK has X-Vectra-Build-Profile:scoped-v1;
no feed/signature/install/update floor changes. Hash and profile label are required
when selecting a test package. Plain package remains fallback.

## Private diagnostics/reproduction custody

Save a diagnostic reproduction bundle under a0700 developer directory, files0600:
exact application Git archive/commit, Go version, pinned source manifest+patch+tool
binary/hash, seed, GOGARBLE allowlist, flags/target, captured trace, archive and
artifact hashes. The fixed seed is public reproducibility input, not a secret key;
private custody protects source/trace/metadata from casual sharing. No router
configs, identity, subscriptions, keys or memory dumps belong in this bundle.
Source and reproducible inputs reconstruct Garble's reverse mappings; no runtime
secret export or special router debug access is needed. Never add source/tool or
trace bundles to IPK or ordinary support exports.

Synthetic exact-profile diagnostic proof (vault validator callback panic before
key creation, only a private temporary synthetic JSON file):

```
scripts/profile-scoped.sh build -trimpath -buildvcs=false -o /private/output/probe ./scripts/testdata/scoped-diagnostics
/private/output/probe > /private/output/trace.txt 2>&1  # expected nonzero
scripts/profile-scoped.sh reverse ./scripts/testdata/scoped-diagnostics /private/output/trace.txt
```

Reverse omits unsupported build-only-trimpath flag. Retain identical source,
seed,scope,tool and Go. Testdata fixture is developer-only, never packaged. Existing
production raw traceback suppression remains; successful synthetic reverse does
not promise automatic production crash capture. Sensitive traces need redaction. `probe nil` also captures a nil-dereference
example: reverse recovers function/file, but non-call-site fallback line numbers
can be inaccurate. This is an upstream documented limitation; never treat a
fallback line as exact. Retain a private `-debugdir` transformed/original source
bundle for manual diagnosis. Call-site panic recovery is tested separately.

## Gates

Review exact profile source/IPK and transformed normal/race suites, intentional
negative-race proof, native-Xray, same-profile bridge worker/Connect contracts,
recursive fake-canary artifact/export and permissions checks, repeat builds and
private diagnostic reverse. Prior tests on693850dc are supporting evidence, not
blanket approval for a new package. Hardware OpenWrt remains a separate authorized
trial: stdin/EOF/orphans/procd/cores, lowpriv ubus, RAM/latency/power-loss,
migration/reboot/sysupgrade/key-rich recovery and signed fallback. Mac short CLI
measurements are not router throughput. No live/publishing action is part of this
profile. No owning-root/TEE/secure-boot guarantee.
