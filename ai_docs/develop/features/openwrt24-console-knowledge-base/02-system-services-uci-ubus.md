# Система, сервисы, UCI и ubus

Дата подготовки: 2026-04-04

## 1. Идентификация системы и версии

Начинать любое исследование OpenWrt нужно с подтверждения board, release и architecture.

```sh
ubus call system board
ubus call system info
cat /etc/openwrt_release
uname -a
uname -m
opkg print-architecture
cat /tmp/sysinfo/board_name
cat /tmp/sysinfo/model
```

Что особенно важно:

- `ubus call system board` возвращает `model`, `board_name`, `kernel`, `rootfs_type`, поля `release`
- `ubus call system info` дает uptime, load average, memory, swap, local time
- `/etc/openwrt_release` удобен для `grep`, automation и быстрого сравнения релиза
- `opkg print-architecture` важен для выбора правильных `.ipk`

Пример адресного чтения через `jsonfilter`:

```sh
ubus call system board | jsonfilter -e '@.board_name'
ubus call system board | jsonfilter -e '@.release.version'
ubus call system info | jsonfilter -e '@.memory.available'
```

## 2. Сервисный слой: rc.common, /etc/init.d и service

### 2.1 Стандартные действия init-скриптов

По `rc.common` для обычных сервисов стандартный набор действий:

- `start`
- `stop`
- `restart`
- `reload`
- `enable`
- `disable`
- `enabled`

Если сервис использует `procd`, дополнительно доступны:

- `running`
- `status`
- `trace`
- `info`

Практика:

```sh
/etc/init.d/network restart
/etc/init.d/dnsmasq reload
/etc/init.d/firewall status
/etc/init.d/uhttpd enabled
```

### 2.2 Обертка service

Команда `service` работает как удобная обертка над `/etc/init.d/<name>`.

Примеры:

```sh
service
service network restart
service dnsmasq status
service firewall reload
```

Что полезно знать:

- без аргументов `service` перечисляет init-скрипты
- при этом она сверяет enabled/disabled и running/stopped
- для оценки runtime-состояния она опирается на `ubus call service list`

### 2.3 reload_config

`reload_config` не "перезапускает все подряд".

Что он делает по коду:

- снимает снимок текущих UCI-пакетов из `/etc/config/*`
- сравнивает md5 с предыдущим снимком
- для изменившихся пакетов шлет `ubus call service event` с типом `config.change`

Это важно понимать правильно:

- `uci commit` пишет конфиг
- `reload_config` рассылает событие о том, что конфиг изменился
- дальше уже конкретный `procd`-managed сервис может на это событие отреагировать

Базовый workflow:

```sh
uci set dhcp.lan.leasetime='4h'
uci commit dhcp
reload_config
```

Если сервис не реагирует на `config.change`, потребуется явный reload/restart.

## 3. Вспомогательные системные команды

### 3.1 hotplug-call

```sh
hotplug-call iface
hotplug-call net
hotplug-call usb
```

Назначение:

- вручную прогнать хендлеры из `/etc/hotplug.d/<subsystem>/`
- полезно для отладки логики hotplug-скриптов

### 3.2 led.sh

```sh
led.sh set status
led.sh clear status
```

Это низовой helper для LED-триггеров. В everyday-администрировании нужен редко, но полезен для диагностики board-specific логики.

### 3.3 board_detect и config_generate

```sh
board_detect
config_generate
```

Это уже не повседневные команды администратора, а board/bootstrap helpers:

- `board_detect` собирает сведения о плате
- `config_generate` генерирует базовый UCI-конфиг для свежей системы

Их не стоит запускать "на всякий случай" на уже настроенном устройстве без понимания последствий.

## 4. UCI: полный каталог основных команд

### 4.1 Что такое UCI на практике

UCI управляет файлами `/etc/config/*`. Это главный persistent-конфигурационный слой OpenWrt.

Примеры пакетов:

- `network`
- `wireless`
- `firewall`
- `dhcp`
- `system`
- `uhttpd`
- `fstab`

### 4.2 Главные подкоманды UCI

| Команда | Что делает | Пример |
|---------|------------|--------|
| `show [path]` | печатает конфиг | `uci show network` |
| `get path` | читает одно значение | `uci get system.@system[0].hostname` |
| `set path=value` | задает значение | `uci set system.@system[0].hostname='ax3000t'` |
| `add <config> <type>` | создает новую секцию | `uci add firewall rule` |
| `add_list path=value` | добавляет элемент списка | `uci add_list dhcp.lan.dhcp_option='6,1.1.1.1'` |
| `del_list path=value` | удаляет элемент списка | `uci del_list dhcp.lan.dhcp_option='6,1.1.1.1'` |
| `delete path` | удаляет секцию/опцию | `uci delete firewall.@rule[-1]` |
| `rename path=name` | переименовывает секцию/опцию | `uci rename network.@device[0]='brlan'` |
| `reorder path=pos` | меняет порядок секции | `uci reorder firewall.@rule[3]=0` |
| `changes [config]` | показывает неподтвержденные изменения | `uci changes firewall` |
| `commit [config]` | записывает изменения в файл | `uci commit firewall` |
| `revert [path]` | откатывает неподтвержденные изменения | `uci revert network` |
| `export [config]` | экспортирует в UCI-формате | `uci export wireless` |
| `import [config]` | импортирует конфиг | `uci import firewall` |
| `batch` | массовое применение команд | `uci batch <<'EOF' ... EOF` |

### 4.3 Важные опции UCI CLI

Из CLI полезно помнить:

- `-q` — quiet, меньше ругани при отсутствии ключей
- `-X` — без strict checks для extended syntax
- `-d <str>` — delimiter для list values при `show`
- `-f <file>` — работать с альтернативным файлом
- `-m` — merge при import

### 4.4 Named section и anonymous section

OpenWrt часто использует анонимные секции, например:

```sh
uci show system | grep system.@system
uci get system.@system[0].hostname
```

Именованные секции выглядят проще:

```sh
uci get network.lan.ipaddr
uci set network.lan.ipaddr='192.168.2.1'
```

### 4.5 Практические примеры UCI

Сменить hostname:

```sh
uci set system.@system[0].hostname='ax3000t-lab'
uci commit system
/etc/init.d/system reload
```

Изменить LAN IP:

```sh
uci set network.lan.ipaddr='192.168.2.1'
uci set network.lan.netmask='255.255.255.0'
uci commit network
/etc/init.d/network restart
```

Добавить DNS-опцию в DHCP:

```sh
uci add_list dhcp.lan.dhcp_option='6,1.1.1.1,1.0.0.1'
uci commit dhcp
/etc/init.d/dnsmasq restart
```

Смотреть только pending-изменения:

```sh
uci changes
uci changes network
```

### 4.6 Batch-режим

`uci batch` полезен для воспроизводимых изменений:

```sh
uci batch <<'EOF'
set network.lan.ipaddr='192.168.2.1'
set network.lan.netmask='255.255.255.0'
commit network
EOF
/etc/init.d/network restart
```

## 5. ubus: runtime API, RPC и события

### 5.1 Что такое ubus

`ubus` — это единая шина RPC и событий OpenWrt. Через нее общаются `procd`, `netifd`, `firewall`, `rpcd`, LuCI и другие системные компоненты.

Если `uci` отвечает на вопрос "что записано в конфиге?", то `ubus` часто отвечает на вопрос "что прямо сейчас происходит в системе?".

### 5.2 Главные подкоманды ubus CLI

