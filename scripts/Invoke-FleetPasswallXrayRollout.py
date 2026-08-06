#!/usr/bin/env python3
"""Драйвер массовой раскатки PassWall2 + Xray по парку Vectra-ProRouter.

Реализует рантбук .omc/skills/fleet-passwall-xray-rollout.md: триаж парка, замер
простоя, преф-лайт-гейты, ручной лейн xray, зависимости, app и приёмку.

Все мутации требуют явного --apply. Без него команда печатает план и выходит.

Скорость. Узкое место — не роутер, а раунд-трипы панели: джоб доставляется на
чек-ине (~8с), поэтому каждая лишняя фаза стоит ~40-60с. Поэтому скачивание,
своп xray и зависимости склеены в ОДИН отвязанный проход: на роутер остаётся три
ожидания (stage1 -> app -> приёмка). Дальше масштабируется только параллелью:
--parallel гоняет роутеры одновременно, дедуп джобов у панели per-router,
поэтому это безопасно.

Примеры:
    python3 scripts/Invoke-FleetPasswallXrayRollout.py triage
    python3 scripts/Invoke-FleetPasswallXrayRollout.py idle --all
    python3 scripts/Invoke-FleetPasswallXrayRollout.py preflight yuranrod-msk
    python3 scripts/Invoke-FleetPasswallXrayRollout.py rollout yuranrod-msk --apply
    python3 scripts/Invoke-FleetPasswallXrayRollout.py rollout a b c d --apply --parallel 4
"""

import argparse
import base64
import calendar
import json
import subprocess
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor

CLI = ["bash", "./scripts/VectraPanelCli.sh"]

HARD_EXCLUDE = {"hh"}          # никогда не трогать массово

XRAY_ZIP = "/tmp/xray.zip"
# /tmp — это tmpfs, то есть RAM: хвосты стоят памяти на роутере с 234 MB.
# Список закрывает ВСЕ файлы, которые драйвер создаёт, включая логи своих же
# отвязанных стадий (vstage3.log оставался, пока его сюда не внесли).
STAGING = ["/tmp/xray.zip",
           "/tmp/vstage.sh", "/tmp/vstage.log",
           "/tmp/vstage3.sh", "/tmp/vstage3.log",
           "/tmp/vapp.sh", "/tmp/vapp.log"]

MIN_MEM_MB_MANUAL = 40
# Пороги job_safety.go в агенте. Панельный апдейт пакетов — storage-класс, для него
# ПОЛ ПО RAM = 64 MB и он НЕ ослабляется манифестом (в отличие от overlay-пола, который
# для scoped-пакета опускается до ~4 MB). Ниже порога агент молча отбивает джоб, а
# панель показывает просто `failed` без причины — так упал yuranrod-msk на 43 MB.
MIN_MEM_MB_APP = 64
MIN_TMP_MB_APP = 32
# Маркер, по которому rollout отличает «лечится ребутом» от настоящего блокера.
# Свип 2026-08-06: 15 из 22 роутеров парка не проходят именно этот порог.
RAM_BLOCKER = "RAM"
MIN_OVERLAY_MB = 8
IDLE_LAN_BYTES_5S = 4096
IDLE_CONNTRACK = 60

POLL_TERMINAL = 3              # опрос terminal history
POLL_LOG = 6                   # опрос лога отвязанного скрипта
POLL_JOB = 5                   # опрос состояния панельного джоба

_print_lock = threading.Lock()


# --------------------------------------------------------------------------- io


def cli_json(args, stdin=None, what=""):
    p = subprocess.run(CLI + args, input=stdin, capture_output=True, text=True)
    try:
        return json.loads(p.stdout)
    except Exception:
        raise RuntimeError("panel CLI не отдал JSON (%s): %s"
                           % (what, (p.stdout or p.stderr)[-300:]))


def trpc(path, payload=None, mutation=False):
    args = ["call", path] + (["--", "--mutation"] if mutation else [])
    d = cli_json(args, stdin=json.dumps(payload if payload is not None else {}), what=path)
    # часть процедур (update.artifacts) отдаёт голый список, а не {result: ...}
    if isinstance(d, dict):
        return d.get("result", d)
    return d


def fleet_list():
    d = cli_json(["--json", "fleet", "list"], what="fleet list")
    return d if isinstance(d, list) else (d.get("result") or [])


def drift_rows():
    return trpc("update.versionDriftWorkspace").get("rows") or []


class Log(object):
    """Буфер вывода на роутер: при параллели строки не перемешиваются."""

    def __init__(self, host, live):
        self.host, self.live, self.lines = host, live, []

    def __call__(self, msg):
        line = "  %s" % msg
        if self.live:
            with _print_lock:
                print("[%s] %s" % (self.host, msg))
                sys.stdout.flush()
        else:
            self.lines.append(line)

    def flush(self):
        if not self.live and self.lines:
            with _print_lock:
                print("== %s" % self.host)
                print("\n".join(self.lines))
                sys.stdout.flush()


# ------------------------------------------------------------------ lane choice


def lane_decision():
    """Фаза 1: планка runtimeTarget против версии опубликованного ipk."""
    items = trpc("update.artifacts")
    if isinstance(items, dict):
        items = items.get("artifacts") or items.get("rows") or []
    bundles = [a for a in items if a.get("type") == "passwall_bundle"]
    pkgs = [a for a in items if a.get("type") == "passwall_package"]
    if not bundles:
        raise RuntimeError("в update.artifacts нет passwall_bundle")
    bundle = bundles[0]
    rt = ((bundle.get("metadata") or {}).get("runtimeTargets") or {}).get("xray-core") or {}
    target, asset = rt.get("remoteVersion"), rt.get("assetUrl")
    ipk = next((a.get("version") for a in pkgs if a.get("name") == "xray-core"), None)
    app_ipk = next((a.get("version") for a in pkgs
                    if a.get("name") == "luci-app-passwall2"), None)
    # URL'ы отдельных ipk из бандла: нужны, чтобы доставить недостающие бинари
    # (geoview) и поставить app в обход opkg на роутерах с битой регистрацией пакетов
    meta = bundle.get("metadata") or {}
    pkg_urls = {}
    for a in (meta.get("packageArtifacts") or []):
        if a.get("name") and a.get("artifactUrl"):
            pkg_urls[a["name"]] = a["artifactUrl"]
    return {
        "bundleVersion": bundle.get("version"),
        "runtimeTarget": target,
        "ipkVersion": ipk,
        "appTarget": app_ipk,
        "assetUrl": asset,
        "packageUrls": pkg_urls,
        "manualLaneRequired": bool(target and ipk and target != ipk.split("-")[0]),
    }


