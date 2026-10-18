#!/usr/bin/env python3
"""Roll a vctl release out to the fleet's vctl routers, one router at a time.

    python3 scripts/Rollout-VctlFleet.py --version 0.7.0-r26            # plan only
    python3 scripts/Rollout-VctlFleet.py --version 0.7.0-r26 --yes      # roll out
    python3 scripts/Rollout-VctlFleet.py --version 0.7.0-r26 --hosts kirill-msk,VagrandRouter --yes

What it does, for every vctl router (engineMode xray-direct) not yet on the
version, in the panel's order (or --hosts' order):

1. reads the package from the channel's signed index over HTTPS: its file
   name and sha256 (and the index's usign signature with the feed's key when
   Go is here: tools/feedtool verify);
2. intake (a panel terminal job): the router's version, free RAM, overlay and
   /tmp, no stand drill tables — below the floors it is skipped, not touched;
3. install: downloads the ipk on the router, checks its sha256, and starts
   `opkg install` detached (setsid, after 15 s) so the job that asked
   returns before the package restarts the controller that runs it;
4. health, after the wait: the version, xray running, the data plane loaded,
   and YouTube / Instagram / ya.ru answering — once more a minute later if not
   (a new geo set restarts xray once after an upgrade).

A router that fails its health check STOPS the rollout: the rest are not
touched, and the output says how to roll it back. One terminal job per router
at a time, each waited for (the panel dedupes a router's jobs).

Nothing here asks for a router's credentials: the panel's operator session
(scripts/VectraPanelCli.sh) and the routers' remote shell are the path, as for
any terminal job.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
# A checkout without apps/web's node_modules (a worktree) points at one that
# has them; the module (tools/feedtool) likewise.
PANEL_CLI = Path(os.environ.get("VECTRA_PANEL_CLI", REPO / "scripts" / "VectraPanelCli.sh"))
MODULE = Path(os.environ.get("VCTL_MODULE", REPO / "router" / "vectra-controller-pro"))
FEED = "https://api.vectra-pro.net/artifacts/openwrt"
ARCH = "aarch64_cortex-a53"
PACKAGE = "vectra-controller-pro"
FLOORS = {"mem": 40, "overlay": 16, "tmp": 16}  # MiB, as the update guard's


def panel(path: str, payload: dict | None = None, mutation: bool = False) -> object:
    cmd = ["bash", str(PANEL_CLI), "--no-ssh-env", "--json", "call", path]
    if mutation or payload is not None:
        cmd += ["--"]
    if mutation:
        cmd += ["--mutation"]
    if payload is not None:
        cmd += ["--input", json.dumps(payload)]
    p = subprocess.run(cmd, capture_output=True, text=True, stdin=subprocess.DEVNULL, cwd=PANEL_CLI.parent.parent)
    out = p.stdout + p.stderr
    for opener in ("[", "{"):
        i = out.find(opener)
        if i >= 0:
            try:
                return json.loads(out[i:])
            except json.JSONDecodeError:
                continue
    msg = next((line.strip() for line in out.splitlines() if "message" in line.lower() or "Error:" in line), out[-300:])
    raise RuntimeError(f"panel {path}: no JSON answer ({msg[:300]})")


def run_job(router_id: str, script: str, timeout: int = 90, wait: int = 240) -> dict:
    """Queue a terminal job and wait for its result (its own jobId). The
    panel caps a job at 120 s and its command at 8000 characters."""
    timeout = min(timeout, 120)
    if len(script) > 8000:
        raise RuntimeError("a terminal command is at most 8000 characters")
    job = panel("terminal.queueCommand", {"routerId": router_id, "command": script, "timeoutSeconds": timeout}, mutation=True)
    job_id = job["id"]
    deadline = time.time() + wait
    while time.time() < deadline:
        time.sleep(6)
        hist = panel("terminal.history", {"routerId": router_id})
        item = next((x for x in hist.get("history", []) if x.get("jobId") == job_id), None)
        if item and item.get("state") not in ("queued", "running", "claimed", "dispatched"):
            return item
    raise RuntimeError(f"job {job_id} on {router_id}: no result in {wait} s")


def kv(stdout: str) -> dict:
    out = {}
    for line in (stdout or "").splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


def fetch(url: str) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "vectra-rollout/1"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read()


def package_entry(channel: str, version: str) -> dict:
    base = f"{FEED}/{channel}/{ARCH}"
    packages = fetch(f"{base}/Packages")
    if shutil.which("go") and (MODULE / "go.mod").exists():
        sig = fetch(f"{base}/Packages.sig")
        pub = fetch(f"{FEED}/{channel}/vectra-pro.pub")
        with tempfile.TemporaryDirectory() as d:
            for name, data in (("Packages", packages), ("Packages.sig", sig), ("pub", pub)):
                Path(d, name).write_bytes(data)
            v = subprocess.run(["go", "run", "./tools/feedtool", "verify", "-pub", f"{d}/pub", "-in", f"{d}/Packages", "-sig", f"{d}/Packages.sig"],
                               cwd=MODULE, capture_output=True, text=True)
            if v.returncode != 0:
                raise RuntimeError(f"the channel's index does not verify: {v.stderr[-300:]}")
        print("index signature: OK")
    entry: dict = {}
    for block in packages.decode().split("\n\n"):
        fields = dict(line.split(": ", 1) for line in block.splitlines() if ": " in line)
        if fields.get("Package") == PACKAGE and fields.get("Version") == version:
            entry = fields
    if not entry:
        raise RuntimeError(f"{PACKAGE} {version} is not in {channel}'s index")
    entry["URL"] = f"{base}/{entry['Filename']}"
    return entry


INTAKE = r"""#!/bin/sh
echo "host=$(uci -q get system.@system[0].hostname)"
echo "version=$(opkg list-installed vectra-controller-pro | cut -d' ' -f3)"
echo "mem=$(awk '/^MemAvailable/{print int($2/1024)}' /proc/meminfo)"
echo "overlay=$(df -k /overlay | awk 'NR==2{print int($4/1024)}')"
echo "tmp=$(df -k /tmp | awk 'NR==2{print int($4/1024)}')"
d=no; nft list table inet drill >/dev/null 2>&1 && d=yes; nft list table inet vdrill >/dev/null 2>&1 && d=yes; echo "drill=$d"
"""

INSTALL = r"""#!/bin/sh
[ "$(uci -q get system.@system[0].hostname)" = "{host}" ] || {{ echo "err=wrong_host"; exit 1; }}
curl -fsS -m 120 -o /tmp/vctl-rollout.ipk "{url}" || {{ echo "err=download"; exit 1; }}
[ "$(sha256sum /tmp/vctl-rollout.ipk | cut -d' ' -f1)" = "{sha}" ] || {{ rm -f /tmp/vctl-rollout.ipk; echo "err=sha"; exit 1; }}
cat > /tmp/vctl-rollout.run <<'X'
#!/bin/sh
sleep 15
opkg install /tmp/vctl-rollout.ipk > /tmp/vctl-rollout.log 2>&1; echo "rc=$?" >> /tmp/vctl-rollout.log; rm -f /tmp/vctl-rollout.ipk
X
(setsid sh /tmp/vctl-rollout.run </dev/null >/dev/null 2>&1 &)
echo "started=yes"
"""

HEALTH = r"""#!/bin/sh
echo "version=$(opkg list-installed vectra-controller-pro | cut -d' ' -f3)"
echo "opkg=$(tail -1 /tmp/vctl-rollout.log 2>/dev/null)"
pidof xray >/dev/null && echo "xray=running" || echo "xray=down"
echo "dataplane=$(ubus -S call vectra status 2>/dev/null | jsonfilter -e '@.dataplane.loaded' 2>/dev/null)"
echo "yt=$(curl -s -o /dev/null -m 8 -w %{http_code} https://www.youtube.com/generate_204)"
echo "ig=$(curl -s -o /dev/null -m 8 -w %{http_code} https://www.instagram.com/)"
echo "ya=$(curl -s -o /dev/null -m 6 -w %{http_code} https://ya.ru)"
echo "mem=$(awk '/^MemAvailable/{print int($2/1024)}' /proc/meminfo)"
"""


def healthy(h: dict, version: str) -> list[str]:
    bad = []
    if h.get("version") != version:
        bad.append(f"version {h.get('version')}")
    if h.get("xray") != "running":
        bad.append("xray down")
    if h.get("dataplane") != "true":
        bad.append(f"data plane {h.get('dataplane')}")
    for k, ok in (("yt", ("204",)), ("ig", ("200", "301", "302")), ("ya", ("200", "301", "302"))):
        if h.get(k) not in ok:
            bad.append(f"{k} {h.get(k)}")
    return bad


def main() -> int:
    sys.stdout.reconfigure(line_buffering=True)  # progress shows while it runs, also into a file
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--version", required=True, help="e.g. 0.7.0-r26")
    ap.add_argument("--channel", default="pro-canary")
    ap.add_argument("--hosts", default="", help="comma-separated hostnames, in this order (default: every vctl router)")
    ap.add_argument("--exclude", default="", help="comma-separated hostnames never touched")
    ap.add_argument("--wait", type=int, default=120, help="seconds between the install and the health check")
    ap.add_argument("--yes", action="store_true", help="roll out (without it: the plan only)")
    a = ap.parse_args()

    entry = package_entry(a.channel, a.version)
    print(f"package: {entry['Filename']} sha256 {entry['SHA256sum']}")
    routers = [r for r in panel("fleet.list") if r.get("engineMode") == "xray-direct" and r.get("architecture") == ARCH]
    exclude = {h.strip().lower() for h in a.exclude.split(",") if h.strip()}
    if a.hosts:
        order = [h.strip() for h in a.hosts.split(",") if h.strip()]
        by_host = {r["hostname"]: r for r in routers}
        missing = [h for h in order if h not in by_host]
        if missing:
            print(f"not vctl routers of the fleet: {', '.join(missing)}", file=sys.stderr)
            return 2
        routers = [by_host[h] for h in order]
    routers = [r for r in routers if r["hostname"].lower() not in exclude]
    print(f"vctl routers: {', '.join(r['hostname'] for r in routers) or 'none'}")
    if not a.yes:
        print("plan only: pass --yes to roll out")
        return 0

    done, skipped = [], []
    for r in routers:
        host, rid = r["hostname"], r["id"]
        print(f"\n== {host} ({rid[:8]})")
        it = kv(run_job(rid, INTAKE, timeout=40).get("stdout"))
        print(f"   intake: {it}")
        if it.get("version") == a.version:
            skipped.append(f"{host}: already {a.version}")
            continue
        if it.get("drill") == "yes":
            skipped.append(f"{host}: a stand drill is active")
            continue
        low = [k for k, floor in FLOORS.items() if int(it.get(k, "0") or 0) < floor]
        if low:
            skipped.append(f"{host}: below the floors ({', '.join(low)})")
            continue
        st = kv(run_job(rid, INSTALL.format(host=it.get("host", host), url=entry["URL"], sha=entry["SHA256sum"]), timeout=120).get("stdout"))
        if st.get("started") != "yes":
            print(f"   install refused: {st}")
            print("STOP: the rollout stops here; nothing else is touched.")
            return 1
        print(f"   installing; health check in {a.wait} s")
        time.sleep(a.wait)
        h = kv(run_job(rid, HEALTH, timeout=60).get("stdout"))
        bad = healthy(h, a.version)
        if bad:
            print(f"   not yet healthy ({', '.join(bad)}); once more in 60 s")
            time.sleep(60)
            h = kv(run_job(rid, HEALTH, timeout=60).get("stdout"))
            bad = healthy(h, a.version)
        print(f"   health: {h}")
        if bad:
            print(f"STOP: {host} is not healthy after the upgrade ({', '.join(bad)}). The rest are not touched.")
            print(f"   roll back: install the previous ipk from /opt/vectra-prorouter-backups/pro-canary-* the same way (this script with --version <old> --channel pro-test after staging it there).")
            return 1
        done.append(host)
    print("\n== rollout")
    for h in done:
        print(f"   upgraded: {h}")
    for s in skipped:
        print(f"   skipped:  {s}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
