# Local diagnostic hardening

The production controller logger redacts messages and attributes before writing
text or JSON output. Secret field names are suppressed even for short values;
free text uses the existing credential-shape redactor. Nested slog groups keep
this policy. Arbitrary unlabeled short secrets cannot reliably be identified:
callers must never log raw configuration or payload objects.

Xray's bounded log writer sanitizes complete lines, so credentials split across
process-copy chunks cannot leak through chunk boundaries. Lines exceeding 16 KiB
and PEM private-key blocks are omitted. Incomplete final lines are not persisted.
Both log generations remain 0600. Existing old logs/backups are not erased by
this change; their migration is a separate explicit recovery decision.

The production procd agent has a service-scoped zero core limit. The Xray wrapper
also sets a zero core limit, including standalone invocation. No kernel-wide core,
ptrace, debug or recovery settings change. Safe controller slog output uses stdout;
raw Go panic stderr is discarded by procd, and the duplicate Go crash file is
disabled. The production Xray wrapper likewise discards runtime stderr. This
intentionally removes automatic panic titles/stacks; procd exit/crash-loop events,
OOM incidents and ordinary structured application events remain. Developer CLI
commands outside production agent startup retain their usual stderr. Go/Xray
memory is not guaranteed zeroized, and a root owner can still inspect it.

New PassWall retirement archives use the authenticated vault envelope and retain
credentials encrypted for recovery (0600, directory0700). `restore-passwall`
validates entries and streams decrypted data to tar through stdin, without a
plaintext archive file. Recovery intentionally recreates PassWall UCI files.
Older plaintext archives remain intact and require the documented explicit tar
restore. No silent downgrade or destructive migration occurs.
System backup tools can include both encrypted data and the local decryption key:
that pairing requires an explicit backup/recovery policy, not silent exclusion.

Local builds already use `-trimpath -ldflags "-s -w"`. These remove source paths
and debugger symbol tables, but do not hide Go runtime metadata or secrets. No
obfuscator was installed. Optional obfuscation needs reproducible build and crash
symbolization/recovery validation before release.

## Candidate 0.7.0-r1 compatibility and recovery

Secret files retain their existing logical paths but contain a versioned
`VCTLVAULT1` AES-256-GCM envelope instead of JSON/gzip. A root-only `.vault`
directory alongside each storage directory contains transaction metadata only.
Keys for default persistent paths live under `/etc/vectra-controller-pro-vault-keys/`
in a SHA-256 storage-directory subdirectory; runtime keys use the corresponding
`/var/run/vectra-controller-pro-vault-keys/` root. Custom directories use a sibling
`<storage-directory>.vault-keys/key`. Key directories are 0700 and keys 0600.
Keep original paths, transaction metadata and separate keys for full recovery. Copying an envelope to a different path fails authentication.

Migration validates the legacy format before sealing it, stages only encrypted
content, and publishes atomically. Interrupted publication can recover the
pending encrypted generation. A sealed file replaced with plaintext is refused;
invalid authentication or a missing key requires explicit recovery. Do not
rollback to an older plaintext-reading binary on migrated storage. Retain the
previous package for operator-controlled recovery, but a downgrade must not
silently export/decrypt secrets.

The agent supplies validated Xray config over private stdin; source-mode Xray
output is omitted to avoid raw parser/runtime secret disclosure. Exit status,
restarts and controller events remain. `vctl-xray-private` identifies direct
binary children, while the packaged `vctl-xray-wrapper` preserves its identity;
init recognizes both for orphan cleanup and hand-back. Other services' generic
Xray stdin processes are outside that ownership pattern.

Encrypted last-good data allows local restart/offline recovery while its key
and metadata are available. Encryption adds no subscription grace period and
does not change entitlement expiry or server enforcement. Lost keys and corrupt
vault data can prevent offline start; neither plaintext downgrade nor regenerated
keys silently restore access. An owning root can read/use the local key and live
process memory: this hardening removes easy plaintext artifact copying, not root
extraction capability. Secure deletion of old flash blocks is not guaranteed.

`vctl export-config -out /tmp/vectra-config.tgz` exports an exact allowlist of
encrypted operator/provider/cache envelopes and a manifest, with no identity,
keys, runtime, logs, temporary files or older archives. It is not a standalone
recovery backup. Config-directory copying also omits keys at the default paths.
Full sysupgrade intentionally preserves both persistent data and the separate
key root: treat that full recovery archive as sensitive. Custom common-parent
backups can include sibling keys and require equivalent exclusions.
