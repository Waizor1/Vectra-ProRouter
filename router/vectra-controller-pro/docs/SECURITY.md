# Безопасность роутера Vectra

Что роутер с vctl бережёт, чего сберечь не может и что для этого делает
оператор. Репозиторий открытый: защита не держится на том, что кто-то не
видел код, — только на ключах, которых в репозитории нет.

## Что на роутере секретно

Всё это лежит в файлах с правами `0600` (читает только root):

- `/etc/vectra-controller-pro/state.json` — токен роутера для панели и ключ
  устройства (ed25519);
- `/etc/vectra-controller-pro/xray-desired.json` — адрес подписки (в нём её
  токен);
- `/etc/vectra-controller-pro/provider-config.json`, список узлов,
  `/var/run/vectra-controller-pro/xray.json` (то, что запускает xray) — узлы
  провайдера: UUID, пароли, ключи Reality;
- журнал xray (в его ошибках бывают куски конфига).

## Что роутер защищает

1. **Ответы интерфейса роутера без учётных данных.** Каждый ответ ubus-объекта
   `vectra` (его читает LuCI) проходит одну чистку (`cmd/vctl/rpcd_scrub.go`,
   `internal/redact`): точные секреты роутера и всё, что похоже на учётные
   данные по виду (ссылки на узлы, токены в адресах, UUID, ключи). Охранный
   тест (`cmd/vctl/rpcd_secrets_test.go`) вызывает каждый метод на роутере, где
   каждый файл набит поддельными секретами. Отчёт в поддержку тоже приходит
   без паролей и ключей.
2. **Только https, с проверкой сертификата.** Адрес панели обязан быть https
   (кроме самого роутера, loopback). Клиент панели не идёт ни по одному
   редиректу (токен роутера в каждом запросе); подписка и загрузки не идут по
   редиректу с https на http.
3. **Обновления — только из нашего подписанного фида.** Задание панели
   `update_controller` ставит пакет, только если индекс фида `vectra_pro`
   (строку в `/etc/opkg/customfeeds.conf` пишет установщик) подписан ключом из
   `/etc/opkg/keys` и в нём есть `vectra-controller-pro` с тем же sha256, под
   архитектуру роутера и той версии, что названа в задании. Иначе — отказ
   `update_controller: not in the signed Vectra feed (nothing installed): …`,
   пакет даже не скачивается. Подписать фид панель не может: ключ подписи
   лежит только на хосте сборки.
4. **Обновление гео-данных пишет только в свой каталог**
   (`/usr/share/vectra-controller-pro/geo`), имена файлов — простые, без путей.
5. **Терминал поддержки выключен на новых роутерах.** `run_terminal_command`
   выполняет любые команды от root, поэтому vctl выполняет его только там, где
   разрешил владелец: `uci vectra-controller-pro.main.remote_shell`. Новый
   роутер получает `'0'`; роутер, где уже работал vctl или старый агент
   Vectra, — `'1'` (у него терминал был). Решается один раз, при установке;
   дальше — только владелец: переключатель «Доступ поддержки к роутеру» в
   интерфейсе роутера или `uci set vectra-controller-pro.main.remote_shell=0 &&
   uci commit vectra-controller-pro`. Панель видит его в каждом check-in
   (`inventory.remoteShell`).
