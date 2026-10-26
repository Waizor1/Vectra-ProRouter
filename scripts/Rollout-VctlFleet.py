#!/usr/bin/env python3
"""Roll a vctl release out to the fleet's vctl routers, one router at a time.

    python3 scripts/Rollout-VctlFleet.py --version 0.7.0-r26            # plan only
    python3 scripts/Rollout-VctlFleet.py --version 0.7.0-r26 --yes      # roll out
    python3 scripts/Rollout-VctlFleet.py --version 0.7.0-r26 --hosts kirill-msk,VagrandRouter --yes

What it does, for every vctl router (engineMode xray-direct) older than the
version, in the panel's order (or --hosts' order):

1. reads the package's file name and sha256 from the channel's index (to plan;
   the router checks the index itself, below);
2. intake (a panel terminal job): the router's name, version, free RAM, overlay
   and /tmp, no stand drill, no earlier rollout still installing — a router
   that differs or is below the floors is skipped, not touched;
3. install: on the router, the channel's index is checked against the feed
   key the installer filed in /etc/opkg/keys (usign, as opkg checks a feed),
   the package must be in that index with this sha256, the ipk is downloaded
   and its sha256 checked, and `opkg install` starts detached (setsid, after
   15 s) so the job that asked returns before the package restarts the
   controller that runs it. The channel's own xray-core goes in first — one
   xray per vctl, the one it depends on — by itself, with --force-space
   (opkg counts its whole Installed-Size, which UBIFS stores in less than
   half) and, over a binary swapped in by hand or under a newer package,
   --force-reinstall / --force-downgrade; then a vctl from 0.7.0-r29 on is
   restarted onto it, so the old binary's room is back, and vctl goes in.
   Its entry is checked against the signed index too, and the floors are
   raised by what it needs (its download in /tmp; on UBIFS 1.4 × its Size on
   flash, beside the old binary the running xray holds until it restarts).
   The job refuses to run outside a five-minute
   window from when it was queued (a router offline now cannot pick it up
   unwatched later; a router whose clock is unset refuses too);
4. health, after the wait: opkg finished with rc=0, the version, the running
   controller is the new binary, xray running, the data plane loaded, and
   YouTube / Instagram / ya.ru answering — once more a minute later if not
   (a new geo set restarts xray once after an upgrade).

A router that fails its health check, or whose install cannot be followed,
STOPS the rollout: the rest are not touched. One terminal job per router at a
time, each waited for; a router busy with someone else's job is waited for,
then skipped. hh is never touched; external partners' routers and routers
exempt from the fleet policy only with --include-partners / --include-exempt.

Nothing here asks for a router's credentials: the panel's operator session
(scripts/VectraPanelCli.sh) and the routers' remote shell are the path, as for
any terminal job.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
# A checkout without apps/web's node_modules (a worktree) points at one that has them.
PANEL_CLI = Path(os.environ.get("VECTRA_PANEL_CLI", REPO / "scripts" / "VectraPanelCli.sh"))
FEED = "https://api.vectra-pro.net/artifacts/openwrt"
ARCH = "aarch64_cortex-a53"
PACKAGE = "vectra-controller-pro"
FLOORS = {"mem": 40, "overlay": 16, "tmp": 16}  # MiB, as the update guard's
NO_TOUCH = {"hh"}
OWN_PARTNER = "vectra"  # Connect's own routers; any other partnerId is someone else's
ACTIVE = ("queued", "delivered", "running")  # packages/contracts jobStateSchema
JOB_WINDOW = 300  # seconds an install job stays valid after it is queued

HOST_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$")
VERSION_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)-r(\d+)$")
SHA_RE = re.compile(r"^[0-9a-f]{64}$")
FILE_RE = re.compile(r"^[A-Za-z0-9._+-]+\.ipk$")


class Skip(Exception):
    """Nothing was done on this router; the rollout goes on."""


class Stop(Exception):
    """This router may be mid-install or unhealthy; the rollout stops."""


def panel(path: str, payload: dict | None = None, mutation: bool = False) -> object:
    cmd = ["bash", str(PANEL_CLI), "--no-ssh-env", "--json", "call", path]
    if mutation or payload is not None:
        cmd += ["--"]
    if mutation:
        cmd += ["--mutation"]
    if payload is not None:
        cmd += ["--input", json.dumps(payload)]
    p = subprocess.run(cmd, capture_output=True, text=True, stdin=subprocess.DEVNULL, cwd=PANEL_CLI.parent.parent)
    if p.returncode != 0:
        lines = p.stderr.strip().splitlines() or [p.stdout.strip()[-300:]]
        msg = next((line for line in lines if "Error" in line), lines[-1])
        raise RuntimeError(f"panel {path}: {msg[:300]}")
    try:
        return json.loads(p.stdout)
    except json.JSONDecodeError:
        raise RuntimeError(f"panel {path}: no JSON answer ({p.stdout.strip()[-200:]})") from None


def job_item(router_id: str, job_id: str) -> dict | None:
    hist = panel("terminal.history", {"routerId": router_id})
    return next((x for x in hist.get("history", []) if x.get("jobId") == job_id), None)


def run_job(router_id: str, script: str, timeout: int = 90, wait: int = 240) -> dict:
    """Queue a terminal job and wait for its own result. The panel caps a job
    at 120 s and its command at 8000 characters, and runs one job per router:
    asked while another is active, it answers with that job — someone
    else's, waited out once and then refused (Skip)."""
    timeout = min(timeout, 120)
    script = script.strip()
    if len(script) > 8000:
        raise RuntimeError("a terminal command is at most 8000 characters")
    for attempt in (1, 2):
        job = panel("terminal.queueCommand", {"routerId": router_id, "command": script, "timeoutSeconds": timeout}, mutation=True)
        if (job.get("payload") or {}).get("command") == script:
            break
        if attempt == 2:
            raise Skip(f"busy with another terminal job ({job.get('id', '?')[:8]})")
        deadline = time.time() + wait
        while time.time() < deadline:
            time.sleep(10)
            item = job_item(router_id, job["id"])
            if not item or item.get("state") not in ACTIVE:
                break
    job_id = job["id"]
    deadline = time.time() + wait
    while time.time() < deadline:
        time.sleep(6)
        item = job_item(router_id, job_id)
        if item and item.get("state") not in ACTIVE:
            if item.get("state") != "succeeded" and not item.get("stdout"):
                raise RuntimeError(f"job {job_id[:8]} {item.get('state')}: {item.get('error') or item.get('stderr') or 'no output'}")
            return item
    raise TimeoutError(f"job {job_id[:8]}: no result in {wait} s")


