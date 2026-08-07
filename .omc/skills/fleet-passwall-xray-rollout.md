---
name: fleet-passwall-xray-rollout
description: Массовое обновление PassWall2 и Xray на парке Vectra-ProRouter через операторскую панель — с гейтом дата-плана, выбором безопасного лейна для xray и волновой приёмкой
triggers:
  - обновить passwall на парке
  - обновить xray на парке
  - раскатать passwall/xray массово
  - update passwall fleet
  - queueBulkPasswallPackageUpdate
  - queueBulkXrayUpdate
  - роутеры отстали по версиям
---

# Массовая раскатка PassWall2 + Xray на парк

> **Драйвер:** `scripts/Invoke-FleetPasswallXrayRollout.py` реализует всё, что ниже.
> Рантбук читать для понимания решений и ловушек; руками фазы собирать не нужно.
>
> ```bash
> python3 scripts/Invoke-FleetPasswallXrayRollout.py triage             # кто отстал и пригоден
> python3 scripts/Invoke-FleetPasswallXrayRollout.py idle --all         # кто простаивает
> python3 scripts/Invoke-FleetPasswallXrayRollout.py preflight --all    # сводка готовности
> python3 scripts/Invoke-FleetPasswallXrayRollout.py rollout <host>     # план (dry-run)
> python3 scripts/Invoke-FleetPasswallXrayRollout.py rollout <host> --apply
> python3 scripts/Invoke-FleetPasswallXrayRollout.py rollout --all --apply --parallel 4 \
>     --reboot-if-needed
> ```
>
> Без `--apply` изменений не будет. Гейты Фазы 2 роутер не пропускают молча — он
> печатается как БЛОКЕР и пропускается. `preflight --all` делит парк на три корзины:
> пойдут автоматом / нужна ручная распаковка (драйвер умеет сам) / заблокированы.
> `--reboot-if-needed` перезагружает роутер, когда единственный блокер — RAM ниже
> порога `job_safety`, и продолжает после возврата.

## Что показал свип по парку (2026-08-06, 22 роутера)

> ⚠️ **Вывод «узкое место парка — RAM, 15 из 22» был ОШИБОЧНЫМ и отозван.**
> Он опирался на пред-гейт, который я сам же и ввёл, прочитав порог
> `jobSafetyStorageMemoryFloorMB = 64` в исходниках агента. Реального отказа джоба
> именно из-за RAM не наблюдалось ни разу: у `yuranrod-msk` джоб падал при 79 MB и
> причина была в гео-зависимостях, а `AndreyVK`, которого пред-гейт дважды отправил в
> ребут с вердиктом «МАЛО RAM», обновился **с первой попытки при 67 MB**, стоило
> перестать блокировать заранее.
>
> **Правило: RAM — предупреждение, а не блокер.** Решает агент, и перед решением он
> делает собственный сброс кэша. Драйвер лечит ПО ФАКТУ отказа лестницей
> «попытка → сброс кэша → ребут → отказ», а не по прогнозу. Мерить кэш бесполезно:
> `drop_caches` дал 83 MB, а следующий замер через несколько секунд — снова 62.

Всё остальное оказалось разовым: `unzip`, `geoview` и регистрация гео-пакетов были
сломаны **только на yuranrod-msk**, у остальных 21 — на месте (`geo_pkg=yyy`).
Два роутера (`aleksandr-grigorievsky`, `denisvitalievichmain`) не ответили за окно —
повторить отдельно.

**Полный автоматический проход проверен на живом роутере** (`avfilicity`, 2026-08-06,
7 минут, вердикт ГОТОВО): сработали обе страховки подряд — `--reboot-if-needed` поднял
RAM 65 → 79 MB и продолжил сам, затем апдейт app снёс xray, и Фаза 6.5 восстановила его
из кэша. **Простоя не было.** Это и есть эталонный прогон для волны.

Отработано на 1111111111 (2026-08-04) и VagrandRouter (2026-08-06). Оба раза узкое место
было НЕ в самой установке, а в преф-лайте: без него апдейт молча не происходит или
затирает wrapper. Порядок фаз — не рекомендация, а условие безопасности.

## Волна на семи роутерах, параллель 3 (2026-08-07) — и почему это дорого обошлось

Итог: 4 готово, 1 корректно заблокирован, **3 упали на ошибках панельного CLI**, и один
из них — `zhenya13911` — **остался без VPN примерно на 15 минут**.