6. **Документ провайдера не решает за роутер** (r12,
   `internal/coreengine/xray/provider_guard.go`). Два класса.
   **Опасное — документ отклоняется целиком:** `reverse` где угодно (мосты и
   порталы, reverse у VLESS), `redirect` у freedom, `certificateFile`/`keyFile`
   в `tlsSettings.certificates` и `masterKeyLog`, гео-список `ext:` с путём
   (`/`, `\`, `..`), управляющий символ или длина больше 256 байт в теге,
   имени балансировщика, правила или ключа, пул FakeDNS вне 198.18.0.0/15 и
   fc00::/7. Отклонённый документ не ставится: роутер работает на прежнем
   рендере, а после перезагрузки — на последнем удачном рендере, который
   хранится зашифрованным на флеше (`xray-last-good.json`); инцидент
   `PROVIDER_REFUSED`, `RENDER_RESUME_FALLBACK` или `RENDER_RESUME_FAILED`.
   **Ненужное — выбрасывается, остальное применяется** (одна строка
   предупреждения в журнале, инцидент `PROVIDER_PARTS_DROPPED`): верхний
   уровень — белый список (`dns`, `routing`, `outbounds`, `policy`, `stats`,
   `observatory`, `burstObservatory`, `remarks`, `version`, `fakedns`; `log`
   заменяется журналом роутера, `inbounds` — входом TPROXY); всё прочее
   (`api`, `metrics`, `env`, `transport`, `$schema`, `assets`, будущие ключи —
   в любом написании) не попадает в xray. С DNS через туннель записи
   `dns.hosts`, перекрывающие имена, которые роутер разрешает напрямую
   (панель, NTP), выбрасываются.
7. **Порт TPROXY не принимает соединений напрямую.** Цепочка
   `inbound_guard` сбрасывает пакеты исходного направления на порт TPROXY,
   адресованные самому роутеру (их принёс не TPROXY): иначе одно соединение
   заставляло xray проксировать сам в себя до исчерпания дескрипторов
   (счётчик `vctl_inbound_guard`). Ответы на собственные сокеты роутера,
   у которых локальный порт случайно совпал с портом TPROXY (dnsmasq
   спрашивает со случайных портов), не трогаются.
8. **xray не открывает соединений в LAN** (цепочка `lan_egress_guard`,
   счётчик `vctl_lan_dial`): сокет xray (метка 0x5644), уходящий с адреса
   самого роутера в br-lan или гостевой мост, сбрасывается — ни
   подставленный `Host: 192.168.1.1`, ни документ провайдера не делают xray
   дверью в локальную сеть. Ответы xray клиентам LAN уходят с адреса,
   к которому обращался клиент (TPROXY), и проходят. Границы:
   - устройства LAN берутся у netifd при загрузке правил (статические,
     поднятые интерфейсы без маршрута по умолчанию); не ответил netifd —
     только `br-lan`, а гостевые сети без защиты до следующей загрузки
     правил;
   - IPv6 между двумя сетями LAN с глобальными адресами, пока IPv6 несётся
     туннелем (не отключён), раньше шёл через xray и теперь сбрасывается;
     с отключённым IPv6 (`RefuseIPv6`) такой трафик в xray не попадает;
   - LAN с публичными IPv4-адресами (не из частных диапазонов) — то же для
     трафика между её подсетями;
   - соединение xray на собственный адрес роутера (`Host: 192.168.1.1:80`)
     идёт через `lo`, а не в LAN: его эта цепочка не видит; API, метрики и
     проба выхода закрыты `local_guard`, порт TPROXY — `inbound_guard`,
     остальные службы роутера (LuCI, SSH, dnsmasq) отвечают xray так же, как
     клиенту LAN.

## Ключ устройства и подписанный User-Agent

- Ключ устройства (ed25519) роутер создаёт сам при первом запуске (или берёт
  у старого агента Vectra) и хранит в `state.json`. Открытая половина уходит в
  панель и в QR привязки; при регистрации роутер подписью доказывает, что ключ
  у него, — заранее созданная запись панели достаётся только своему роутеру.
- Подписку роутер запрашивает с User-Agent `VectraRouter/<версия>
  vr1.<жетон>` (`internal/uatoken`, `internal/routersign`). Жетон подписан
  ключом устройства, привязан ко времени, HWID и самой подписке (пути
  запроса) и зашифрован ключом Vectra (`claimKey` из check-in, иначе
  встроенный kid 1; закрытая половина — только у бэкенда Vectra Connect).
  Скопированный User-Agent не подходит к другой подписке или другому
  устройству, а новый без ключа устройства не сделать. Подписку без живого
  жетона бэкенд не отдаёт, когда он это проверяет (ворота 5 в
  `docs/LAUNCH.md`).

## Чего роутер защитить не может

- **root — это всё.** Кто знает пароль root (LuCI, SSH) или держит роутер в
  руках (UART, снятая флеш-память), прочитает всё, что запускает xray: узлы,
  пароли, ключ устройства. xray нужны учётные данные в открытом виде, а
  шифрование на диске с ключом на том же диске ничего не даёт. Поэтому пароль
  root обязателен (его просит LuCI), а LuCI и SSH в WAN не открывать.
- **Резервные копии.** sysupgrade и «Резервная копия» LuCI сохраняют
  `/etc/vectra-controller-pro/` и `/etc/config/vectra-controller-pro`: там ключ
  устройства, токен панели, адрес подписки и документ провайдера. Копия
  роутера — такой же секрет, как пароль: не пересылать, не выкладывать.
- **Панель.** Она задаёт конфиг роутера, а где владелец включил терминал —
  выполняет команды от root. Учётная запись оператора панели — ключ от всех
  его роутеров.
- **Ключи в `/etc/opkg/keys`.** opkg и vctl доверяют любому из них. Чужие
  ключи на роутеры не добавлять.

## Что делать оператору

- **Ключ подписи фида** (`vectra-pro.sec`) держать только на хосте сборки
  (VPS, `/opt/vectra-prorouter/.codex-runtime/pro-feed-keys/`, права `600`):
  никогда в git, в контейнере панели, в чате. Копию — офлайн.
- **Ключ подписи утёк или сменён** — подписать фид новым ключом
  (`scripts/sign-pro-feed.sh` тут же печёт в установщик его открытую
  половину) и прогнать новый установщик на каждом роутере: он кладёт новый
  ключ в `/etc/opkg/keys`. До этого роутер обновление через панель
  отклоняет (`unknown key`) — отказ безопасный. Утёкший старый ключ убрать с
  роутеров (`/etc/opkg/keys/<его номер>`): пока он там, им подписанное
  роутер примет.
- **Роутер ставили не установщиком** (нет строки `vectra_pro` в
  `customfeeds.conf` или ключа фида в `/etc/opkg/keys`) — `update_controller`
  на нём откажет. Добавить их так же, как установщик: строку
  `src/gz vectra_pro <адрес фида>/<архитектура>` и открытую половину ключа
  (`vectra-pro.pub`) файлом `/etc/opkg/keys/<номер ключа>`.
- **Утекли учётные данные** (копия роутера, отчёт, скриншот, пароль root) —
  менять то, что утекло, не дожидаясь злоупотребления: подписку — у
  провайдера / в Vectra Connect (новый адрес обесценивает утёкшую копию:
  жетон User-Agent привязан к адресу подписки, а нового в копии нет); пароль
  root — на роутере. Утёк `state.json` (токен панели, ключ устройства) —
  роутеру нужна новая личность: старую запись роутера удалить в панели, а
  роутер поставить заново начисто (`vectra-install.sh --uninstall --purge`,
  затем установщик). Где раньше работал старый агент, убрать и его
  `/etc/vectra-controller/state.json`: иначе vctl снова возьмёт личность
  оттуда — ту же, утёкшую.
- **Терминал поддержки** просить у владельца включить на время работы и
  выключить после.

## Connect r37

Typed jobs validate router, adopted ownerRef and actionId before mutation. Strict per-action schemas reject unknown, duplicate or malformed fields. Journal receipts carry hashes and outcomes, never Wi-Fi passwords. Secret readback needs a verified successful Wi-Fi action, matching current owner and AP identity, no pending/orphan setup; release or owner reassignment removes the marker. Only the confidential cloned HTTPS check-in includes this value; panel authorization and encryption remain a separate boundary.

Subscription redirects may remain only on the original HTTPS origin, with the same effective port and without userinfo. This prevents custom headers/HWID escaping to another server; the caller policy can restrict redirects further. These controls do not hide runtime VPN credentials from root: Xray must consume them, and root can read its config and memory. Root-extraction/upstream revocation audit is a separate release gate; no custom crypto or root-proof promise is introduced.

OpenWrt keep.d preserves /etc/vectra-controller-pro, including device identity, panel token and last-good recovery state. Exported root backups therefore need the same secret custody and revocation policy as the original device; ordinary file permissions and encrypted panel storage do not protect these copies from their privileged holder. This local work exports no live backup.

Normal raw update_controller jobs require a signed package at least as new as the installed controller before artifact download. Unknown installed version or comparison failure refuses the update; equal reinstall remains possible. No downgrade bypass was added. Explicit recovery policy would require separate authorization/design.