def kv(stdout: str | None) -> dict:
    out = {}
    for line in (stdout or "").splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


def release(version: str) -> tuple[int, ...] | None:
    m = VERSION_RE.match(version or "")
    return tuple(int(x) for x in m.groups()) if m else None


def fetch(url: str) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "vectra-rollout/1"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read()


def version_key(v: str) -> tuple[int, ...]:
    """A package version's runs of digits, as install.sh's version_ge compares them."""
    return tuple(int(x) for x in re.findall(r"\d+", v or ""))


def xray_entry(channel: str) -> dict | None:
    """The channel's own xray-core for ARCH: the one the vctl in it depends on."""
    base = f"{FEED}/{channel}/{ARCH}"
    for block in fetch(f"{base}/Packages").decode().split("\n\n"):
        fields = dict(line.split(": ", 1) for line in block.splitlines() if ": " in line)
        if fields.get("Package") == "xray-core" and fields.get("Architecture") == ARCH:
            if not FILE_RE.match(fields.get("Filename", "")) or not SHA_RE.match(fields.get("SHA256sum", "")):
                raise RuntimeError(f"{channel}'s index has an unexpected xray-core entry")
            return fields
    return None


def xray_how(it: dict, xray: dict | None) -> str:
    """How the channel's xray-core goes in, from what the intake read — keep,
    install, reinstall, downgrade — always by itself before vctl, as
    cmd/vctl/feed_xray.go."""
    if not xray:
        return "keep"
    want, pkg, binary = xray["Version"], it.get("xraypkg", ""), it.get("xrayv", "")
    if pkg == want:
        return "keep" if binary == re.sub(r"-r?\d+$", "", want) else "reinstall"
    if pkg and version_key(pkg) > version_key(want):
        return "downgrade"
    return "install"