**Панельный CLI под параллелью периодически отдаёт не-JSON.** Причина — истёкшая сессия
и гонка нескольких процессов CLI за кэш операторской куки. Симптом: `terminal.queueCommand`
или `fleet.byId` возвращают простыню Node-стектрейса. Лечится тремя вещами, все внесены
в драйвер: прогрев сессии ОДИН раз до пула, повтор вызова до трёх раз с `--force-login`
на последней попытке, и вытаскивание из стектрейса первой осмысленной строки (раньше в
лог уходил кусок пути внутри `node_modules`, по которому нельзя понять ничего).

**Главное: драйвер обязан пережить собственный сбой.** `zhenya13911` лёг не потому, что
что-то сломалось на роутере, а потому, что исключение случилось МЕЖДУ постановкой апдейта
app и Фазой 6.5 — апдейт снёс xray, а восстановить его было уже некому. Теперь в
обработчике исключений есть аварийная сеть: если апдейт app успел стартовать, драйвер
пытается восстановить бинарь даже при собственной аварии, и вердикт становится
`ВОССТАНОВЛЕН` либо `ТРЕБУЕТ РУК`.

⇒ **Параллель выше двух без этих правок опасна.** Не потому, что ломается роутер, а
потому, что растёт шанс уронить драйвер ровно в том окне, где роутер беззащитен.

**Новый класс блокера:** `vladimirdrfilicity` не достаёт github (`feed=ok`, но
`github=fail`) — ручной лейн xray там невозможен в принципе. Заблокирован корректно,
ничего не тронуто. Лечится либо своим зеркалом бинаря, либо маршрутом до github.

**Лестница повторов доказана живьём:** у `DmitryGubenko` app-джоб упал на первой попытке,
после сброса кэша прошёл со второй — при RAM 35 MB, то есть отказ был транзиентом, а не
нехваткой памяти. Ещё один довод против пред-гейта по RAM.

## Уроки первой параллельной волны (2026-08-06, 2 роутера)

Волна ничего не сломала, но вскрыла три дефекта — все уже закрыты в драйвере, здесь они
как предупреждение тому, кто будет менять код или делать то же руками.

**1. Отвязанная стадия не должна удалять свой лог.** Драйвер читает лог ПОСЛЕ выхода
скрипта. Самоудаление означало, что маркера `DONE` не увидит никто: прогон висел до
таймаута и падал в «ОШИБКА» при фактически успешной работе. Логи стадий чистит тот, кто
их прочитал.

**2. Ручной своп xray нужен независимо от наличия wrapper.** Пока `runtimeTarget != ipk`,
планка достижима ТОЛЬКО ручной заменой; wrapper решает лишь то, опасен ли панельный лейн.
Ранняя логика завязывала своп на wrapper и на роутере с `uci xray_file=/usr/bin/xray`
(`AlexanderBabkin`) молча обновила один app, оставив xray позади — то есть собрала ровно
ту комбинацию «новый app + старый xray», которая ломала парк в июне. Здесь обошлось
(`Configuration OK.`, все пробы зелёные: 26.6.22 достаточно свежий), но полагаться на это
нельзя.

**3. Порог RAM надо проверять ПО ФАКТУ, а не прогнозом.** Ожидание «64 + 10 про запас»
после ребута не выполнялось никогда: `AndreyVK` стабильно садится на ~63 MB. Правильно —
ждать ровно порог, а запас на своп добирать вторым ребутом уже перед фазой app, измерив
RAM после свопа.

Плюс: при параллели вывод раньше буферизовался до конца прогона, и наблюдателю
19 минут не было видно ничего. Теперь строки идут живьём с префиксом хоста.

## Когда НЕ применять
- Один роутер и просто «догнать версию» → это тот же рантбук, но без волн; читай Фазы 1-7.
- Роутер `hh` — **исключать из любых массовых операций всегда**.
- `supportState = blocked`, `importState != approved`, `blocked = true` в дрейф-вью — пропускать.
- Cudy/Filogic, не достающие `downloads.openwrt.org` по другой причине — отдельный кейс,
  сюда не мешать (у них нет маршрута к фиду вообще, а не из-за дохлого слота).

## Инварианты
1. **xray ПЕРВЫМ, app ВТОРЫМ.** Обратный порядок ломал парк в июне (`unknown action: return`).
   «Новый xray + старый app» безопасно и проверено; наоборот — нет.
