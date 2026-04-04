# PassWall2 на OpenWrt 24.xx: база знаний

Дата подготовки: 2026-04-04
Локальный upstream при наличии локального mirror: `passwall2/`
Цель документа: зафиксировать практическую карту PassWall2 для дальнейшей работы из консоли на OpenWrt 24.xx, отдельно разобрав управление сервисом, UCI, обновление правил, подписок, бинарных компонентов и совместимость с Filogic-роутерами уровня Xiaomi AX3000T.

## 1. Что именно было изучено

### Локальные исходники

- `passwall2/luci-app-passwall2/root/etc/init.d/passwall2`
- `passwall2/luci-app-passwall2/root/etc/init.d/passwall2_server`
- `passwall2/luci-app-passwall2/root/etc/hotplug.d/iface/98-passwall2`
- `passwall2/luci-app-passwall2/root/etc/uci-defaults/luci-passwall2`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/app.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/utils.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/tasks.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/monitor.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/subscribe.lua`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/rule_update.lua`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/test.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/socks_auto_switch.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/iptables.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/nftables.sh`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/helper_dnsmasq.lua`
- `passwall2/luci-app-passwall2/luasrc/passwall2/server_app.lua`
- `passwall2/luci-app-passwall2/luasrc/passwall2/com.lua`
- `passwall2/luci-app-passwall2/luasrc/passwall2/api.lua`
- `passwall2/luci-app-passwall2/luasrc/controller/passwall2.lua`
- `passwall2/luci-app-passwall2/Makefile`
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/0_default_config`

### Внешние источники

- Официальный репозиторий PassWall2: <https://github.com/Openwrt-Passwall/openwrt-passwall2>
- Официальный latest release PassWall2 на момент исследования: `26.4.2-1`, опубликован 2026-04-02: <https://github.com/Openwrt-Passwall/openwrt-passwall2/releases/tag/26.4.2-1>
- Официальный репозиторий PassWall: <https://github.com/Openwrt-Passwall/openwrt-passwall>
- Официальный latest release PassWall на момент исследования: `26.4.3-1`, опубликован 2026-04-03: <https://github.com/Openwrt-Passwall/openwrt-passwall/releases/tag/26.4.3-1>
- Документация OpenWrt по `opkg`: <https://openwrt.org/docs/guide-user/additional-software/opkg>
- Документация OpenWrt по `apk`: <https://openwrt.org/docs/guide-user/additional-software/apk>
- Страница релиза OpenWrt 24.10: <https://openwrt.org/releases/24.10/start>
- Device page Xiaomi AX3000T: <https://openwrt.org/toh/xiaomi/ax3000t>

## 2. Краткая архитектура PassWall2

PassWall2 состоит из нескольких слоев:

1. LuCI-пакет `luci-app-passwall2`, который ставит UI, init-скрипты, UCI-шаблоны и helper-скрипты.
2. Главный runtime-скрипт `/usr/share/passwall2/app.sh`, который:
   - читает UCI-конфиг `passwall2`
   - поднимает socks/http/redir-инстансы
   - выбирает `nftables` или `iptables`
   - управляет dnsmasq helper'ами
   - формирует cron/циклические задачи
3. Серверный runtime `/usr/lib/lua/luci/passwall2/server_app.lua`, вызываемый через `/etc/init.d/passwall2_server`.
4. Бинарные компоненты, которыми пользуется PassWall2:
   - `xray-core`
   - `sing-box`
   - `hysteria`
   - `geoview`
   - `v2ray-geoip`
   - `v2ray-geosite`
   - а также опциональные `naiveproxy`, `shadowsocks-rust`, `shadowsocks-libev`, `tuic-client`, `haproxy` и др.

### Главные UCI-конфиги

- Клиентский конфиг: `/etc/config/passwall2`
- Серверный конфиг: `/etc/config/passwall2_server`

### Ключевые секции `passwall2`

- `global`: главный enable/disable, default node, socks, ACL
- `global_forwarding`: выбор проксирования TCP/UDP, `prefer_nft`, IPv6 TProxy
- `global_rules`: правила обновления geoip/geosite и путь к `v2ray_location_asset`
- `global_delay`: delayed start, daemon/watchdog, расписания
- `global_app`: пути к бинарям `xray`, `sing-box`, `hysteria`, `geoview`
- `global_subscribe`: настройки парсинга и фильтрации подписок
- `nodes`: сами ноды
- `socks`: локальные socks/http сервисы
- `subscribe_list`: подписки
- `acl_rule`: ACL-правила
- `haproxy_config`: балансировка
- `shunt_rules`: правила разветвления

### Что важно для OpenWrt 24.xx

По коду `app.sh` и дефолтному конфигу:

- `prefer_nft=1` по умолчанию
- при наличии `fw4` и `dnsmasq` с `nftset` PassWall2 выбирает `nftables`
- если окружение `nftables` неполное, скрипт автоматически откатывается на `iptables` и пишет предупреждение в лог

Для OpenWrt 24.xx это ожидаемое поведение, потому что `firewall4` и `nftables` являются штатным путем.

## 3. Что реально управляет сервисом из консоли

### 3.1 Основные init-команды

Это основной и поддерживаемый способ управления.

```sh
/etc/init.d/passwall2 enable
/etc/init.d/passwall2 disable
/etc/init.d/passwall2 start
/etc/init.d/passwall2 stop
/etc/init.d/passwall2 restart
/etc/init.d/passwall2 reload
```

Замечания:

- `reload` не делает мягкий reload. В коде он явно пишет предупреждение и делает полный `restart`.
- `passwall2` использует lock-файл `/var/lock/passwall2.lock`, поэтому параллельный старт/стоп подавляется.

Серверный режим:

```sh
/etc/init.d/passwall2_server enable
/etc/init.d/passwall2_server disable
/etc/init.d/passwall2_server start
/etc/init.d/passwall2_server stop
/etc/init.d/passwall2_server restart
```

### 3.2 Логи и временное состояние

```sh
logread | grep -i passwall2
tail -f /tmp/log/passwall2.log
tail -f /tmp/log/passwall2_server.log
ls -la /tmp/etc/passwall2
ls -la /tmp/etc/passwall2_server
ls -la /var/lock | grep passwall2
```

Что означает:

- `/tmp/log/passwall2.log`: главный runtime-лог клиента
- `/tmp/log/passwall2_server.log`: лог server mode
- `/tmp/etc/passwall2`: временные конфиги, pid-like артефакты, generated json, dnsmasq helper state
- `/var/lock/passwall2_ready.lock`: сервис прошел delayed boot и считается поднятым

### 3.3 Базовое управление через UCI

Это основной путь автоматизации из shell.

Проверить общий статус:

```sh
uci get passwall2.@global[0].enabled
uci get passwall2.@global[0].node
uci get passwall2.@global[0].socks_enabled
uci get passwall2.@global_forwarding[0].prefer_nft
uci get passwall2.@global_rules[0].v2ray_location_asset
```

Посмотреть все ноды:

```sh
uci show passwall2 | grep "=nodes"
```

Посмотреть подписки:

```sh
uci show passwall2 | grep "=subscribe_list"
```

Посмотреть socks-секции:

```sh
uci show passwall2 | grep "=socks"
```

Включить сервис и переключить main node:

```sh
uci set passwall2.@global[0].enabled='1'
uci set passwall2.@global[0].node='<node_id>'
uci commit passwall2
/etc/init.d/passwall2 restart
```

Выключить сервис:

```sh
uci set passwall2.@global[0].enabled='0'
uci commit passwall2
/etc/init.d/passwall2 stop
```

Примечание: реальная смена активной ноды применяется после `restart`, потому что `app.sh` заново собирает runtime из UCI.

## 4. CLI-команды PassWall2 сверх init.d

Ниже перечислено то, что реально имеет entrypoint в исходниках.

### 4.1 Подписки

Обновить конкретную подписку:

```sh
lua /usr/share/passwall2/subscribe.lua start <cfgid>
```

Обновить несколько подписок:

```sh
lua /usr/share/passwall2/subscribe.lua start cfgid1,cfgid2
```

Обновить все подписки:

```sh
lua /usr/share/passwall2/subscribe.lua start all
```

Ручной режим обновления из LuCI-логики:

```sh
lua /usr/share/passwall2/subscribe.lua start <cfgid> manual
lua /usr/share/passwall2/subscribe.lua start all manual
```

Удалить все импортированные из подписки ноды:

```sh
lua /usr/share/passwall2/subscribe.lua truncate
```

Удалить ноды конкретной subscription group:

```sh
lua /usr/share/passwall2/subscribe.lua truncate "GroupName"
```

Импорт из сырого списка ссылок:

```sh
cat >/tmp/links.conf <<'EOF'
ss://...
vmess://...
vless://...
EOF