def floors_for(it: dict, entry: dict, xray: dict | None, how: str) -> dict:
    """The intake's floors (MiB) for this router, raised by what the channel's
    xray-core needs when it goes in: its download in /tmp, and on flash the
    new binary beside the old one the running xray holds — on UBIFS/JFFS2
    about its package's Size (an AX3000T: 14.1 MB for 13.3, 18.5 MB at the
    install's low point), 1.4 × it, its Installed-Size elsewhere. When the
    vctl that runs lets go of the old xray before vctl's own opkg (0.7.0-r29
    and later, restarted onto the new one), the larger of the two steps,
    else their sum; and 4 MiB to spare."""
    floors = dict(FLOORS)
    if how == "keep" or not xray:
        return floors
    def mib(b) -> int:
        return -(-int(b or 0) // (1 << 20))
    floors["tmp"] += mib(xray.get("Size"))
    compresses = it.get("ovfs") in ("ubifs", "jffs2")
    x_need = mib(int(xray.get("Size", "0") or 0) * 14 // 10) if compresses else mib(xray.get("Installed-Size"))
    v_need = mib(entry.get("Installed-Size"))
    released = (release(it.get("version", "")) or (0,)) >= (0, 7, 0, 29)
    floors["overlay"] = max(floors["overlay"], (max(x_need, v_need) if released else x_need + v_need) + 4)
    return floors


def package_entry(channel: str, version: str) -> dict:
    base = f"{FEED}/{channel}/{ARCH}"
    entry: dict = {}
    for block in fetch(f"{base}/Packages").decode().split("\n\n"):
        fields = dict(line.split(": ", 1) for line in block.splitlines() if ": " in line)
        if fields.get("Package") == PACKAGE and fields.get("Version") == version:
            entry = fields
    if not entry:
        raise RuntimeError(f"{PACKAGE} {version} is not in {channel}'s index")
    if not FILE_RE.match(entry.get("Filename", "")) or not SHA_RE.match(entry.get("SHA256sum", "")):
        raise RuntimeError(f"{channel}'s index has an unexpected entry for {version}")
    entry["Base"] = base
    return entry


INTAKE = r"""#!/bin/sh
base=@BASE@
echo "host=$(uci -q get system.@system[0].hostname)"
echo "version=$(opkg list-installed vectra-controller-pro | cut -d' ' -f3)"
echo "mem=$(awk '/^MemAvailable/{print int($2/1024)}' /proc/meminfo)"
echo "overlay=$(df -k /overlay | awk 'NR==2{print int($4/1024)}')"
echo "tmp=$(df -k /tmp | awk 'NR==2{print int($4/1024)}')"
d=no; nft list table inet drill >/dev/null 2>&1 && d=yes; nft list table inet vdrill >/dev/null 2>&1 && d=yes; echo "drill=$d"
[ -e /tmp/vctl-rollout.run ] && echo "pending=yes" || echo "pending=no"
echo "xraypkg=$(awk '/^Package: /{p=($2=="xray-core")} p&&/^Version: /{print $2; exit}' /usr/lib/opkg/status)"
echo "xrayv=$(/usr/bin/xray version 2>/dev/null | awk 'NR==1{print $2}')"
echo "ovfs=$(awk '$2=="/overlay"{print $3}' /proc/mounts)"
d=/tmp/vctl-rollout.i; rm -rf "$d"; mkdir -p "$d"
curl -fsS -m 15 -o "$d/Packages" "$base/Packages" && curl -fsS -m 15 -o "$d/Packages.sig" "$base/Packages.sig" \
  && usign -V -q -m "$d/Packages" -x "$d/Packages.sig" -P /etc/opkg/keys && echo "index=signed" || echo "index=unverified"
rm -rf "$d"
"""

# @NAME@ placeholders, filled with shell-quoted values (install_script).
INSTALL = r"""#!/bin/sh
host=@HOST@; base=@BASE@; file=@FILE@; sha=@SHA@; ver=@VERSION@; issued=@ISSUED@
how=@XHOW@; xfile=@XFILE@; xsha=@XSHA@; xver=@XVERSION@
now=$(date +%s)
[ "$now" -ge $((issued - 600)) ] && [ "$now" -le $((issued + @WINDOW@)) ] || { echo "err=expired_or_clock"; exit 1; }
[ "$(uci -q get system.@system[0].hostname)" = "$host" ] || { echo "err=wrong_host"; exit 1; }
[ -e /tmp/vctl-rollout.run ] && { echo "err=pending"; exit 1; }
d=/tmp/vctl-rollout.d; rm -rf "$d"; mkdir -p "$d"
fail() { rm -rf "$d"; echo "err=$1"; exit 1; }
curl -fsS -m 15 -o "$d/Packages" "$base/Packages" && curl -fsS -m 15 -o "$d/Packages.sig" "$base/Packages.sig" || fail index
usign -V -q -m "$d/Packages" -x "$d/Packages.sig" -P /etc/opkg/keys || fail index_signature
signed() { awk -v p="$1" -v v="$2" -v f="$3" -v s="$4" '
  function chk() { if (P == p && V == v && F == f && S == s) ok = 1; P = ""; V = ""; F = ""; S = "" }
  /^$/ { chk(); next }
  /^Package: / { P = substr($0, 10) } /^Version: / { V = substr($0, 10) }
  /^Filename: / { F = substr($0, 11) } /^SHA256sum: / { S = substr($0, 12) }
  END { chk(); exit !ok }' "$d/Packages"; }
signed vectra-controller-pro "$ver" "$file" "$sha" || fail not_in_signed_index
curl -fsS -m 75 -o "$d/pkg.ipk" "$base/$file" || fail download
[ "$(sha256sum "$d/pkg.ipk" | cut -d' ' -f1)" = "$sha" ] || fail sha
if [ "$how" != keep ]; then
  signed xray-core "$xver" "$xfile" "$xsha" || fail xray_not_in_signed_index
fi
rm -f /tmp/vctl-rollout.log
{ echo '#!/bin/sh'; echo "how=$how; base='$base'; xfile='$xfile'; xsha='$xsha'"; cat; } > /tmp/vctl-rollout.run <<'X'
trap 'rm -rf /tmp/vctl-rollout.d /tmp/vctl-rollout.run' EXIT
sleep 15
# The channel's xray-core first (one xray per vctl), fetched here — past the
# panel's job cap — and checked against the signed index's sha256; by itself,
# with --force-space (opkg counts its whole Installed-Size; the floors held
# its real need) and, the same version or newer, its force flag — never a
# --force-* in vctl's opkg (a reinstall removes the package first; vctl's
# removal hands the router back) — and installed plainly again when one
# failed after taking it away. Then the vctl that runs, if its configs the
# new xray takes (0.7.0-r29+), is restarted onto it: the old binary's room on
# the flash comes back before vctl's own opkg, which counts its
# Installed-Size. Then vctl, whose postinst restarts it as ever.
x=/tmp/vctl-rollout.d/xray.ipk rc=0
if [ "$how" != keep ]; then
  curl -fsS -m 300 -o "$x" "$base/$xfile" > /tmp/vctl-rollout.log 2>&1 && [ "$(sha256sum "$x" | cut -d' ' -f1)" = "$xsha" ] || rc=xray_download
  if [ "$rc" = 0 ]; then
    f=""; [ "$how" = reinstall ] && f=--force-reinstall; [ "$how" = downgrade ] && f=--force-downgrade
    opkg install --force-space $f "$x" >> /tmp/vctl-rollout.log 2>&1; rc=$?
    if [ "$rc" != 0 ] && [ -n "$f" ] && ! grep -q '^Package: xray-core$' /usr/lib/opkg/status; then
      opkg install --force-space "$x" >> /tmp/vctl-rollout.log 2>&1 && rc=0
    fi
  fi
  if [ "$rc" = 0 ] && /etc/init.d/vectra-controller-pro running && awk -v a="$(opkg list-installed vectra-controller-pro | cut -d' ' -f3)" 'BEGIN {
      n = split(a, x, /[^0-9]+/); split("0.7.0-r29", y, /[^0-9]+/)
      for (i = 1; i <= 4; i++) { if (x[i] + 0 > y[i] + 0) exit 0; if (x[i] + 0 < y[i] + 0) exit 1 } exit 0 }'; then
    /etc/init.d/vectra-controller-pro restart >> /tmp/vctl-rollout.log 2>&1
    w=0; until /etc/init.d/vectra-controller-pro running && pidof xray >/dev/null || [ $w -ge 30 ]; do sleep 1; w=$((w + 1)); done
  fi
fi
[ "$rc" = 0 ] && { opkg install /tmp/vctl-rollout.d/pkg.ipk >> /tmp/vctl-rollout.log 2>&1; rc=$?; }
# vectra-geodata and vectra-reporter stay whatever a later vctl's Depends
# says: opkg removes one marked Auto-Installed once a vctl that does not name
# it replaces one that did, and no opkg command takes the mark away. The vctl
# that went in names both (none before 0.7.0-r30 is rolled out), so this opkg
# removed neither; the marks go now, by that vctl, under opkg's own lock
# (`vctl keep-companions`): a shell has no lock opkg heeds, and a look before
# a mv leaves a moment in which an opkg's write is undone.
[ "$rc" = 0 ] && { /usr/sbin/vctl keep-companions --wait 30 >> /tmp/vctl-rollout.log 2>&1 || echo "companions: the marks stay" >> /tmp/vctl-rollout.log; }
echo "rc=$rc" >> /tmp/vctl-rollout.log
X
(setsid sh /tmp/vctl-rollout.run </dev/null >/dev/null 2>&1 &)
echo "started=yes"
echo "xray=$how"
"""

HEALTH = r"""#!/bin/sh
echo "version=$(opkg list-installed vectra-controller-pro | cut -d' ' -f3)"
echo "opkg=$(tail -1 /tmp/vctl-rollout.log 2>/dev/null)"
echo "xraypkg=$(awk '/^Package: /{p=($2=="xray-core")} p&&/^Version: /{print $2; exit}' /usr/lib/opkg/status)"
echo "xrayv=$(/usr/bin/xray version 2>/dev/null | awk 'NR==1{print $2}')"
echo "marks=$(awk '/^Package: /{p=$2} /^$/{p=""} (p=="vectra-geodata"||p=="vectra-reporter")&&$0=="Auto-Installed: yes"{n++} END{print n+0}' /usr/lib/opkg/status)"
[ -e /tmp/vctl-rollout.run ] && echo "pending=yes" || echo "pending=no"
a=down
for p in $(ubus call service list '{"name":"vectra-controller-pro"}' 2>/dev/null | jsonfilter -e '@[*].instances[*].pid' 2>/dev/null); do
  case "$(readlink /proc/$p/exe 2>/dev/null)" in
    "/usr/sbin/vctl") [ "$a" = old ] || a=new ;;
    "/usr/sbin/vctl (deleted)") a=old ;;
  esac
done
echo "agent=$a"
pidof xray >/dev/null && echo "xray=running" || echo "xray=down"
echo "dataplane=$(ubus -S call vectra status 2>/dev/null | jsonfilter -e '@.dataplane.loaded' 2>/dev/null)"
echo "yt=$(curl -s -o /dev/null -m 8 -w %{http_code} https://www.youtube.com/generate_204)"
echo "ig=$(curl -s -o /dev/null -m 8 -w %{http_code} https://www.instagram.com/)"
echo "ya=$(curl -s -o /dev/null -m 6 -w %{http_code} https://ya.ru)"
echo "mem=$(awk '/^MemAvailable/{print int($2/1024)}' /proc/meminfo)"
"""


def install_script(host: str, entry: dict, xray: dict | None = None, how: str = "keep") -> str:
    x = xray or {}
    values = {"HOST": host, "BASE": entry["Base"], "FILE": entry["Filename"], "SHA": entry["SHA256sum"],
              "VERSION": entry["Version"], "ISSUED": str(int(time.time())), "WINDOW": str(JOB_WINDOW),
              "XHOW": how if xray else "keep", "XFILE": x.get("Filename", ""), "XSHA": x.get("SHA256sum", ""),
              "XVERSION": x.get("Version", "")}
    script = INSTALL
    for k, v in values.items():
        script = script.replace(f"@{k}@", v if k in ("ISSUED", "WINDOW") else shlex.quote(v))
    return script


def healthy(h: dict, version: str, xray: dict | None = None) -> list[str]:
    bad = []
    if xray:
        want = xray["Version"]
        if h.get("xraypkg") != want or h.get("xrayv") != re.sub(r"-r?\d+$", "", want):
            bad.append(f"xray {h.get('xrayv') or 'unknown'} (xray-core {h.get('xraypkg') or 'none'}), not the channel's {want}")
    if h.get("pending") != "no" or h.get("opkg") != "rc=0":
        bad.append(f"opkg {h.get('opkg') or 'not finished'}")
    if h.get("version") != version:
        bad.append(f"version {h.get('version')}")
    if h.get("agent") != "new":
        bad.append(f"controller {h.get('agent')}")
    if h.get("xray") != "running":
        bad.append("xray down")
    if h.get("dataplane") != "true":
        bad.append(f"data plane {h.get('dataplane')}")
    for k, ok in (("yt", ("204",)), ("ig", ("200", "301", "302")), ("ya", ("200", "301", "302"))):
        if h.get(k) not in ok:
            bad.append(f"{k} {h.get(k)}")
    return bad


def roll(r: dict, a: argparse.Namespace, entry: dict, target: tuple[int, ...], xray: dict | None = None) -> str:
    """One router: its result line, or Skip / Stop."""
    host, rid = r["hostname"], r["id"]
    try:
        it = kv(run_job(rid, INTAKE.replace("@BASE@", shlex.quote(entry["Base"])), timeout=60).get("stdout"))
    except (RuntimeError, TimeoutError) as e:  # offline, remote shell off: nothing was done
        raise Skip(f"no intake ({e})") from None
    print(f"   intake: {it}")
    if it.get("host") != host:
        raise Skip(f"the router calls itself {it.get('host')!r}, the panel {host!r}")
    have = release(it.get("version", ""))
    if it.get("pending") != "no":
        raise Stop("an earlier rollout's install is still running here (or its marker is left): check this router by hand first")
    if have is None:
        raise Skip(f"installed version unreadable ({it.get('version')!r}; opkg busy?)")
    if have >= target:
        raise Skip(f"already {it.get('version')}")
    if it.get("drill") != "no":
        raise Skip("a stand drill is active")
    if it.get("index") != "signed":
        raise Skip("the channel's index does not verify here (no usign, no Vectra key in /etc/opkg/keys, or no download)")
    how = xray_how(it, xray)
    floors = floors_for(it, entry, xray, how)
    low = [k for k, floor in floors.items() if int(it.get(k, "0") or 0) < floor]
    if low:
        raise Skip(f"below the floors ({', '.join(f'{k} < {floors[k]} MiB' for k in low)}; xray-core {how})")

    try:
        st = run_job(rid, install_script(host, entry, xray, how), timeout=120)
    except Skip:
        raise
    except (RuntimeError, TimeoutError) as e:
        raise Stop(f"the install job cannot be followed ({e}); it expires {JOB_WINDOW // 60} min after it was queued — check {host} by hand") from None
    res = kv(st.get("stdout"))
    if res.get("err") in ("expired_or_clock", "wrong_host", "pending"):
        raise Skip(f"install refused before doing anything ({res['err']})")
    if res.get("started") != "yes":
        raise Stop(f"install refused: {res or st.get('stderr') or st.get('error')}")
    print(f"   installing; health check in {a.wait} s")
    time.sleep(a.wait)
    bad, h = ["no answer"], {}
    for attempt in (1, 2):
        try:
            h = kv(run_job(rid, HEALTH, timeout=60).get("stdout"))
            bad = healthy(h, a.version, xray)
        except (RuntimeError, TimeoutError, Skip) as e:
            bad = [f"no health answer ({e})"]
        if not bad or attempt == 2:
            break
        print(f"   not yet healthy ({', '.join(bad)}); once more in 60 s")
        time.sleep(60)
    print(f"   health: {h}")
    if bad:
        raise Stop(f"not healthy after the upgrade ({', '.join(bad)}). {rollback_hint(it.get('version', ''))}")
    if h.get("marks") != "0":
        later = "; this vctl drops them at its next start" if target >= DROPS_MARKS_AT_START else ""
        print(f"   note: the companions' Auto-Installed marks stay ({h.get('marks')}){later}")
    return f"{it.get('version')} -> {a.version}"


# The oldest vctl whose Depends names vectra-geodata and vectra-reporter: one
# before it, installed over one that names them, removes both where they came
# in as its dependencies (marked Auto-Installed) — the reporter's spool and
# state, the geo data.
KEEPS_COMPANIONS = (0, 7, 0, 30)
# The oldest vctl with `vctl keep-companions` and the pass at its start.
DROPS_MARKS_AT_START = (0, 7, 0, 32)


def rollback_hint(previous: str) -> str:
    """How to go back by hand: to the build the router ran, or to 0.7.0-r30
    where that one is older — never below it."""
    to = previous if (release(previous) or (0,)) >= KEEPS_COMPANIONS else "0.7.0-r30"
    return (f"Roll back by hand: a terminal job running `opkg install --force-downgrade <the {to} ipk>` "
            f"(the previous builds are in /opt/vectra-prorouter-backups/pro-canary-* on the VPS) — not below 0.7.0-r30, "
            f"which would remove vectra-geodata and vectra-reporter where they are marked Auto-Installed")


def main() -> int:
    sys.stdout.reconfigure(line_buffering=True)  # progress shows while it runs, also into a file
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--version", required=True, help="e.g. 0.7.0-r26")
    ap.add_argument("--channel", default="pro-canary")
    ap.add_argument("--hosts", default="", help="comma-separated hostnames, in this order (default: every vctl router)")
    ap.add_argument("--exclude", default="", help="comma-separated hostnames never touched")
    ap.add_argument("--include-partners", action="store_true", help="also external partners' routers")
    ap.add_argument("--include-exempt", action="store_true", help="also routers exempt from the fleet policy")
    ap.add_argument("--wait", type=int, default=120, help="seconds between the install and the health check")
    ap.add_argument("--yes", action="store_true", help="roll out (without it: the plan only)")
    a = ap.parse_args()

    target = release(a.version)
    if not target:
        print(f"--version: expected like 0.7.0-r26, got {a.version!r}", file=sys.stderr)
        return 2
    if target < KEEPS_COMPANIONS:
        print(f"--version {a.version}: a vctl before 0.7.0-r30 does not depend on vectra-geodata and vectra-reporter, "
              f"and opkg would remove them where they are marked Auto-Installed", file=sys.stderr)
        return 2
    entry = package_entry(a.channel, a.version)
    print(f"package: {entry['Filename']} sha256 {entry['SHA256sum']}")
    xray = xray_entry(a.channel)
    print(f"xray-core: {xray['Filename']} sha256 {xray['SHA256sum']}" if xray else "xray-core: none in the channel (vctl alone)")

    routers = [r for r in panel("fleet.list") if r.get("engineMode") == "xray-direct" and r.get("architecture") == ARCH]
    if a.hosts:
        order = [h.strip() for h in a.hosts.split(",") if h.strip()]
        by_host = {r["hostname"]: r for r in routers}
        missing = [h for h in order if h not in by_host]
        if missing:
            print(f"not vctl routers of the fleet: {', '.join(missing)}", file=sys.stderr)
            return 2
        routers = [by_host[h] for h in order]
    exclude = {h.strip().lower() for h in a.exclude.split(",") if h.strip()} | NO_TOUCH
    left_out, plan = [], []
    for r in routers:
        host = r.get("hostname") or ""
        snap = ((r.get("latestSnapshot") or {}).get("payload") or {}) if isinstance(r.get("latestSnapshot"), dict) else {}
        names = {str(n).lower() for n in (host, r.get("displayName"), snap.get("hostname")) if n}
        why = ("excluded" if names & exclude
               else "a name the script will not put in a shell" if not HOST_RE.match(host)
               else f"partner {r.get('partnerId')} (--include-partners)" if r.get("partnerId") not in (None, OWN_PARTNER) and not a.include_partners
               else "exempt from the fleet policy (--include-exempt)" if r.get("routePolicyExempt") and not a.include_exempt
               else "")
        (left_out.append(f"{host}: {why}") if why else plan.append(r))
    print(f"vctl routers: {', '.join(r['hostname'] for r in plan) or 'none'}")
    for s in left_out:
        print(f"   left out: {s}")
    if not a.yes:
        print("plan only: pass --yes to roll out")
        return 0

    done, skipped = [], []
    for r in plan:
        print(f"\n== {r['hostname']} ({r['id'][:8]})")
        try:
            done.append(f"{r['hostname']}: {roll(r, a, entry, target, xray)}")
        except Skip as e:
            print(f"   skipped: {e}")
            skipped.append(f"{r['hostname']}: {e}")
        except Stop as e:
            print(f"STOP at {r['hostname']}: {e}\nThe rest are not touched.")
            return 1
    print("\n== rollout")
    for s in done:
        print(f"   upgraded: {s}")
    for s in skipped + left_out:
        print(f"   skipped:  {s}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