# ----------------------------------------------------------------- terminal ops


def queue_terminal(router_id, command, timeout=60):
    r = trpc("terminal.queueCommand",
             {"routerId": router_id, "command": command, "timeoutSeconds": timeout},
             mutation=True)
    job = r.get("job") if isinstance(r, dict) and "job" in r else r
    jid = (job or {}).get("id")
    if not jid:
        raise RuntimeError("terminal.queueCommand без job.id: %s" % str(r)[:200])
    return jid


def wait_terminal(router_id, job_id, marker, budget_s=240):
    """Ждать РОВНО свой jobId И свой маркер: дедуп иначе подсунет чужой результат."""
    deadline = time.time() + budget_s
    while time.time() < deadline:
        try:
            d = cli_json(["terminal", "history", router_id], what="terminal history")
            lr = (d.get("result", d) or {}).get("latestResult") or {}
            if lr.get("jobId") == job_id and marker in (lr.get("stdout") or ""):
                return lr.get("stdout") or ""
        except RuntimeError:
            pass
        time.sleep(POLL_TERMINAL)
    raise TimeoutError("нет результата job %s (%s)" % (job_id[:8], marker))


def probe(router, command, marker, timeout=90, budget_s=240):
    return wait_terminal(router["id"],
                         queue_terminal(router["id"], command, timeout=timeout),
                         marker, budget_s=budget_s)


def launch_detached(router, script_text, path, tag):
    b64 = base64.b64encode(script_text.encode()).decode()
    cmd = ("echo {b} | base64 -d > {p}; chmod 0755 {p}; "
           "setsid /bin/sh {p} </dev/null >/dev/null 2>&1 & sleep 2; echo {t}"
           ).format(b=b64, p=path, t=tag)
    wait_terminal(router["id"], queue_terminal(router["id"], cmd, timeout=60), tag)


def wait_for_log(router, log_path, done_marker, budget_s=900):
    deadline = time.time() + budget_s
    cmd = "echo MARKER_LOG; cat %s 2>&1" % log_path
    while time.time() < deadline:
        try:
            out = probe(router, cmd, "MARKER_LOG", timeout=60, budget_s=120)
            if done_marker in out:
                return out
        except (TimeoutError, RuntimeError):
            pass
        time.sleep(POLL_LOG)
    raise TimeoutError("скрипт не дошёл до %s" % done_marker)


def parse_kv(text):
    out = {}
    for line in (text or "").splitlines():
        if "=" in line and not line.startswith((" ", "\t")):
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


# ---------------------------------------------------------------------- probes


IDLE_CMD = (
    'echo MARKER_IDLE; '
    'echo "leases=$(wc -l < /tmp/dhcp.leases 2>/dev/null || echo 0)"; '
    'echo "conntrack=$(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null || echo 0)"; '
    'W=0; for i in $(iw dev 2>/dev/null | grep Interface | cut -d" " -f2); do '
    'W=$((W+$(iw dev $i station dump 2>/dev/null | grep -c Station))); done; echo "wifi=$W"; '
    'R1=$(cat /sys/class/net/br-lan/statistics/rx_bytes 2>/dev/null||echo 0); '
    'T1=$(cat /sys/class/net/br-lan/statistics/tx_bytes 2>/dev/null||echo 0); sleep 5; '
    'R2=$(cat /sys/class/net/br-lan/statistics/rx_bytes 2>/dev/null||echo 0); '
    'T2=$(cat /sys/class/net/br-lan/statistics/tx_bytes 2>/dev/null||echo 0); '
    'echo "lan_rx=$((R2-R1))"; echo "lan_tx=$((T2-T1))"; '
    'echo "mem_mb=$(($(grep MemAvailable /proc/meminfo | tr -s \' \' | cut -d\' \' -f2)/1024))"; '
    'echo "overlay_mb=$(($(df -k /overlay | tail -1 | tr -s \' \' | cut -d\' \' -f4)/1024))"'
)

PREFLIGHT_CMD = (
    'echo MARKER_PREFLIGHT; '
    'echo "mem_mb=$(($(grep MemAvailable /proc/meminfo | tr -s \' \' | cut -d\' \' -f2)/1024))"; '
    'echo "overlay_mb=$(($(df -k /overlay | tail -1 | tr -s \' \' | cut -d\' \' -f4)/1024))"; '
    'echo "xray_ver=$(/usr/bin/xray version 2>&1 | head -1)"; '
    'echo "xray_file=$(uci -q get passwall2.@global_app[0].xray_file)"; '
    'echo "xray_in_overlay=$([ -f /overlay/upper/usr/bin/xray ] && echo yes || echo no)"; '
    'echo "app=$(grep -m1 ^Version /usr/lib/opkg/info/luci-app-passwall2.control | cut -d" " -f2)"; '
    'echo "default_node=$(uci -q get passwall2.myshunt.default_node)"; '
    'for p in lyaml libyaml coreutils-timeout; do '
    '  echo "dep_$p=$([ -f /usr/lib/opkg/info/$p.control ] && echo yes || echo no)"; done; '
    # Depends нового app должны резолвиться ВСЕ. На yuranrod-msk гео-пакеты не были
    # зарегистрированы в opkg, и джоб падал без внятной причины (панель: просто failed).
    'for p in geoview v2ray-geoip v2ray-geosite; do '
    '  echo "geo_$p=$([ -f /usr/lib/opkg/info/$p.control ] && echo yes || echo no)"; done; '
    # Отличать «пакет не зарегистрирован» от «файла физически нет»: первое лечится
    # ручной установкой app, второе требует доставить бинарь/данные
    'echo "asset_dir=$(uci -q get passwall2.@global_rules[0].v2ray_location_asset)"; '
    'A=$(uci -q get passwall2.@global_rules[0].v2ray_location_asset); A=${A:-/usr/share/v2ray/}; '
    'echo "bin_geoview=$([ -x /usr/bin/geoview ] && echo yes || echo no)"; '
    'echo "file_geoip=$([ -s ${A}geoip.dat ] && wc -c < ${A}geoip.dat || echo no)"; '
    'echo "file_geosite=$([ -s ${A}geosite.dat ] && wc -c < ${A}geosite.dat || echo no)"; '
    'echo "enable_geoview_ip=$(uci -q get passwall2.myshunt.enable_geoview_ip)"; '
    # unzip по парку НЕ гарантирован: на yuranrod-msk его нет вовсе, и попытка
    # распаковать zip XTLS давала "sh: unzip: not found", что выглядело как битый архив
    'echo "has_unzip=$(command -v unzip >/dev/null 2>&1 && echo yes || echo no)"; '
    'echo "tmp_mb=$(($(df -k /tmp | tail -1 | tr -s \' \' | cut -d\' \' -f4)/1024))"; '
    'echo "feed=$(wget -q -O /dev/null --timeout=12 https://downloads.openwrt.org/ && echo ok || echo fail)"; '
    'echo "github=$(wget -q -O /dev/null --timeout=12 https://github.com/ && echo ok || echo fail)"'
)

