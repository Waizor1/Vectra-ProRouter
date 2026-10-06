# vctl: проброс портов (+ режим устройства «мимо VPN»)

> **УПРОЩЕНО 06.10 по слову владельца («не усложняй, просто настройка портов, попроще даже для про»). Этот блок главнее всего ниже.**
> Правило = **устройство (IP) · порт (число или диапазон; внешний = внутренний) · протокол tcp|udp|both · VPN: через / мимо (`direct`) · вкл/выкл**. Всё.
> УБРАНО: label/имя правила, раздельные внешний/внутренний порты, пресеты, `pin`/статические аренды DHCP, показ чужих (LuCI) правил (конфликт с ними проверяется молча → `port_conflict`), WAN-плашка с IP/классами, lanSubnets, блок «Как это работает».
> Осталось от WAN: булево `cgnat` — одна строка-предупреждение в UI, только когда true (WAN-адрес 100.64/10 или частный).
> Контракт: `ui/contract/port_forwards.json`. `set_port_forwards` params `{"rules":[{id?,destIp,port,proto,direct,enabled}]}` (заменяет список). Коды: `port_forwards_set`, `invalid_params`, `too_many`, `dest_not_lan`, `dest_is_router`, `port_conflict`, `locked`, `pending`, `failed`. fw4-секция: `name 'Vectra: <deviceName|destIp>'`.
> Режим «мимо VPN» в nft — без изменений (см. ниже).

Дата: 2026-10-06. Ветка: `feat/vctl-port-forwarding` (от main 290f2b6a, vctl r20).
Решения владельца (06.10): «проброс + режим устройства»; управление — UI роутера **и** Vectra Connect.

## Что получает пользователь

Раздел «Проброс портов»: список правил вида
**«Внешний порт 25565 (TCP) → Minecraft-ПК (192.168.1.50:25565) · трафик устройства: через VPN / мимо VPN · вкл/выкл»**,
плюс плашка состояния WAN: «Белый IP 93.x.x.x — проброс будет работать» / «Адрес провайдера 100.64.x (CGNAT) — снаружи не достучаться» / «Роутер за другим роутером (192.168.x) — пробросьте порт и на нём».

## Честные границы (в UI так и написано)

- **Входящие ЧЕРЕЗ VPN невозможны**: выходные серверы — провайдерские, порт к нам они не пробрасывают. Входящие приходят только на WAN роутера. (Свой VPS-портал через xray reverse — отдельный проект, не здесь.)
- «Трафик устройства мимо VPN» = новые исходящие соединения устройства идут напрямую ядром. **Заблокированные сайты на этом устройстве продолжают открываться через VPN**: роутер отдаёт им FakeDNS-адреса (198.18.x), которые без xray не работают — они из обхода исключены.
- Только IPv4 (DNAT). IPv6-«открыть порт» — не в этой версии.

## Роутер (vctl)

### Хранение — нативный fw4
Правило = секция `config redirect` в `/etc/config/firewall`, именованная `vectra_pf_<id>` (`id` = 8 hex), с `option name 'Vectra: <label>'`, `src 'wan'`, `dest 'lan'`, `target 'DNAT'`, `proto`, `src_dport`, `dest_ip`, `dest_port`, `enabled`, `reflection '1'` (hairpin). Плюс собственная опция `option vectra_direct '1'` для режима «мимо VPN».
- Работает и при остановленном vctl, видно в LuCI.
- vctl трогает **только** свои секции `vectra_pf_*`. Чужие redirect'ы (LuCI, PassWall) показываются read-only, учитываются в проверке конфликтов.
- Применение: `uci` batch → `uci commit firewall` → `fw4 reload` (через `/etc/init.d/firewall reload`). Проверить на стенде, что reload fw4 не трогает `table inet vctl` (ожидание — не трогает; иначе вызвать перепрограммирование своего набора).
- Ответы проброшенного сервера уже идут мимо xray (`ct direction reply return`, nft.go) — изменений в TPROXY для самого проброса не нужно; добавляется тест-регрессия.

### Режим «мимо VPN»
Новый набор `vctl_pf_direct4` (ipv4_addr) в `inet vctl`; в `prerouting` ДО P2P/admit/TPROXY:
```
ct state new ip saddr @vctl_pf_direct4 ip daddr != { <CarriedV4 FakeDNS-пулы> } ct mark set ct mark or <DirectCtMark> counter name "vctl_pf_direct" return
```
- Наполнение = `dest_ip` всех включённых правил с `vectra_direct '1'`. Набор обновляется `nft add/delete element` без перезагрузки всей таблицы.
- Kill switch: **побеждает** (ревью 06.10). При включённом kill switch правило и набор не рендерятся (как P2P-обход), `directActive=false` — UI показывает строку «пока через VPN». Тихой утечки мимо выключателя нет. DNS (53/853) и FakeDNS — всегда в туннеле, независимо от HijackDNS.
- Требует `DirectCtMark` (есть по умолчанию). В rescue-direct режиме набор не нужен (и так всё напрямую).