| Команда | Назначение | Пример |
|---------|------------|--------|
| `list [path]` | перечислить объекты | `ubus list` |
| `call <path> <method> [json]` | вызвать RPC метод | `ubus call system board` |
| `subscribe <path>...` | подписаться на объекты | `ubus subscribe service` |
| `listen [type]...` | слушать события | `ubus listen` |
| `send <type> [json]` | отправить событие | `ubus send debug '{ "tag": "test" }'` |
| `wait_for <object>...` | ждать появления объекта | `ubus wait_for network.interface.lan` |
| `monitor` | низкоуровневый мониторинг шины | `ubus monitor` |

### 5.3 Полезные опции ubus CLI

- `-s <socket>` — другой ubus socket path
- `-t <seconds>` — timeout
- `-S` — simplified output
- `-v` — verbose
- `-m <type>` / `-M <r|t>` — фильтрация monitor output

### 5.4 Наиболее важные ubus-вызовы

Система:

```sh
ubus call system board
ubus call system info
ubus call system reboot
ubus call system watchdog '{ "frequency": 5 }'
ubus call system signal '{ "pid": 1234, "signum": 15 }'
```

Сервисы:

```sh
ubus call service list
ubus call service list '{ "verbose": true }'
ubus call service list '{ "name": "network", "verbose": true }'
```

Сеть:

```sh
ubus call network.interface dump
ubus call network.interface status '{ "interface": "lan" }'
ubus call network.device status '{ "name": "br-lan" }'
ubus call network reload
```

Wi-Fi:

```sh
ubus call network.wireless status
```

Прошивка:

```sh
ubus call system validate_firmware_image '{ "path": "/tmp/openwrt.bin" }'
```

Сразу важно: `sysupgrade` в конце работы сам опирается на `ubus call system sysupgrade`, так что ubus — это не только диагностика, но и реальный control API.

### 5.5 Слушать события и наблюдать систему

Для отладки очень полезны:

```sh
ubus listen
ubus monitor
```

Типовой кейс:

- открыть `ubus listen` в одной сессии
- в другой сделать `reload_config`, `ifup wan`, `wifi reload`
- смотреть, какие события реально улетают по шине

## 6. Связка UCI + ubus + сервисы: как OpenWrt применяет изменения

Рабочая модель в большинстве случаев такая:

1. Изменить UCI
2. Сделать `uci commit`
3. Дать системе сигнал о смене конфига или явно дернуть reload/restart
4. Проверить runtime через `ubus`, `ifstatus`, `wifi status`, `fw4 print`, `logread`

Пример для `dnsmasq`:

```sh
uci set dhcp.@dnsmasq[0].localservice='1'
uci commit dhcp
reload_config
service dnsmasq restart
logread -e dnsmasq
```

Пример для hostname:

```sh
uci set system.@system[0].hostname='filogic-lab'
uci commit system
reload_config
/etc/init.d/system reload
ubus call system board
```

## 7. advanced: procd shell API

Для повседневной эксплуатации роутера это не основной CLI, но для понимания OpenWrt полезно знать shell helper API `procd.sh`.

Ключевые helper-функции:

- `procd_open_service`
- `procd_close_service`
- `procd_open_instance`
- `procd_close_instance`
- `procd_set_param`
- `procd_append_param`
- `procd_add_jail`
- `procd_add_jail_mount`
- `procd_add_jail_mount_rw`
- `procd_add_reload_trigger`
- `procd_add_interface_trigger`
- `procd_add_raw_trigger`
- `procd_add_validation`
- `procd_running`
- `procd_kill`
- `procd_send_signal`
- `procd_set_config_changed`

Практический смысл:

- именно так многие штатные init-скрипты описывают, как запустить daemon
- поэтому `reload_config` и `config.change` имеют значение только там, где сервис это реально учитывает через `procd`

## 8. Выжимка

Если задача относится к системному управлению, почти всегда надо начать с такого минимума:

```sh
ubus call system board
service
uci changes
ubus call service list '{ "verbose": true }'
reload_config
logread -l 100
```

А дальше уже уходить в профильную подсистему: сеть, Wi-Fi, firewall, storage, packages или sysupgrade.