def manual_app_script(app_url, geoview_url, install_geoview, asset_dir, app_target):
    """Установка app в обход opkg — для роутеров с битой регистрацией гео-пакетов.

    Почему не opkg: `opkg --force-depends install <local.ipk>` на этом opkg НЕ обходит
    проверку зависимостей — он игнорирует переданный файл, идёт искать пакет по имени в
    фидах и падает `not available from any configured src`. Позиция флага роли не играет.
    А `--force-reinstall` РАЗРУШИТЕЛЕН: сначала `Removing package ...`, потом неудачная
    загрузка по имени — и пакет остаётся УДАЛЁННЫМ (так слетел luci-app-passwall2 с
    yuranrod-msk 2026-08-06). Поэтому здесь только ручная распаковка data.tar.gz
    с регистрацией control/list, и НИКАКИХ --force-* флагов.

    Кастомное гео Vectra защищено бэкапом и посимвольной сверкой после установки.
    """
    a = asset_dir if asset_dir.endswith("/") else asset_dir + "/"
    geoview_block = ""
    if install_geoview:
        geoview_block = """echo "--- geoview ---"
if wget -q -O /tmp/gv.ipk --timeout=60 "{gv}"; then
  opkg install /tmp/gv.ipk 2>&1 | tail -3
  rm -f /tmp/gv.ipk
fi
echo "geoview_bin=$(command -v geoview || echo NONE)"
echo "geoview_pkg=$(grep -m1 ^Version /usr/lib/opkg/info/geoview.control 2>/dev/null | cut -d' ' -f2)"
""".format(gv=geoview_url)
    return """#!/bin/sh
exec >/tmp/vapp.log 2>&1
echo "START $(date -u)"
mkdir -p /tmp/geobak
cp {a}geoip.dat {a}geosite.dat /tmp/geobak/ 2>/dev/null
echo "geobak=$(wc -c < /tmp/geobak/geoip.dat 2>/dev/null)/$(wc -c < /tmp/geobak/geosite.dat 2>/dev/null)"
{geoview}echo "--- app: ручная распаковка ---"
if ! wget -q -O /tmp/app.ipk --timeout=60 "{app}"; then echo "APP=DL_FAIL"; echo DONE; exit 1; fi
rm -rf /tmp/ipkx /tmp/ctl; mkdir -p /tmp/ipkx /tmp/ctl
( cd /tmp/ipkx && gzip -dc /tmp/app.ipk | tar -x )
if [ ! -f /tmp/ipkx/data.tar.gz ]; then echo "APP=BAD_IPK"; echo DONE; exit 1; fi
tar -xzf /tmp/ipkx/data.tar.gz -C / && echo "data_extracted=yes"
tar -xzf /tmp/ipkx/control.tar.gz -C /tmp/ctl
cp /tmp/ctl/control /usr/lib/opkg/info/luci-app-passwall2.control && echo "control_registered=yes"
tar -tzf /tmp/ipkx/data.tar.gz | sed 's|^\\.||' | grep -v '/$' \\
  > /usr/lib/opkg/info/luci-app-passwall2.list && echo "list_registered=yes"
rm -rf /tmp/ipkx /tmp/ctl /tmp/app.ipk
echo "APP=$(grep -m1 ^Version /usr/lib/opkg/info/luci-app-passwall2.control 2>/dev/null | cut -d' ' -f2)"
echo "initd=$([ -f /etc/init.d/passwall2 ] && echo yes || echo NO)"
echo "appsh=$([ -f /usr/share/passwall2/app.sh ] && echo yes || echo NO)"
echo "uci_lines=$(uci show passwall2 2>/dev/null | wc -l)"
cmp -s /tmp/geobak/geoip.dat {a}geoip.dat || {{ cp /tmp/geobak/geoip.dat {a}; echo "geo_restored=geoip"; }}
cmp -s /tmp/geobak/geosite.dat {a}geosite.dat || {{ cp /tmp/geobak/geosite.dat {a}; echo "geo_restored=geosite"; }}
echo "geo_now=$(wc -c < {a}geoip.dat)/$(wc -c < {a}geosite.dat)"
rm -rf /tmp/geobak /tmp/vapp.sh
echo "DONE $(date -u)"
""".format(a=a, app=app_url, geoview=geoview_block, tgt=app_target)