### Валидация (роутер авторитетен)
- ≤ 32 своих правил; label ≤ 32 символов, печатаемые.
- proto ∈ {tcp, udp, tcp+udp}; внешний порт или диапазон 1–65535; внутренний — порт или диапазон той же длины.
- `dest_ip` — внутри подсети LAN-интерфейса (netifd), не адрес роутера, не network/broadcast. Иначе код `dest_not_lan` / `dest_is_router`.
- Пересечение (proto × внешние порты) с любым другим redirect'ом, своим или чужим → `port_conflict`.
- Внешний порт ∈ слушающих на WAN сервисов роутера (dropbear/uhttpd, если открыты на wan) → `port_conflict`.

### Закрепление адреса
Опциональный флаг правила `pin`: если для `dest_ip` нет статической аренды, vctl находит MAC в `/tmp/dhcp.leases` и создаёт `config host` в `/etc/config/dhcp` (`name`, `mac`, `ip`, метка `vectra_pf '1'`), `reload dnsmasq`. MAC наружу (Connect) **не отдаётся** — только `static: bool`.

### Устройства и WAN
- `devices`: из `dhcp.leases` + статических `host`: `{name, ip, static}` (≤ 64).
- `wan`: `{class: public|cgnat|private|none, ip}` — по WAN-адресу (есть в `wan_check`). `ip` отдаётся только для `public`.

### Интерфейсы
- **ubus (UI роутера):** `port_forwards` (чтение: rules, foreign, devices, wan) и `set_port_forwards` (весь список целиком, как `set_rules`). Контрактные фикстуры `ui/contract/port_forwards.json`. В `ui_lock` — Pro-метод (простой вид его не показывает; пользователи с замком управляют из Connect).
- **Connect action** `set_port_forwards`, capability `set_port_forwards`: параметры `{rules:[{id?,label,proto,extPort,destIp,destPort,enabled,direct,pin}]}`, строгий Parse в `internal/connectactions`, ответ кодами (`applied`, `port_conflict`, `dest_not_lan`, `too_many`, …).
- **Телеметрия Connect** `portForwards`: `{rules, foreignCount, devices, wan}` в `RouterConnectTelemetry` + ограничения в `connecttelemetry.Build`.

### UI роутера (ui/app)
Новый view `PortForwards.tsx` (ru/en/zh в strings.ts): плашка WAN, список правил, лист редактирования (выбор устройства из списка или IP вручную, пресеты: Minecraft 25565, RDP 3389, SSH-на-ПК 22→22, Plex 32400, игровые консоли), чужие правила read-only с подписью «создано в LuCI». Проверка 390 px и обе темы (правило проекта).

## Панель (apps/web + packages/contracts)
- `connectRouterActionNameSchema`, `connectRouterCapabilitySchema`, `CONNECT_ACTION_NAMES`, `CONNECT_CAPABILITY_FLAGS`, zod в `CONNECT_ACTION_PARAMS`.
- `routerConnectTelemetrySchema` + `projectPartnerRouter` — поле `portForwards`.
- Деплой — overlay на прод-мозаику (прод ≠ git: накладывать, не перезаписывать).

## Connect BE (Vectra_Backend) / FE (Vectra_Frontend)
- BE: `ACTIONS`, `validate_action` (зеркало лимитов), capability-гейт, `snapshot_dto` whitelist `portForwards` + `MAX_*`.
- FE: `RouterActionName`, params, `sections.portForward` по capability, строка после Wi-Fi, `RouterPortForwardSheet` (тот же UX, что в UI роутера), `routersCopy.ts` ru/en/zh, моки.
- Порядок выката: vctl r21 → панель → BE → FE. Старые слои просто не предлагают функцию.

## Проверка
- Go: юнит-тесты парсера/валидации/конфликтов, рендера UCI-батча, nft-шаблона (golden), Connect Parse, телеметрии; `go test -race`; кросс-сборка aarch64.
- Стенд dataplane: проброс TCP/UDP снаружи → LAN-хост при работающем TPROXY и включённом kill switch; «мимо VPN» — egress устройства = WAN-IP, а заблокированный домен — через туннель; fw4 reload не ломает `inet vctl`.
- 1111 (живой, разрешён для тестов): правило на тестовый порт, проверка снаружи с VPS (`nc`), откат.
- Панель: tsc (`--incremental false`) + vitest; BE pytest; FE tsc + тесты + скрин 390 px обе темы.
- Независимое ревью (code + security) перед мёржем каждого слоя; мёрж только при всех зелёных.