lua /usr/share/passwall2/subscribe.lua add "ManualGroup"
```

Что делает `subscribe.lua`:

- скачивает subscription URL
- проверяет md5 содержимого
- парсит ссылки в ноды
- удаляет старые ноды `add_mode=2` при обновлении
- пытается сохранить привязки main node, ACL, socks, balancer и backup node через механизм сопоставления
- если запуск не `manual`, перезапускает `/etc/init.d/passwall2`

### 4.2 Обновление geoip/geosite

Обновить по текущим флагам из UCI:

```sh
lua /usr/share/passwall2/rule_update.lua log
```

Обновить только `geoip.dat`:

```sh
lua /usr/share/passwall2/rule_update.lua log geoip
```

Обновить только `geosite.dat`:

```sh
lua /usr/share/passwall2/rule_update.lua log geosite
```

Обновить оба набора:

```sh
lua /usr/share/passwall2/rule_update.lua log geoip,geosite
```

Что делает `rule_update.lua`:

- берет URL из `passwall2.@global_rules[0].geoip_url` и `geosite_url`
- скачивает файлы в `/tmp`
- по возможности валидирует размер и SHA256
- старые данные перекладывает в `/tmp/bak_v2ray/`
- новые файлы кладет в `v2ray_location_asset`
- выставляет `passwall2.@global[0].flush_set='1'`
- сохраняет UCI и инициирует перезапуск через штатную логику

### 4.3 Проверка ноды и connectivity

Проверка URL напрямую:

```sh
/usr/share/passwall2/test.sh test_url https://www.google.com/generate_204 1 3
```

Проверка конкретной ноды через временный socks-инстанс:

```sh
/usr/share/passwall2/test.sh url_test_node <node_id>
```

Выход:

- `200:<latency>` обычно означает успех для `url_test_node`
- в остальных случаях надо смотреть лог и UCI-описание ноды

### 4.4 Автопереключение socks

Это не основной пользовательский API, но скрипт имеет отдельный entrypoint:

```sh
/usr/share/passwall2/socks_auto_switch.sh <socks_section_id>
```

Скрипт:

- периодически проверяет доступность текущей socks-ноды
- тестирует backup nodes
- вызывает `app.sh socks_node_switch flag=<socks_section_id> new_node=<node_id>`

### 4.5 Внутренние runtime-команды `app.sh`

Поддерживаемые entrypoints:

```sh
/usr/share/passwall2/app.sh start
/usr/share/passwall2/app.sh stop
/usr/share/passwall2/app.sh run_socks ...
/usr/share/passwall2/app.sh socks_node_switch flag=<socks_section_id> new_node=<node_id>
```

Практически полезно:

```sh
/usr/share/passwall2/app.sh run_socks \
  flag=testnode \
  node=<node_id> \
  bind=127.0.0.1 \
  socks_port=10808 \
  config_file=testnode.json