2. **Один `run_terminal_command` на роутер одновременно.** `dedupeKey` =
   `run_terminal_command:<routerId>`. Пачка запросов вытесняет друг друга, а
   `terminal history` отдаёт `latestResult` с ЧУЖИМ jobId. Между роутерами параллелить можно.
3. **Потолок terminal-джоба — 120 секунд** (CLI валидирует `5..120` ещё до постановки).
   Всё, что дольше — только отвязанно (Фаза 4).
4. Каждая команда несёт уникальный `MARKER_*`, и результат сверяется И по `jobId`, И по
   наличию маркера в stdout.

---

## Фаза 0 — Триаж парка (read-only)

```bash
printf '%s' '{}' | bash ./scripts/VectraPanelCli.sh call update.versionDriftWorkspace
```

Из `rows` брать: `id`, `displayName`, `controllerInstalled`, `passwallInstalled`,
`passwallNeedsUpdate`, `xrayInstalled`, `xrayNeedsUpdate`, `supportState`, `blocked`.

Отсеять исключения (см. выше) и разложить кандидатов на волны по 4-6 роутеров.
`summary` даёт масштаб: `outdatedPasswallCount` / `outdatedXrayCount` / `blockedCount`.

**Первую волну ставить на простаивающие роутеры.** «Простаивает» меряется, а не
угадывается — `lastSeenAt` для этого бесполезен, все чекинятся одинаково. Read-only проба
(её можно гнать параллельно по всем кандидатам, дедуп у джобов per-router):

```sh
echo "leases=$(wc -l < /tmp/dhcp.leases 2>/dev/null || echo 0)"
echo "conntrack=$(cat /proc/sys/net/netfilter/nf_conntrack_count)"
W=0; for i in $(iw dev 2>/dev/null | grep Interface | cut -d" " -f2); do
  W=$((W+$(iw dev $i station dump 2>/dev/null | grep -c Station))); done; echo "wifi=$W"
R1=$(cat /sys/class/net/br-lan/statistics/rx_bytes); T1=$(cat /sys/class/net/br-lan/statistics/tx_bytes)
sleep 5
R2=$(cat /sys/class/net/br-lan/statistics/rx_bytes); T2=$(cat /sys/class/net/br-lan/statistics/tx_bytes)
echo "lan_rx_5s=$((R2-R1)) lan_tx_5s=$((T2-T1))"
```

Решающий признак — **дельта трафика на `br-lan` за 5 секунд**, а не число leases и не
ассоциированные клиенты: у sairoutermsk висели 2 wifi-клиента при нулевом трафике и
conntrack 17, то есть устройства подключены, но спят. Занятый роутер для сравнения давал
184 KB за те же 5 секунд. Порог для «можно трогать»: `lan_tx_5s` близко к нулю и
`conntrack < ~50`.

Из простаивающих выбирать того, у кого больше свободного overlay — ручной лейн xray
пишет ~35MB.

## Фаза 1 — Выбрать лейн для xray (ГЛАВНОЕ РЕШЕНИЕ)

```bash
printf '%s' '{}' | bash ./scripts/VectraPanelCli.sh call update.artifacts
```

Сравнить два числа в новейшем `passwall_bundle`:
- `A` = `metadata.runtimeTargets['xray-core'].remoteVersion` (планка, из upstream XTLS)
- `B` = версия опубликованного артефакта `passwall_package` / `xray-core` (то, что в ipk)

**Если `A == B`** → пакетный путь берёт планку, встроенный апдейтер не запускается.
Панельный лейн безопасен, можно `queueBulkXrayUpdate`.