def stage3_script(target_version, asset_url):
    """После апдейта app: восстановить бинарь xray, если его снесло, затем приёмка.

    Апдейт `luci-app-passwall2` до 26.7.16-r1 может УДАЛИТЬ пакет xray-core целиком
    (в 26.7.16 он убран из Depends), унося с собой /usr/bin/xray — включая только что
    положенный вручную. Наблюдалось на artem-lutfulin 2026-08-06: пакет ABSENT,
    бинаря нет, все проксируемые пробы 000. Поэтому шаг обязателен и идемпотентен.
    """
    return """#!/bin/sh
exec >/tmp/vstage3.log 2>&1
echo "START $(date -u)"
NEED=0
[ -f /usr/bin/xray ] || NEED=1
if [ $NEED -eq 0 ]; then
  case "$(/usr/bin/xray version 2>&1 | head -1)" in *{ver}*) ;; *) NEED=1;; esac
fi
echo "restore_needed=$NEED"
if [ $NEED -eq 1 ]; then
  [ -f {zip} ] || wget -q -O {zip} --timeout=60 "{url}"
  if unzip -t {zip} >/dev/null 2>&1; then
    unzip -p {zip} xray > /usr/bin/xray
    chmod 0755 /usr/bin/xray
    /etc/init.d/passwall2 restart
    n=0; while [ $n -lt 25 ]; do
      pgrep -f "passwall2/bin/xray run" >/dev/null 2>&1 && break; n=$((n+1)); sleep 1; done
    echo "restored=$(/usr/bin/xray version 2>&1 | head -1)"
  else
    echo "restored=ZIP_UNAVAILABLE"
  fi
fi
echo "app=$(grep -m1 ^Version /usr/lib/opkg/info/luci-app-passwall2.control 2>/dev/null | cut -d' ' -f2)"
echo "pkg_xray_core=$(grep -m1 ^Version /usr/lib/opkg/info/xray-core.control 2>/dev/null | cut -d' ' -f2 || echo ABSENT)"
P=$(pgrep -f "passwall2/bin/xray run" | head -1)
echo "exe=$(readlink /proc/$P/exe 2>/dev/null)"
echo "binsize=$(wc -c < /usr/bin/xray 2>/dev/null)"
echo "wrapper=$(wc -c < /usr/sbin/vectra-xray-wrapper 2>/dev/null)"
echo "xray_ver=$(/usr/bin/xray version 2>&1 | head -1)"
tr '\\0' '\\n' < /proc/$P/environ 2>/dev/null | grep -E "GOMEMLIMIT|GOGC|XRAY_LOCATION_ASSET"
echo "oom=$(cat /proc/$P/oom_score_adj 2>/dev/null)"
echo "cfg=$(XRAY_LOCATION_ASSET=/usr/share/v2ray /usr/bin/xray -test -c /tmp/etc/passwall2/acl/default/global.json 2>&1 | tail -1)"
# Пробы с ретраем: одиночный замер врёт. Сразу после рестарта PassWall живой
# домен отдаёт 000 из-за протухшего fake-IP в кэше dnsmasq, и ложный вердикт
# «сломалось» стоил разбирательств дважды. Берём лучший из двух.
for u in https://api.telegram.org/ https://www.instagram.com/ https://www.youtube.com/generate_204 https://api.ipify.org; do
  C=$(curl -s -o /dev/null -w %{{http_code}} --connect-timeout 8 --max-time 15 $u)
  case "$C" in 000|4*|5*) sleep 3; C=$(curl -s -o /dev/null -w %{{http_code}} --connect-timeout 8 --max-time 15 $u);; esac
  echo "probe $C $u"
done
echo "overlay_mb=$(($(df -k /overlay | tail -1 | tr -s ' ' | cut -d' ' -f4)/1024))"
echo "mem_mb=$(($(grep MemAvailable /proc/meminfo | tr -s ' ' | cut -d' ' -f2)/1024))"
rm -f {staging}
echo "DONE $(date -u)"
""".format(ver=target_version, url=asset_url, zip=XRAY_ZIP,
           staging=" ".join(STAGING + ["/tmp/vstage3.sh"]))


ACCEPT_CMD = (
    'echo MARKER_ACCEPT; '
    'echo "app=$(grep -m1 ^Version /usr/lib/opkg/info/luci-app-passwall2.control | cut -d" " -f2)"; '
    'P=$(pgrep -f "passwall2/bin/xray run" | head -1); '
    'echo "exe=$(readlink /proc/$P/exe 2>/dev/null)"; '
    'echo "binsize=$(wc -c < /usr/bin/xray)"; '
    'echo "wrapper=$(wc -c < /usr/sbin/vectra-xray-wrapper 2>/dev/null)"; '
    'echo "xray_ver=$(/usr/bin/xray version 2>&1 | head -1)"; '
    'tr "\\0" "\\n" < /proc/$P/environ 2>/dev/null | grep -E "GOMEMLIMIT|GOGC|XRAY_LOCATION_ASSET"; '
    'echo "oom=$(cat /proc/$P/oom_score_adj 2>/dev/null)"; '
    'echo "cfg=$(XRAY_LOCATION_ASSET=/usr/share/v2ray /usr/bin/xray -test '
    '-c /tmp/etc/passwall2/acl/default/global.json 2>&1 | tail -1)"; '
    'for u in https://api.telegram.org/ https://www.instagram.com/ '
    'https://www.youtube.com/generate_204 https://api.ipify.org; do '
    '  echo "probe $(curl -s -o /dev/null -w %{http_code} --connect-timeout 8 --max-time 15 $u) $u"; done; '
    'echo "overlay_mb=$(($(df -k /overlay | tail -1 | tr -s \' \' | cut -d\' \' -f4)/1024))"; '
    'echo "mem_mb=$(($(grep MemAvailable /proc/meminfo | tr -s \' \' | cut -d\' \' -f2)/1024))"; '
    # хвосты staging чистим здесь же, чтобы не тратить лишний раунд-трип;
    # НЕ через %-форматирование: в строке живёт curl-овский %{http_code}
    'rm -f ' + " ".join(STAGING)
)


def stage1_script(do_xray, do_deps, target_version, asset_url, deps=None):
    """Один отвязанный проход: доставить зависимости и свопнуть xray.

    Склейка нужна ради скорости — каждая отдельная фаза стоит лишнего раунд-трипа
    к панели (доставка джоба привязана к чек-ину ~8с).

    Зависимости идут ПЕРВЫМИ: в их числе может быть `unzip`, без которого ручной
    лейн xray физически невозможен. Инвариант «xray раньше app» это не нарушает —
    app ставится отдельной фазой после.
    """
    deps = deps or ["lyaml", "coreutils-timeout"]
    parts = ["#!/bin/sh", "exec >/tmp/vstage.log 2>&1", 'echo "START $(date -u)"']
    if do_deps:
        parts.append("""opkg update >/dev/null 2>&1
opkg install {deps} >/dev/null 2>&1
for p in libyaml lyaml coreutils-timeout; do
  echo "dep_$p=$([ -f /usr/lib/opkg/info/$p.control ] && echo yes || echo no)"
done
echo "has_unzip=$(command -v unzip >/dev/null 2>&1 && echo yes || echo no)"
rm -rf /var/opkg-lists /tmp/opkg-lists 2>/dev/null
""".format(deps=" ".join(deps)))
    else:
        parts.append('echo "DEPS=SKIPPED"')
    if do_xray:
        parts.append("""echo "before=$(/usr/bin/xray version 2>&1 | head -1)"
if ! command -v unzip >/dev/null 2>&1; then echo "XRAY=NO_UNZIP"; echo DONE; exit 1; fi
rm -f {zip}
if ! wget -q -O {zip} --timeout=60 "{url}"; then echo "XRAY=DOWNLOAD_FAIL"; echo DONE; exit 1; fi
if ! unzip -t {zip} >/dev/null 2>&1; then echo "XRAY=ZIP_CORRUPT"; rm -f {zip}; echo DONE; exit 1; fi
/etc/init.d/passwall2 stop; sleep 4
killall -9 xray 2>/dev/null; sleep 1
i=0; RES=SWAP_FAILED
while [ $i -lt 2 ]; do
  unzip -p {zip} xray > /usr/bin/xray
  chmod 0755 /usr/bin/xray
  V=$(/usr/bin/xray version 2>&1 | head -1)
  case "$V" in *{ver}*) RES=SWAP_OK; break;; esac
  i=$((i+1))
done
echo "XRAY=$RES"
# zip НЕ удаляем: он нужен stage3, если апдейт app снесёт пакет xray-core вместе
# с бинарём (наблюдалось на artem-lutfulin 2026-08-06). Чистится в stage3.
/etc/init.d/passwall2 start
n=0; while [ $n -lt 20 ]; do
  pgrep -f "passwall2/bin/xray run" >/dev/null 2>&1 && break
  n=$((n+1)); sleep 1
done
echo "xray_up_after_s=$n"
echo "after=$(/usr/bin/xray version 2>&1 | head -1)"
echo "wrapper=$(wc -c < /usr/sbin/vectra-xray-wrapper 2>/dev/null)"
echo "cfg=$(XRAY_LOCATION_ASSET=/usr/share/v2ray /usr/bin/xray -test -c /tmp/etc/passwall2/acl/default/global.json 2>&1 | tail -1)"
""".format(zip=XRAY_ZIP, url=asset_url, ver=target_version))
    else:
        parts.append('echo "XRAY=SKIPPED"')
    parts.append('echo "DONE $(date -u)"')
    return "\n".join(parts) + "\n"