```

Это внутренний API. Его стоит использовать только для отладки или автоматизации, когда уже понятно, как PassWall2 формирует runtime.

### 4.6 Внутренние firewall helper-команды

`iptables.sh`:

```sh
/usr/share/passwall2/iptables.sh start
/usr/share/passwall2/iptables.sh stop
/usr/share/passwall2/iptables.sh get_ipt_bin
/usr/share/passwall2/iptables.sh get_ip6t_bin
/usr/share/passwall2/iptables.sh filter_direct_node_list
```

Также есть внутренние служебные команды:

```sh
/usr/share/passwall2/iptables.sh RULE_LAST_INDEX ...
/usr/share/passwall2/iptables.sh insert_rule_before ...
/usr/share/passwall2/iptables.sh insert_rule_after ...
```

`nftables.sh`:

```sh
/usr/share/passwall2/nftables.sh start
/usr/share/passwall2/nftables.sh stop
/usr/share/passwall2/nftables.sh insert_nftset ...
/usr/share/passwall2/nftables.sh filter_direct_node_list
/usr/share/passwall2/nftables.sh mwan3_start
/usr/share/passwall2/nftables.sh mwan3_stop
```

Это low-level helper'ы. Для обычной эксплуатации предпочтителен `/etc/init.d/passwall2 restart`.

### 4.7 helper_dnsmasq.lua

Поддерживаемые функции:

```sh
lua /usr/share/passwall2/helper_dnsmasq.lua restart '{"LOG":"1"}'
lua /usr/share/passwall2/helper_dnsmasq.lua logic_restart '{"LOG":"1"}'
lua /usr/share/passwall2/helper_dnsmasq.lua copy_instance '{"LISTEN_PORT":"5353"}'
lua /usr/share/passwall2/helper_dnsmasq.lua add_rule '{"FLAG":"default"}'
```

Это тоже внутренний API. В проде им стоит пользоваться только если надо адресно лечить dnsmasq-часть без полного перезапуска.

## 5. Что автоматизируется самим PassWall2

### Delayed boot

`/etc/init.d/passwall2` по умолчанию стартует не сразу, а через `global_delay.start_delay`.

В дефолтном конфиге это `60` секунд.

### Hotplug при `ifup`

`/etc/hotplug.d/iface/98-passwall2` перезапускает сервис, если:

- произошло `ifup`
- `passwall2.@global[0].enabled=1`
- существует `passwall2_ready.lock`
- поднялся default route interface

### Watchdog/monitor

`monitor.sh` при `start_daemon=1` каждые ~58 секунд проверяет, живы ли сохраненные процессы, и перезапускает их.

### Cron и циклические таски

`app.sh` формирует задания в `/etc/crontabs/root` для:

- auto stop
- auto start
- auto restart
- auto update rules
- auto update subscriptions

Если schedule mode настроен как interval mode, запускается бесконечный loop из `tasks.sh`.

## 6. Обновление PassWall2 и компонентов: разделение на уровни

Это ключевой раздел. У PassWall2 есть несколько разных типов обновления, и их нельзя смешивать в одну процедуру.

### Уровень A: обновление пакета `luci-app-passwall2`

Это обновление самого приложения PassWall2.

Что выяснено по коду:

- `api.to_check_self()` сравнивает локальную версию не с latest release, а с `Makefile` из ветки `main`
- при обнаружении новой версии код явно возвращает сообщение, что автоматическое обновление приложения не поддерживается
- итог: приложение обновляется вручную, через установку `ipk` или `apk`

Практический вывод:

- на OpenWrt 24.xx обновлять сам `luci-app-passwall2` нужно вручную пакетами `ipk`
- встроенная кнопка проверки версии полезна только как индикатор, а не как real updater

### Уровень B: обновление бинарных компонентов

По `com.lua` и `api.lua` внутренний updater умеет работать с:

- `hysteria`
- `sing-box`
- `xray`
- `geoview`

Как это устроено:

- PassWall2 берет release metadata через cache JSON, публикуемый из `openwrt-passwall-packages`
- auto-detect architecture идет через `OPENWRT_ARCH` и `DISTRIB_ARCH`
- `to_download()` скачивает архив или бинарь во временный файл
- `to_extract()` распаковывает архив
- `to_move()` делает прямой `mv` бинаря в целевой путь, обычно в `/usr/bin/...`

Важно:

- это не пакетный менеджер
- `to_move()` физически заменяет бинарь на диске
- база `opkg` или `apk` при этом не обновляется

Инженерный вывод из кода:

- на OpenWrt 24.xx безопаснее обновлять компоненты пакетами `opkg`, а не встроенным binary mover'ом
- встроенный updater годится как аварийный/manual binary replacement, но создает drift между установленным пакетом и фактическим бинарем

### Уровень C: обновление geo-баз

Это `rule_update.lua`. Оно не обновляет приложение и не обновляет `xray-core`/`sing-box`.

Обновляет только:

- `geoip.dat`
- `geosite.dat`

### Уровень D: обновление подписок

Это `subscribe.lua`. Оно не обновляет бинарные компоненты и не обновляет сам пакет.

## 7. Что ставить на OpenWrt 24.xx

### Главное правило

Для OpenWrt `24.xx` использовать нужно `ipk` и `opkg`.

По официальной документации OpenWrt:

- `opkg` относится к OpenWrt `24.10 and older`
- `apk` относится к OpenWrt `25.12 and newer`

То есть для OpenWrt 24.xx любые `.apk` из PassWall2 release не нужны.

### Что скачать для PassWall2 на 24.xx

На момент исследования latest PassWall2 release: `26.4.2-1`.

Минимум:

- `luci-app-passwall2_26.4.2-r1_all.ipk`

Если у системы нет готового feed с зависимостями, нужны еще архитектурные пакеты из архива:

- `passwall_packages_ipk_<arch>.zip`

Для Xiaomi AX3000T и совместимых `aarch64_cortex-a53`:

- `passwall_packages_ipk_aarch64_cortex-a53.zip`

## 8. Xiaomi AX3000T и похожие Filogic-роутеры

### Что подтверждено внешними источниками

Официальная device page OpenWrt для Xiaomi AX3000T указывает:

- SoC: MediaTek `MT7981B`
- CPU: `ARM Cortex-A53`
- package architecture: `aarch64_cortex-a53`

Это хорошо совпадает с тем, что в latest PassWall2 release присутствуют оба архива:

- `passwall_packages_ipk_aarch64_cortex-a53.zip`
- `passwall_packages_apk_aarch64_cortex-a53.zip`

Для OpenWrt 24.xx на AX3000T нужен именно первый.

### Как не ошибиться на похожих Filogic-устройствах

Перед установкой на любом "похожем" роутере обязательно проверить:

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH)' /etc/openwrt_release
opkg print-architecture
uname -m
```