**Если `A != B`** (сегодня так: 26.7.28 против 26.7.11) → пакетный путь планку НЕ берёт,
агент проваливается в built-in updater PassWall. На роутере с `vectra-xray-wrapper` тот
пишет скачанный бинарь в `uci xray_file`, то есть **затирает wrapper** (теряются
`GOMEMLIMIT`/`GOGC`) и кладёт вторую 30MB-копию на overlay. Гард против этого есть в
исходниках агента (`passwall_update.go`, PR #31 от 2026-08-05), но **сборка контроллера
должна быть НОВЕЕ этого коммита** — проверять по дате публикации артефакта `controller`
в `update.artifacts`, а не по номеру ревизии.

⇒ Пока `A != B` и на роутере есть wrapper без гарда: **xray только ручным лейном (Фаза 4)**,
`queueBulkXrayUpdate` НЕ трогать — он ударит по всем сразу.

Наличие wrapper'а на роутере: `uci -q get passwall2.@global_app[0].xray_file` ≠ `/usr/bin/xray`.

## Фаза 2 — Преф-лайт на роутере (один read-only джоб)

```bash
bash ./scripts/VectraPanelCli.sh terminal run <selector> -- --timeout 90 --command \
'echo MARKER_PREFLIGHT; free; df -k /tmp /overlay | tail -2;
 ls -l /usr/bin/xray /usr/sbin/vectra-xray-wrapper /overlay/upper/usr/bin/xray 2>&1;
 echo "xray_file=$(uci -q get passwall2.@global_app[0].xray_file)";
 for p in lyaml libyaml coreutils-timeout; do if [ -f /usr/lib/opkg/info/$p.control ]; then echo "$p INSTALLED"; else echo "$p MISSING"; fi; done;
 wget -q -O /dev/null --timeout=12 https://downloads.openwrt.org/ && echo FEED_OK || echo FEED_FAIL;
 wget -q -O /dev/null --timeout=12 https://github.com/ && echo GH_OK || echo GH_FAIL'
```

Гейты, каждый из которых блокирует раскатку на этом роутере:

| Проверка | Порог | Если не проходит |
|---|---|---|
| `FEED_OK` | обязательно | → **Фаза 3**, иначе app молча не встанет |
| `GH_OK` | нужен для ручного лейна xray | → Фаза 3 |
| overlay free | ≥ ~10MB для app; для xray нужна дельта нового бинаря | подождать/чистить |
| MemAvailable | ≥ ~40MB перед ручным лейном | сначала ребут (`update.queueBulkRouterReboot`) |
| `/overlay/upper/usr/bin/xray` существует | да | если нет — бинарь в squashfs, нужен полный copy-up, считать место заново |
| `command -v unzip` | обязателен для ручного лейна | ставить `opkg install unzip`; на yuranrod-msk его НЕ БЫЛО, и попытка распаковки давала `sh: unzip: not found`, что легко принять за битый архив |
| `geoview`, `v2ray-geoip`, `v2ray-geosite` в `/usr/lib/opkg/info/` | все три | иначе Depends нового app не резолвятся и джоб падает БЕЗ причины (панель показывает просто `failed`) — см. ниже |

**Пороги job_safety.go** (агент отбивает джоб молча, панель показывает `failed`):
апдейт пакетов — storage-класс, `RAM ≥ 64 MB` (НЕ ослабляется манифестом),
`/tmp ≥ 32 MB`, `overlay ≥ 16 MB` — и вот overlay-пол для scoped-пакета опускается
до `manifestOverlayFloorMB` ≈ 4 MB. Ручной своп xray съедает ~10 MB RAM, поэтому
роутер, прошедший Фазу 4 впритык, проваливается под RAM-пол уже на Фазе 6.
Лечится ребутом (`update.queueBulkRouterReboot`): на yuranrod-msk 43 → 83 MB.

**Нерезолвимые Depends — отдельный тупик.** Проверять заранее:
```sh
opkg install --noaction luci-app-passwall2 2>&1 | tail -6
```
На yuranrod-msk это выдало `cannot find dependency geoview / v2ray-geoip / v2ray-geosite`
— пакеты стоят физически, но НЕ зарегистрированы в opkg. Доставлять их из бандла
вслепую нельзя: стоковый `v2ray-geoip` (17.9 MB) затирает кастомное гео Vectra
(пропадёт `RUSSIA-OUTSIDE`, см. [[reference_geosite_noproxy_fresh_unit]]), а вместе с
`v2ray-geosite` и `geoview` это ~35 MB overlay, которых на таких роутерах нет.
⇒ Такой роутер из волны исключать. Доделывать вручную по рецепту ниже.

### Рецепт для роутера с нерезолвимыми гео-Depends (проверен на yuranrod-msk)

Сначала различить, чего именно не хватает: пакет не зарегистрирован, но **файлы есть**
(`v2ray-geoip`/`v2ray-geosite` — данные в `/usr/share/v2ray/`) — или его нет физически
(`geoview` отсутствовал вовсе при `enable_geoview_ip=1`, то есть гео-ipset уже не строились).

1. Недостающие **бинари** ставить из бандла, это честно закрывает дыру:
   `wget $B/geoview_0.2.6-r1_aarch64_cortex-a53.ipk && opkg install /tmp/geoview.ipk` (6.8 MB).
2. `v2ray-geoip`/`v2ray-geosite` НЕ ставить: стоковые затрут кастомное гео и не влезут.
3. Бэкапнуть гео перед всем (`cp /usr/share/v2ray/geo*.dat /tmp/geobak/`), после — сверить
   `cmp` и вернуть при расхождении.
4. Поставить app **ручной распаковкой ipk**, а не opkg (см. ловушку ниже):
```sh
cd /tmp/ipkx && gzip -dc /tmp/app.ipk | tar -x        # → control.tar.gz data.tar.gz
tar -xzf data.tar.gz -C /                              # файлы
tar -xzf control.tar.gz -C /tmp/ctl
cp /tmp/ctl/control /usr/lib/opkg/info/luci-app-passwall2.control
tar -tzf data.tar.gz | sed 's|^\.||' | grep -v '/$' > /usr/lib/opkg/info/luci-app-passwall2.list
```
5. Проверить, что xray пережил (Фаза 6.5), затем `/etc/init.d/passwall2 restart` и приёмка.

> ☠️ **`opkg --force-depends install <local.ipk>` НЕ обходит проверку зависимостей.**
> Этот opkg игнорирует переданный файл, уходит искать пакет ПО ИМЕНИ в фидах и падает с
> `Package ... is not available from any configured src`. Флаг и до, и после подкоманды —
> одинаково бесполезен.
>
> **`--force-reinstall` при этом РАЗРУШИТЕЛЕН:** opkg сначала делает
> `Removing package luci-app-passwall2`, потом пытается скачать по имени, не может — и
> пакет остаётся УДАЛЁННЫМ. На yuranrod-msk так и вышло. Спасло только то, что файлы
> (`/etc/init.d/passwall2`, `/usr/share/passwall2/*`, `/etc/config/passwall2` — 4208 строк
> UCI) при этом уцелели, и дата-план не упал. Никогда не применять `--force-reinstall`
> к пакету с нерезолвимыми зависимостями — сразу идти в ручную распаковку.

**Место считать по `df`, не по `du`:** UBIFS жмёт ~2:1 (наблюдалось `du` 59.5MB против
`df used` 28.9MB), поэтому `du` пугает вдвое сильнее, чем есть.

## Фаза 3 — Если фид недостижим: искать мёртвый слот, а не чинить фид

Симптом-ловушка: `downloads.openwrt.org` резолвится в fake-IP `198.18.x` и не отвечает.
Это значит, что домен попал в проксируемый слот, а **нода этого слота мертва**. Чинить надо
ноду, а не фид. Тот же отказ обычно виден как «Telegram/Instagram лежат, YouTube работает».

```bash
# 1) какие слоты на какие ноды смотрят
uci show passwall2.myshunt | grep -E "WorldProxy=|YouTube=|Special=|Tiktok=|DiscordVoiceUdp=|default_node="
# 2) TCP-жив ли эндпоинт (дёшево, без RAM)
tcping -q -c 1 -i 1 -t 3 -p <port> <host>
# 3) реальная проба через ноду (дороже — поднимает временный xray)
/usr/share/passwall2/test.sh url_test_node <nodeId>
```

Мёртвая нода = **TCP_FAIL**, либо **TCP_OK при стабильном `000` в серии из 5 замеров**.
Одиночный `000` — не приговор (см. Ловушку 2).

Починка — панельным драфт-лейном, НЕ через uci руками:

```bash
bash ./scripts/VectraPanelCli.sh draft editor <selector> > /tmp/ed.json
# правится draftConfig: shuntRules[<slot>].outboundNodeId в ОБОИХ контейнерах
#   (basicSettings.shuntRules И ruleManage.shuntRules) + зеркально в nodes[myshunt].extras
bash ./scripts/VectraPanelCli.sh draft save <selector> -- --config-file /tmp/new.json --note '<why>'
bash ./scripts/VectraPanelCli.sh draft queue-apply <selector> -- --revision-id <id>
```

Обязательные проверки перед `save` (лучше ассертами в скрипте, а не глазами):
- `basicSettings.main.mainSwitch === true`, `selectedNodeId === 'myshunt'`
- `nodes[myshunt].extras.default_node === '_direct'` — **никогда не прокси**
- правило `direct` → `_direct`; остальные слоты не изменились
- печатать литеральный диф нового конфига против живого и глазами убедиться, что изменений
  ровно столько, сколько задумано

**Перенос mux/xudp.** Если переезжает слот `WorldProxy`, скопировать на новую ноду
`mux=1`, `mux_concurrency=-1`, `xudp_concurrency=16` — правило WorldProxy стоит выше
`DiscordVoiceUdp` и содержит подсети Discord, поэтому голос идёт через ноду WorldProxy и
без этих extras умирает.

## Фаза 4 — xray ручным лейном (только при `A != B` + wrapper)

Источник — официальный zip XTLS из `runtimeTargets['xray-core'].assetUrl`. Извлекать
**только** `xray`: в архиве лежат ещё `geoip.dat`/`geosite.dat`, они затрут кастомное гео Vectra.

Шаг 1 — скачать и проверить (обычный джоб, ~120с хватает):
```
wget -q -O /tmp/xray.zip --timeout=45 <assetUrl> && unzip -t /tmp/xray.zip && echo ZIP_OK
```

Шаг 2 — своп **только отвязанно** (запись ~35MB на UBIFS не влезает в 120с; при попытке
в лоб джоб возвращает `failed` с пустым stdout). Скрипт передавать через base64, иначе
кавычки не переживают вложенное цитирование:

```bash
python3 - <<'PY'
import json,base64,subprocess
script = open('/tmp/xrayswap.sh').read()          # см. тело ниже
cmd = ("echo %s | base64 -d > /tmp/x.sh; chmod 0755 /tmp/x.sh; rm -f /tmp/x.log; "
       "setsid /bin/sh /tmp/x.sh </dev/null >/dev/null 2>&1 & sleep 2; echo LAUNCHED"
       ) % base64.b64encode(script.encode()).decode()
payload = json.dumps({"routerId":"<uuid>","command":cmd,"timeoutSeconds":60})
subprocess.run(["bash","./scripts/VectraPanelCli.sh","call","terminal.queueCommand","--","--mutation"],
               input=payload, text=True)
PY
```

Тело `/tmp/xrayswap.sh` — обязательно с логом и безусловным подъёмом сервиса в конце:
```sh
#!/bin/sh
exec >/tmp/x.log 2>&1
/etc/init.d/passwall2 stop; sleep 5; killall -9 xray 2>/dev/null; sleep 2
i=0; while [ $i -lt 2 ]; do
  unzip -p /tmp/xray.zip xray > /usr/bin/xray     # пишем СРАЗУ в overlay, минуя 31MB в tmpfs (=RAM)
  chmod 0755 /usr/bin/xray
  V=$(/usr/bin/xray version 2>&1 | head -1); echo "$V"
  case "$V" in *<TARGET>*) echo SWAP_OK; break;; esac
  i=$((i+1))
done
/etc/init.d/passwall2 start; sleep 15
```

Откат: держать zip в `/tmp` до приёмки (19.7MB), плюс прежние версии доступны из Vectra
bootstrap-стора. **Не** держать 30MB-бэкап бинаря в `/tmp` — это RAM, так вешался 1111.
После приёмки — `rm -f /tmp/xray.zip /tmp/x.sh /tmp/d.sh /tmp/x.log /tmp/d.log`
(скрипт-лаунчер не удаляет сам себя, на sairoutermsk `/tmp/d.sh` остался хвостом).

**Бюджет времени:** на простаивающем роутере весь своп занял 55 секунд
(20:19:05 → 20:20:00, из них 15с — штатный `sleep` после старта). Прирост overlay при
этом всего **+280 KB**, хотя файл вырос на 5MB — UBIFS сжимает. Планировать волну по
~2 минуты на роутер с запасом, а не по размеру бинаря.

## Фаза 5 — Зависимости app

`luci-app-passwall2 26.7.16-r1` требует `lyaml` + `libyaml` + `coreutils-timeout` (~327KB),
которых на старых роутерах нет. Без них `opkg install` — **тихий no-op**: версия не
меняется, ничего не ломается, джоб зелёный.

```
opkg update && opkg install lyaml coreutils-timeout     # libyaml подтянется сам
```

**Не хардкодить URL релиза.** Парк живёт на РАЗНЫХ ветках OpenWrt: VagrandRouter тянул
из `24.10.5`, sairoutermsk — из `24.10.3`. `opkg update` берёт ветку из
`/etc/opkg/distfeeds.conf` самого роутера, поэтому работает везде; прямые ссылки на
`.ipk` конкретного релиза сломаются на половине парка.

Тоже отвязанно (тот же приём), потом `rm -rf /var/opkg-lists`. Проверять факт установки
через `[ -f /usr/lib/opkg/info/<pkg>.control ]`, а **не** циклами `opkg list-installed` —
19 вызовов подряд валят джоб.

## Фаза 6 — app, массово

> ⚠️ **Апдейт app может УДАЛИТЬ пакет `xray-core` вместе с бинарём.** В control-файле
> `luci-app-passwall2 26.7.16-r1` пакет `xray-core` убран из `Depends` (в 26.4.10-r1 он
> там был). На `artem-lutfulin` (2026-08-06) после успешного scoped-апдейта app:
> `/usr/lib/opkg/info/xray-core.*` исчез целиком, `/usr/bin/xray` удалён вместе с только
> что положенным вручную 26.7.28, overlay освободился на ~15MB, все проксируемые пробы
> ушли в `000` — **VPN лёг**, а джоб при этом `succeeded`.
>
> **Это не редкость, а почти половина случаев: 4 из 9.** После `artem-lutfulin` (где
> простой и случился) повторилось на `avfilicity`, `ar-filicity` и
> `aleksandr-kutuzovgrad` — и во всех трёх stage3 восстановил бинарь в том же
> автоматическом проходе, простоя НЕ БЫЛО.
>
> | xray-core ДО апдейта | снесён | уцелел |
> |---|---|---|
> | 26.3.27-r1 | | VagrandRouter |
> | 26.4.25-r1 | | sairoutermsk |
> | 26.5.9 | ar-filicity | AndreyVK |
> | 26.6.1 | artem-lutfulin, avfilicity | |
> | 26.6.22 | | AlexanderBabkin |
> | 26.6.27 | aleksandr-kutuzovgrad | |
>
> **Обе гипотезы опровергнуты, заново не проверять.** Не версия: `26.5.9` и `26.6.x`
> встречаются и среди снесённых, и среди уцелевших. Не `Auto-Installed`: у всех
> выживших `Status: install user installed`, самого поля в `/usr/lib/opkg/status` нет.
>
> Механизм не установлен, частота высокая ⇒ **Фаза 6.5 обязательна всегда**, а не
> «на всякий случай».
>
> ⇒ **Обязательная Фаза 6.5 (см. ниже) после КАЖДОГО апдейта app.** И не удалять
> скачанный zip до неё — восстановление из кэша занимает секунды и не требует сети.

```bash
printf '%s' '{"routerIds":["<uuid1>","<uuid2>"],"artifactChannel":"stable","packages":["luci-app-passwall2"]}' \
  | bash ./scripts/VectraPanelCli.sh call update.queueBulkPasswallPackageUpdate -- --mutation
```

Scoped-скоуп важен: гард агента manifest-aware и опускает планку overlay до ~4MB для
одного пакета, тогда как полный `managed-stack` требует 16MB и вдобавок восстанавливает
стандартный 19.5MB `v2ray-geoip`, затирая кастомное гео.

Ответ — массив `results` с `status: queued|failed` и `reason` на каждый роутер; разбирать
поштучно, `failed` не терять.

**Ожидаемое поведение на низком overlay:** роутер может замолчать до ~65 минут во время
`opkg install` (медленная запись UBIFS). Это НЕ зависание — не ребутить, ребут посреди
opkg даёт битую полуустановку. Ждать штатный on-router watchdog.

## Фаза 6.5 — Восстановить бинарь, если апдейт app его снёс

Идемпотентно, в общем случае это no-op за две секунды:

```sh
NEED=0
[ -f /usr/bin/xray ] || NEED=1
[ $NEED -eq 0 ] && case "$(/usr/bin/xray version 2>&1 | head -1)" in *<TARGET>*) ;; *) NEED=1;; esac
if [ $NEED -eq 1 ]; then
  [ -f /tmp/xray.zip ] || wget -q -O /tmp/xray.zip --timeout=60 "<assetUrl>"
  unzip -p /tmp/xray.zip xray > /usr/bin/xray && chmod 0755 /usr/bin/xray
  /etc/init.d/passwall2 restart
fi
```

Заодно проверить `grep ^Version /usr/lib/opkg/info/xray-core.control`: если `ABSENT`,
пакет действительно снесён. Бинарь при этом работает и живёт под wrapper'ом, но
opkg о нём больше не знает — это надо зафиксировать для роутера, иначе следующий
пакетный апдейт xray поведёт себя непредсказуемо.

## Фаза 7 — Приёмка (что реально доказывает успех)

Ни один из этих пунктов не заменяет остальные:

```sh
# бинарь, который РЕАЛЬНО исполняется, и жив ли wrapper в цепочке
P=$(pgrep -f "passwall2/bin/xray run" | head -1)
ls -l /proc/$P/exe                       # → /usr/bin/xray
wc -c < /usr/bin/xray                    # размер целевой сборки
tr '\0' '\n' < /proc/$P/environ | grep -E "GOMEMLIMIT|GOGC|XRAY_LOCATION_ASSET"
cat /proc/$P/oom_score_adj               # -500
wc -c < /usr/sbin/vectra-xray-wrapper    # 1390

# конфиг — ОБЯЗАТЕЛЬНО с asset-каталогом
XRAY_LOCATION_ASSET=/usr/share/v2ray /usr/bin/xray -test -c /tmp/etc/passwall2/acl/default/global.json

# версия app
grep -m1 ^Version /usr/lib/opkg/info/luci-app-passwall2.control

# дата-план: коды, а не exit-статусы
for u in https://api.telegram.org/ https://www.instagram.com/ https://www.youtube.com/generate_204; do
  curl -s -o /dev/null -w "%{http_code} $u\n" --connect-timeout 8 "$u"; done
```

Критерии успеха волны:
- `Configuration OK.`, wrapper 1390 B, `GOMEMLIMIT`/`GOGC` в environ живого процесса
- app = целевая версия в `.control`
- пробы дают HTTP-коды (200/204/302/303), а не таймауты
- панельные `telegramReachability` / `youtubeReachability` / `instagramReachability` → `reachable`
- `safetyEvents` не ушли в `critical`

---

## Ловушки, каждая из которых давала ЛОЖНЫЙ диагноз

1. **Джоб `failed` с пустым stdout и `exit=-1`** — это либо превышение 120с, либо нехватка
   RAM. Прежде чем повторять: снять `ls -l` и mtime целевого файла. Если mtime не менялся —
   запись не начиналась, повтор безопасен.
2. **Первые 2-5 минут после `passwall2 start` врут.** `url_test_node` отдаёт `000` на живой
   ноде, а fakedns-домены отваливаются, потому что в кэше dnsmasq лежит fake-IP от
   ПРЕДЫДУЩЕГО инстанса xray. Само рассасывается по TTL. Не хоронить ноду по одному замеру
   сразу после рестарта — брать серию из 5.
   **Проявляется тем сильнее, чем больше активных клиентов.** На VagrandRouter (живые клиенты)
   YouTube падал после КАЖДОГО рестарта; на пустом sairoutermsk (0 leases, 0 трафика)
   транзиент не воспроизвёлся ни разу — некому было держать протухший кэш. Это и есть
   подтверждение механизма, и заодно довод обкатывать волны на простаивающих роутерах.
3. **`xray -test` без `XRAY_LOCATION_ASSET`** читает стоковый `/usr/share/xray/geosite.dat`
   и ложно падает на `illegal domain rule: geosite:russia-outside`. Живой процесс читает
   `/usr/share/v2ray/`.
4. **Код возврата `wget` — плохой дискриминатор:** нонзеро на любом не-200. Наблюдалось
   `youtu.be` = 303 и `discord.com` = 200, оба выглядели как FAIL. Мерить
   `curl -w '%{http_code}'`.
5. **Панель отстаёт по `binaryVersions.xray`:** при MemAvailable ниже порога агент отдаёт
   кэш, и дрейф-вью ещё долго показывает старую версию, хотя `passwallInstalled` обновился
   сразу. Верить `/proc/<pid>/exe`, а не дрейф-вью.
6. **Ручная замена бинаря не обновляет БД opkg:** `opkg list-installed xray-core` продолжит
   показывать старую версию. Это принятое поведение лейна, не проблема.

## Открытые вопросы, которые скилл не решает
- Момент, когда `A == B` и можно перейти на `queueBulkXrayUpdate`, наступит только после
  публикации артефакта с бинарём под планку либо после опускания планки до версии ipk.
  Решение архитектурное, см. `project_xray_runtime_target_unreachable_via_package`.
- Роутеры, где ручной лейн упирается в overlay (полный copy-up из squashfs), в этом
  рантбуке не покрыты — считать место отдельно.