# ------------------------------------------------------------------- selection


def resolve_targets(selectors, fleet):
    by_host = {r.get("hostname"): r for r in fleet}
    out = []
    for s in selectors:
        m = by_host.get(s) or next(
            (r for r in fleet if r["id"].startswith(s) or s == r.get("deviceIdentifier")),
            None)
        if not m:
            raise SystemExit("роутер не найден: %s" % s)
        out.append(m)
    return out


def age_hours(row):
    """Метки панели в UTC. timegm, а НЕ mktime: mktime трактует struct как
    локальное время и на UTC+3 давал ровно 3 лишних часа — весь парк «офлайн»."""
    try:
        return (time.time() - calendar.timegm(time.strptime(
            str(row.get("lastSeenAt"))[:19], "%Y-%m-%dT%H:%M:%S"))) / 3600
    except Exception:
        return 999


def eligible(row, drow):
    if (row.get("hostname") or "") in HARD_EXCLUDE:
        return "исключён жёстко"
    if row.get("importState") != "approved":
        return "importState=%s" % row.get("importState")
    if row.get("status") != "active":
        return "status=%s" % row.get("status")
    if drow and drow.get("blocked"):
        return "blocked: %s" % drow.get("blockedReason")
    if age_hours(row) > 2:
        return "офлайн %.0fч" % age_hours(row)
    return None


# -------------------------------------------------------------------- commands


def cmd_triage(args):
    fleet, drows = fleet_list(), {r.get("displayName"): r for r in drift_rows()}
    lane = lane_decision()
    print("bundle %s | планка xray %s | ipk %s | app %s | ручной лейн: %s\n" % (
        lane["bundleVersion"], lane["runtimeTarget"], lane["ipkVersion"],
        lane["appTarget"], "ДА" if lane["manualLaneRequired"] else "нет"))
    print("%-24s %-10s %-11s %-9s %s" % ("router", "ctrl", "app", "xray", "статус"))
    for r in sorted(fleet, key=lambda x: str(x.get("hostname"))):
        d = drows.get(r.get("hostname")) or {}
        why = eligible(r, d)
        xi = str(d.get("xrayInstalled") or "")
        need = [n for n, f in (("app", d.get("passwallNeedsUpdate")),
                               ("xray", d.get("xrayNeedsUpdate"))) if f]
        print("%-24s %-10s %-11s %-9s %s" % (
            str(r.get("hostname"))[:24], d.get("controllerInstalled") or "-",
            d.get("passwallInstalled") or "-",
            xi.split(" ")[1] if xi.startswith("Xray ") else "-",
            why if why else ("нужно: " + ",".join(need) if need else "актуален")))


def candidates(fleet, drows, only_outdated=True):
    out = []
    for r in fleet:
        d = drows.get(r.get("hostname")) or {}
        if eligible(r, d):
            continue
        if only_outdated and not (d.get("passwallNeedsUpdate") or d.get("xrayNeedsUpdate")):
            continue
        out.append(r)
    return out


def cmd_idle(args):
    fleet, drows = fleet_list(), {r.get("displayName"): r for r in drift_rows()}
    cands = candidates(fleet, drows) if args.all else resolve_targets(args.selectors, fleet)
    print("замеряю простой на %d роутерах…\n" % len(cands))

    def one(r):
        try:
            kv = parse_kv(probe(r, IDLE_CMD, "MARKER_IDLE", timeout=60, budget_s=200))
        except Exception as e:
            return {"host": r["hostname"], "err": str(e)[:60]}
        lan = int(kv.get("lan_rx", 0) or 0) + int(kv.get("lan_tx", 0) or 0)
        return {"host": r["hostname"], "lan": lan,
                "conntrack": int(kv.get("conntrack", 0) or 0), "wifi": kv.get("wifi"),
                "mem": int(kv.get("mem_mb", 0) or 0),
                "overlay": int(kv.get("overlay_mb", 0) or 0)}

    with ThreadPoolExecutor(max_workers=args.parallel) as ex:
        res = list(ex.map(one, cands))
    ok = [x for x in res if "err" not in x]
    ok.sort(key=lambda x: (x["lan"], x["conntrack"], -x["overlay"]))
    print("%-24s %8s %7s %5s %7s %7s %s" % (
        "router", "lan/5s", "conntr", "wifi", "mem MB", "ovl MB", "вердикт"))
    for x in ok:
        idle = x["lan"] <= IDLE_LAN_BYTES_5S and x["conntrack"] <= IDLE_CONNTRACK
        print("%-24s %8d %7d %5s %7d %7d %s" % (
            x["host"][:24], x["lan"], x["conntrack"], x["wifi"], x["mem"], x["overlay"],
            "ПРОСТАИВАЕТ" if idle else "занят"))
    for x in res:
        if "err" in x:
            print("%-24s нет результата: %s" % (x["host"][:24], x["err"]))