Правило выбора:

- если `DISTRIB_ARCH` равен `aarch64_cortex-a53`, берем `passwall_packages_ipk_aarch64_cortex-a53.zip`
- если архитектура другая, берем zip строго под свой `DISTRIB_ARCH`
- если система уже не 24.xx, а 25.12+ и использует `apk`, тогда брать `.apk` и `passwall_packages_apk_<arch>.zip`

## 9. Практический сценарий обновления на Xiaomi AX3000T с OpenWrt 24.xx

Ниже безопасный сценарий для ручного обновления.

### 9.1 Проверка платформы

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH)' /etc/openwrt_release
opkg print-architecture
```

Ожидаемо для AX3000T:

- OpenWrt `24.xx`
- `DISTRIB_ARCH='aarch64_cortex-a53'`

### 9.2 Резервная копия

Сделать минимум:

```sh
cp /etc/config/passwall2 /root/passwall2.backup
cp /etc/config/passwall2_server /root/passwall2_server.backup
cp /usr/share/passwall2/domains_excluded /root/domains_excluded.backup
```

Либо использовать backup из LuCI. В коде backup-логика упаковывает именно эти три файла.

### 9.3 Остановить сервисы

```sh
/etc/init.d/passwall2 stop
/etc/init.d/passwall2_server stop
```

### 9.4 Скачать нужные артефакты

Пример для latest release `26.4.2-1`:

```sh
mkdir -p /tmp/pw2 && cd /tmp/pw2
wget https://github.com/Openwrt-Passwall/openwrt-passwall2/releases/download/26.4.2-1/luci-app-passwall2_26.4.2-r1_all.ipk
wget https://github.com/Openwrt-Passwall/openwrt-passwall2/releases/download/26.4.2-1/passwall_packages_ipk_aarch64_cortex-a53.zip
```

### 9.5 Установить архитектурные зависимости

```sh
mkdir -p /tmp/pw2/pkgs
unzip -o passwall_packages_ipk_aarch64_cortex-a53.zip -d /tmp/pw2/pkgs
opkg install /tmp/pw2/pkgs/*.ipk
opkg install /tmp/pw2/luci-app-passwall2_26.4.2-r1_all.ipk
```

Если часть компонентов уже есть, `opkg` либо обновит, либо откажет на несовместимых зависимостях. Это лучше, чем прямой `mv` бинарей мимо пакетной базы.

### 9.6 Перезапустить службы

```sh
/etc/init.d/rpcd restart
/etc/init.d/passwall2 restart
/etc/init.d/passwall2_server restart
```

### 9.7 Проверить результат

```sh
opkg list-installed | grep -E 'passwall2|xray|sing-box|hysteria|geoview|v2ray-geo'
tail -n 100 /tmp/log/passwall2.log
```

## 10. Как обновлять именно компоненты программы

### Вариант 1. Рекомендуемый для OpenWrt 24.xx

Обновлять компонентные пакеты через `opkg`.

Примеры имен пакетов, которые реально фигурируют в Makefile/release:

- `xray-core`
- `sing-box`
- `hysteria`
- `geoview`
- `v2ray-geoip`
- `v2ray-geosite`
- `naiveproxy`
- `shadowsocks-rust-sslocal`
- `shadowsocks-libev-ss-redir`
- `tuic-client`

Если feed настроен правильно:

```sh
opkg update
opkg install xray-core sing-box geoview v2ray-geoip v2ray-geosite
```

Если feed не настроен, использовать zip из релиза PassWall2 и ставить локальные `.ipk`.

### Вариант 2. Через встроенный updater PassWall2

Этот путь существует в коде, но для 24.xx я бы считал его вспомогательным.

Причина:

- он не вызывает `opkg`
- он не регистрирует пакетную транзакцию
- он просто скачивает/распаковывает и двигает бинарь в target path

Использовать его стоит только если:

- нужен быстрый тест нового бинаря
- package feed временно отсутствует
- есть понимание, что потом состояние нужно привести к консистентному через пакетный менеджер

## 11. Когда обновлять всю программу, а когда только правила или компоненты

### Обновить только `geoip/geosite`, если:

- поменялись правила маршрутизации
- ноды живы, но гео-маршрутизация устарела
- нет необходимости менять `xray`/`sing-box`

Команда:

```sh
lua /usr/share/passwall2/rule_update.lua log geoip,geosite
```

### Обновить только подписки, если:

- провайдер сменил ноды
- нужно подтянуть новые VMess/VLESS/SS endpoints

Команда:

```sh
lua /usr/share/passwall2/subscribe.lua start all
```

### Обновить только бинарные компоненты, если:

- есть конкретный баг в `xray-core` или `sing-box`
- нужно получить поддержку нового протокольного поведения

Для 24.xx лучше через `opkg` и релизный zip под архитектуру.

### Обновить весь PassWall2, если:

- меняется логика приложения
- в changelog/release нужны новые поля UCI/LuCI
- меняется интеграция с OpenWrt 24.xx

Тогда обновлять нужно именно `luci-app-passwall2` плюс связанные компонентные пакеты.

## 12. Важные наблюдения по коду

### 12.1 `reload` не является мягким reload

И у `passwall2`, и у `passwall2_server` `reload()` делает фактически `restart`.

### 12.2 Горячий `ifup` может сам перезапустить сервис

Если вы чините сетевую часть и одновременно тестируете PassWall2, учтите, что `98-passwall2` может сам дернуть `restart`.

### 12.3 `start_daemon=1` включает watchdog-поведение

То есть часть "самоподъема" процессов происходит не только через init, но и через `monitor.sh`.

### 12.4 `prefer_nft=1` не гарантирует, что реально будет nftables

Скрипт проверяет:

- наличие `fw4`
- наличие `dnsmasq` с `nftset`
- kernel modules для nft tproxy/socket/nat

Если чего-то нет, он логически уходит в `iptables`.

### 12.5 Проверка новой версии приложения ориентируется на `main`

Это значит, что UI-проверка версии PassWall2 не обязательно соответствует latest stable release.

### 12.6 Встроенный updater компонентов подменяет бинарь напрямую

Это важный operational detail: на OpenWrt 24.xx при нормальной эксплуатации лучше держать компоненты под контролем `opkg`.

## 13. Минимальный набор команд для ежедневной эксплуатации

```sh
# Статус и базовая диагностика
uci get passwall2.@global[0].enabled
uci get passwall2.@global[0].node
logread | grep -i passwall2
tail -f /tmp/log/passwall2.log

# Перезапуск
/etc/init.d/passwall2 restart

# Смена main node
uci set passwall2.@global[0].node='<node_id>'
uci commit passwall2
/etc/init.d/passwall2 restart

# Обновление подписок
lua /usr/share/passwall2/subscribe.lua start all

# Обновление geo-баз
lua /usr/share/passwall2/rule_update.lua log geoip,geosite

# Проверка конкретной ноды
/usr/share/passwall2/test.sh url_test_node <node_id>
```

## 14. Рекомендуемая стратегия для дальнейшей работы

Для роутеров класса Xiaomi AX3000T на OpenWrt 24.xx я бы придерживался следующего правила:

1. Конфигурировать PassWall2 через UCI и init-скрипты.
2. Подписки и geodata обновлять встроенными `lua`-скриптами.
3. Бинарные компоненты и сам `luci-app-passwall2` обновлять пакетно через `opkg` и release-артефакты под точную архитектуру.
4. Встроенный component updater использовать только как fallback/manual override.
5. Перед любым крупным обновлением сохранять `/etc/config/passwall2`, `/etc/config/passwall2_server`, `/usr/share/passwall2/domains_excluded`.

## 15. Актуальные версии на момент исследования

Согласно official release `Openwrt-Passwall/openwrt-passwall2` от 2026-04-02:

- PassWall2: `26.4.2-1`
- `chinadns-ng`: `2025.08.09`
- `geoview`: `0.2.5`
- `hysteria`: `2.8.1`
- `naiveproxy`: `143.0.7499.109`
- `shadowsocks-rust`: `1.24.0`
- `shadowsocksr-libev`: `2.5.6`
- `simple-obfs`: `0.0.5`
- `sing-box`: `1.13.5`
- `tcping`: `0.3`
- `tuic-client`: `1.7.2`
- `v2ray-plugin`: `5.48.0`
- `xray-core`: `26.3.27`
- `v2ray-geoip`: `202603260032`
- `v2ray-geosite`: `202603292224`

## 16. Источники

- PassWall2 repo: <https://github.com/Openwrt-Passwall/openwrt-passwall2>
- PassWall2 latest release `26.4.2-1`: <https://github.com/Openwrt-Passwall/openwrt-passwall2/releases/tag/26.4.2-1>
- PassWall repo latest release `26.4.3-1`: <https://github.com/Openwrt-Passwall/openwrt-passwall/releases/tag/26.4.3-1>
- OpenWrt `opkg`: <https://openwrt.org/docs/guide-user/additional-software/opkg>
- OpenWrt `apk`: <https://openwrt.org/docs/guide-user/additional-software/apk>
- OpenWrt 24.10 release page: <https://openwrt.org/releases/24.10/start>
- OpenWrt device page Xiaomi AX3000T: <https://openwrt.org/toh/xiaomi/ax3000t>