def do_preflight(router, lane, log):
    kv = parse_kv(probe(router, PREFLIGHT_CMD, "MARKER_PREFLIGHT", timeout=90))
    kv["_manual_lane"] = (lane["manualLaneRequired"]
                          and "wrapper" in (kv.get("xray_file") or "")
                          and lane["runtimeTarget"] not in (kv.get("xray_ver") or ""))
    # unzip ставим вместе с зависимостями app: ручной лейн xray без него невозможен,
    # а по парку он не гарантирован (на yuranrod-msk отсутствовал совсем)
    kv["_deps_to_install"] = []
    if any(kv.get("dep_" + p) != "yes" for p in ("lyaml", "libyaml", "coreutils-timeout")):
        kv["_deps_to_install"] += ["lyaml", "coreutils-timeout"]
    if kv["_manual_lane"] and kv.get("has_unzip") != "yes":
        kv["_deps_to_install"].append("unzip")
    kv["_needs_deps"] = bool(kv["_deps_to_install"])
    blockers = []
    if kv.get("feed") != "ok":
        blockers.append("фид OpenWrt недостижим — Фаза 3 (искать мёртвый слот), "
                        "иначе app даст тихий no-op")
    if kv["_manual_lane"] and kv.get("github") != "ok":
        blockers.append("github недостижим — ручной лейн xray невозможен")
    if int(kv.get("overlay_mb", 0) or 0) < MIN_OVERLAY_MB:
        blockers.append("overlay %s MB < %d" % (kv.get("overlay_mb"), MIN_OVERLAY_MB))
    if kv["_manual_lane"] and int(kv.get("mem_mb", 0) or 0) < MIN_MEM_MB_MANUAL:
        blockers.append("MemAvailable %s MB < %d — сначала ребут"
                        % (kv.get("mem_mb"), MIN_MEM_MB_MANUAL))
    # Своп xray отъедает ~10 MB RAM (бинарь крупнее), поэтому роутер, прошедший
    # ручной лейн, может провалиться под storage-пол уже НА ШАГЕ app. Считаем с запасом.
    need_app = kv.get("app") != (lane.get("appTarget") or "")
    if need_app:
        projected = int(kv.get("mem_mb", 0) or 0) - (10 if kv["_manual_lane"] else 0)
        if projected < MIN_MEM_MB_APP:
            blockers.append(
                RAM_BLOCKER + " %s MB%s < %d — job_safety отобьёт апдейт app; нужен ребут"
                % (kv.get("mem_mb"),
                   " (после свопа станет ~%d)" % projected if kv["_manual_lane"] else "",
                   MIN_MEM_MB_APP))
        if int(kv.get("tmp_mb", 0) or 0) and int(kv.get("tmp_mb")) < MIN_TMP_MB_APP:
            blockers.append("/tmp %s MB < %d" % (kv.get("tmp_mb"), MIN_TMP_MB_APP))
        # Гео-зависимости: различаем «нет регистрации» и «нет файла».
        #   geoview      — БИНАРЬ, доставляется из бандла (6.8 MB), это чинит и
        #                  латентную дыру при enable_geoview_ip=1;
        #   v2ray-geo*   — ДАННЫЕ, на парке кастомные (0.4 MB против стоковых 17.9 MB).
        #                  Стоковые ставить нельзя: затрут гео и не влезут в overlay.
        #                  Если файлы на месте — Depends удовлетворены фактически, и app
        #                  ставится ручной распаковкой ipk (панельный лейн тут бессилен).
        kv["_geoview_install"] = (kv.get("geo_geoview") != "yes"
                                  or kv.get("bin_geoview") != "yes")
        data_unresolved = [p for p in ("v2ray-geoip", "v2ray-geosite")
                           if kv.get("geo_" + p) != "yes"]
        files_ok = (str(kv.get("file_geoip", "no")).isdigit()
                    and str(kv.get("file_geosite", "no")).isdigit())
        kv["_manual_app"] = bool(data_unresolved) or kv["_geoview_install"]
        if data_unresolved and not files_ok:
            blockers.append(
                "не зарегистрированы %s И файлов гео нет в %s — Depends не удовлетворить "
                "ничем, кроме стокового v2ray-geoip (17.9 MB, затрёт кастомное гео). "
                "Ручное решение оператора"
                % (", ".join(data_unresolved), kv.get("asset_dir")))
        if kv["_geoview_install"] and int(kv.get("overlay_mb", 0) or 0) < 10:
            blockers.append("нужен geoview (+6.8 MB), а overlay %s MB — не влезет"
                            % kv.get("overlay_mb"))
    if kv.get("default_node") != "_direct":
        blockers.append("default_node=%s, ожидается _direct" % kv.get("default_node"))
    log("xray=%s app=%s mem=%sMB overlay=%sMB feed=%s github=%s лейн=%s" % (
        (kv.get("xray_ver") or "?").split(" ")[1] if kv.get("xray_ver") else "?",
        kv.get("app"), kv.get("mem_mb"), kv.get("overlay_mb"),
        kv.get("feed"), kv.get("github"),
        "РУЧНОЙ" if kv["_manual_lane"] else "панельный/не нужен"))
    return kv, blockers


def cmd_preflight(args):
    fleet, lane = fleet_list(), lane_decision()
    drows = {r.get("displayName"): r for r in drift_rows()}
    targets = (candidates(fleet, drows) if args.all
               else resolve_targets(args.selectors, fleet))
    print("bundle %s | планка xray %s | app %s | роутеров: %d\n"
          % (lane["bundleVersion"], lane["runtimeTarget"], lane["appTarget"], len(targets)))

    def one(r):
        log = Log(r["hostname"], False)
        try:
            kv, blockers = do_preflight(r, lane, log)
            return r["hostname"], kv, blockers, None
        except Exception as e:
            return r["hostname"], {}, [], str(e)[:70]

    with ThreadPoolExecutor(max_workers=args.parallel) as ex:
        res = list(ex.map(one, targets))

    auto, manual, blocked, failed = [], [], [], []
    for host, kv, blockers, err in res:
        if err:
            failed.append((host, err)); continue
        (blocked if blockers else (manual if kv.get("_manual_app") else auto)).append(
            (host, kv, blockers))

    def row(host, kv):
        return ("%-24s xray=%-8s app=%-11s mem=%-4s ovl=%-4s unzip=%-3s geoview=%-3s "
                "geo_pkg=%s%s%s" % (
                    host[:24],
                    (kv.get("xray_ver") or "?").split(" ")[1] if kv.get("xray_ver") else "?",
                    kv.get("app"), kv.get("mem_mb"), kv.get("overlay_mb"),
                    kv.get("has_unzip"), kv.get("bin_geoview"),
                    "y" if kv.get("geo_geoview") == "yes" else "n",
                    "y" if kv.get("geo_v2ray-geoip") == "yes" else "n",
                    "y" if kv.get("geo_v2ray-geosite") == "yes" else "n"))

    print("=== ПОЙДУТ АВТОМАТОМ (%d) ===" % len(auto))
    for host, kv, _ in auto:
        print("  " + row(host, kv))
    print("\n=== ЧЕРЕЗ РУЧНУЮ РАСПАКОВКУ, драйвер умеет сам (%d) ===" % len(manual))
    for host, kv, _ in manual:
        need = []
        if kv.get("_geoview_install"):
            need.append("+geoview")
        if kv.get("geo_v2ray-geoip") != "yes" or kv.get("geo_v2ray-geosite") != "yes":
            need.append("гео-пакеты не в opkg")
        print("  " + row(host, kv) + "   [" + ", ".join(need) + "]")
    print("\n=== ЗАБЛОКИРОВАНЫ (%d) ===" % len(blocked))
    for host, kv, blockers in blocked:
        print("  " + row(host, kv))
        for b in blockers:
            print("      ! " + b)
    if failed:
        print("\n=== НЕТ ОТВЕТА (%d) ===" % len(failed))
        for host, err in failed:
            print("  %-24s %s" % (host[:24], err))
    print("\nИТОГ: авто %d | ручная распаковка %d | блок %d | без ответа %d"
          % (len(auto), len(manual), len(blocked), len(failed)))


def rollout_one(router, lane, args):
    host = router["hostname"]
    log = Log(host, args.parallel == 1)
    t0 = time.time()
    verdict = "ПРОПУЩЕН"
    try:
        kv, blockers = do_preflight(router, lane, log)
        # Нехватка RAM — не приговор: она лечится ребутом (на парке это САМЫЙ частый
        # блокер, 15 из 22 при свипе 2026-08-06). Всё остальное требует человека.
        if blockers and args.apply and args.reboot_if_needed \
                and all(b.startswith(RAM_BLOCKER) for b in blockers):
            log("RAM ниже порога — ребут и ожидание…")
            trpc("update.queueBulkRouterReboot", {"routerIds": [router["id"]]},
                 mutation=True)
            deadline, back = time.time() + 420, False
            time.sleep(45)
            while time.time() < deadline:
                snap = ((trpc("fleet.byId", {"routerId": router["id"]})
                         .get("latestSnapshot") or {}).get("payload") or {})
                mem = (snap.get("resources") or {}).get("memoryAvailableMb") or 0
                if mem >= MIN_MEM_MB_APP + (10 if kv["_manual_lane"] else 0):
                    log("  вернулся, RAM %s MB" % mem)
                    back = True
                    break
                time.sleep(POLL_JOB * 3)
            if not back:
                log("ОСТАНОВ: после ребута RAM так и не поднялась")
                return host, "МАЛО RAM", time.time() - t0, log
            kv, blockers = do_preflight(router, lane, log)
        if blockers:
            log("БЛОКЕР: %s" % "; ".join(blockers))
            return host, "БЛОКЕР", time.time() - t0, log
        app_target = args.app_target or lane["appTarget"]
        if not args.apply:
            plan = []
            if kv["_manual_lane"]:
                plan.append("xray -> %s" % lane["runtimeTarget"])
            if kv["_needs_deps"]:
                plan.append("зависимости")
            if kv.get("app") != app_target:
                plan.append("app %s -> %s" % (kv.get("app"), app_target))
            log("ПЛАН (dry-run): %s" % ("; ".join(plan) or "нечего делать"))
            return host, "DRY-RUN", time.time() - t0, log

        # --- stage1: xray + зависимости одним отвязанным проходом
        if kv["_manual_lane"] or kv["_needs_deps"]:
            log("stage1: %s%s…" % ("xray " if kv["_manual_lane"] else "",
                                   "deps" if kv["_needs_deps"] else ""))
            launch_detached(router,
                            stage1_script(kv["_manual_lane"], kv["_needs_deps"],
                                          lane["runtimeTarget"], lane["assetUrl"],
                                          deps=kv["_deps_to_install"]),
                            "/tmp/vstage.sh", "LAUNCHED_STAGE1")
            skv = parse_kv(wait_for_log(router, "/tmp/vstage.log", "DONE"))
            if kv["_needs_deps"]:
                missing = [p for p in ("lyaml", "libyaml", "coreutils-timeout")
                           if skv.get("dep_" + p) != "yes"]
                if missing:
                    log("ОСТАНОВ: не встали %s — app дал бы тихий no-op" % missing)
                    return host, "ЗАВИСИМОСТИ", time.time() - t0, log
                log("  зависимости на месте (ставил: %s)"
                    % ", ".join(kv["_deps_to_install"]))
            if kv["_manual_lane"]:
                log("  xray: %s wrapper=%s cfg=%s (старт %sс)"
                    % (skv.get("XRAY"), skv.get("wrapper"), skv.get("cfg"),
                       skv.get("xray_up_after_s")))
                if skv.get("XRAY") != "SWAP_OK":
                    hint = {"NO_UNZIP": "на роутере нет unzip — он в списке зависимостей, "
                                        "но установка не прошла",
                            "ZIP_CORRUPT": "архив не проходит unzip -t",
                            "DOWNLOAD_FAIL": "не скачался архив с github"}.get(
                                skv.get("XRAY"), "")
                    log("ОСТАНОВ: своп не удался%s, app не ставлю"
                        % (" (%s)" % hint if hint else ""))
                    return host, "СВОП УПАЛ", time.time() - t0, log

        # --- app: ручная распаковка там, где opkg не резолвит гео-зависимости
        if kv.get("app") != app_target and kv.get("_manual_app"):
            log("app -> %s ручной распаковкой (гео-пакеты не зарегистрированы%s)…"
                % (app_target, ", доставляю geoview" if kv["_geoview_install"] else ""))
            urls = lane.get("packageUrls") or {}
            if not urls.get("luci-app-passwall2"):
                log("ОСТАНОВ: в бандле нет URL для luci-app-passwall2")
                return host, "НЕТ АРТЕФАКТА", time.time() - t0, log
            launch_detached(router,
                            manual_app_script(urls["luci-app-passwall2"],
                                              urls.get("geoview", ""),
                                              kv["_geoview_install"],
                                              kv.get("asset_dir") or "/usr/share/v2ray/",
                                              app_target),
                            "/tmp/vapp.sh", "LAUNCHED_APP")
            akv = parse_kv(wait_for_log(router, "/tmp/vapp.log", "DONE"))
            log("  app=%s initd=%s uci=%s гео=%s%s"
                % (akv.get("APP"), akv.get("initd"), akv.get("uci_lines"),
                   akv.get("geo_now"),
                   " ВОССТАНОВЛЕНО:" + akv.get("geo_restored") if akv.get("geo_restored") else ""))
            if akv.get("APP") != app_target or akv.get("initd") != "yes":
                log("ОСТАНОВ: ручная установка не дала целевую версию")
                return host, "APP УПАЛ", time.time() - t0, log
        # --- app панельным массовым лейном
        elif kv.get("app") != app_target:
            log("app -> %s…" % app_target)
            res = trpc("update.queueBulkPasswallPackageUpdate",
                       {"routerIds": [router["id"]], "artifactChannel": "stable",
                        "packages": ["luci-app-passwall2"]}, mutation=True)
            entry = (res.get("results") or [{}])[0]
            if entry.get("status") != "queued":
                log("джоб не поставлен: %s" % entry.get("reason"))
                return host, "APP НЕ ПОСТАВЛЕН", time.time() - t0, log
            jid, state, deadline = entry.get("jobId"), None, time.time() + 900
            while time.time() < deadline:
                byid = trpc("fleet.byId", {"routerId": router["id"]})
                job = next((j for j in (byid.get("recentJobs") or [])
                            if j.get("id") == jid), {})
                state = job.get("state")
                if state in ("succeeded", "failed"):
                    break
                time.sleep(POLL_JOB)
            log("  job %s" % state)
            if state != "succeeded":
                return host, "APP УПАЛ", time.time() - t0, log

        # --- stage3: восстановить бинарь, если апдейт app его снёс, затем приёмка
        launch_detached(router, stage3_script(lane["runtimeTarget"], lane["assetUrl"]),
                        "/tmp/vstage3.sh", "LAUNCHED_STAGE3")
        acc = wait_for_log(router, "/tmp/vstage3.log", "DONE")
        akv = parse_kv(acc)
        if akv.get("restore_needed") == "1":
            log("  ВНИМАНИЕ: апдейт app снёс xray, восстановлен из кэша "
                "(pkg xray-core=%s)" % akv.get("pkg_xray_core"))
        probes = [l.split() for l in acc.splitlines() if l.startswith("probe ")]
        bad = [p for p in probes if not p[1].isdigit() or p[1] == "000" or int(p[1]) >= 400]
        ok = (akv.get("app") == app_target
              and akv.get("exe") == "/usr/bin/xray"
              and akv.get("cfg") == "Configuration OK."
              and akv.get("wrapper") == "1390"
              and "GOMEMLIMIT" in acc and not bad)
        log("итог: app=%s xray=%s wrapper=%s cfg=%s"
            % (akv.get("app"), (akv.get("xray_ver") or "").split(" ")[1:2],
               akv.get("wrapper"), akv.get("cfg")))
        log("пробы: %s" % " ".join("%s=%s" % (p[2].split("//")[1][:16], p[1])
                                   for p in probes))
        log("overlay=%sMB mem=%sMB" % (akv.get("overlay_mb"), akv.get("mem_mb")))
        verdict = "ГОТОВО" if ok else "ПРОВЕРИТЬ ВРУЧНУЮ"
    except Exception as e:
        log("ОШИБКА: %s" % str(e)[:200])
        verdict = "ОШИБКА"
    return host, verdict, time.time() - t0, log


def cmd_rollout(args):
    fleet, drows = fleet_list(), {r.get("displayName"): r for r in drift_rows()}
    lane = lane_decision()
    targets = (candidates(fleet, drows) if args.selectors == ["--all"]
               else resolve_targets(args.selectors, fleet))
    print("bundle %s | планка xray %s | app %s | ручной лейн: %s | роутеров: %d | параллель: %d\n"
          % (lane["bundleVersion"], lane["runtimeTarget"], lane["appTarget"],
             "ДА" if lane["manualLaneRequired"] else "нет", len(targets), args.parallel))
    if not args.apply:
        print("DRY-RUN: изменений не будет, добавь --apply\n")
    t0 = time.time()
    with ThreadPoolExecutor(max_workers=args.parallel) as ex:
        results = list(ex.map(lambda r: rollout_one(r, lane, args), targets))
    for _, _, _, log in results:
        log.flush()
    print("\n%-24s %-18s %s" % ("router", "итог", "время"))
    for host, verdict, secs, _ in results:
        print("%-24s %-18s %.0fс" % (host[:24], verdict, secs))
    print("\nволна: %.1f мин на %d роутеров" % ((time.time() - t0) / 60, len(targets)))


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("triage", help="дрейф версий и пригодность по парку")
    p_i = sub.add_parser("idle", help="замер простоя (дельта br-lan за 5с)")
    p_i.add_argument("selectors", nargs="*")
    p_i.add_argument("--all", action="store_true", help="все пригодные отстающие")
    p_i.add_argument("--parallel", type=int, default=8)
    p_p = sub.add_parser("preflight", help="гейты Фазы 2, без изменений")
    p_p.add_argument("selectors", nargs="*")
    p_p.add_argument("--all", action="store_true", help="все пригодные отстающие")
    p_p.add_argument("--parallel", type=int, default=8)
    p_r = sub.add_parser("rollout", help="stage1 -> app -> приёмка")
    p_r.add_argument("selectors", nargs="+", help="хосты, или --all для всех отстающих")
    p_r.add_argument("--apply", action="store_true", help="выполнить изменения")
    p_r.add_argument("--parallel", type=int, default=1)
    p_r.add_argument("--app-target", default=None, help="по умолчанию — версия из бандла")
    p_r.add_argument("--reboot-if-needed", action="store_true",
                     help="перезагрузить роутер, если единственный блокер — RAM ниже "
                          "порога job_safety (самый частый случай на парке)")
    args = ap.parse_args()
    {"triage": cmd_triage, "idle": cmd_idle,
     "preflight": cmd_preflight, "rollout": cmd_rollout}[args.cmd](args)


if __name__ == "__main__":
    main()
