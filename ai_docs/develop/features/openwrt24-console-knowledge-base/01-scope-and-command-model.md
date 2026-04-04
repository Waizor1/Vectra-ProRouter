# OpenWrt 24.xx: границы и модель команд

Дата подготовки: 2026-04-04

## 1. Что здесь понимается под "управлением OpenWrt из консоли"

У OpenWrt нет одной-единственной "админ-команды". Управление разбито на несколько слоев:

| Слой | Основные команды | Что реально меняют / показывают |
|------|------------------|---------------------------------|
| Постоянная конфигурация | `uci` | Файлы в `/etc/config/*` |
| Runtime и RPC | `ubus` | Состояние сервисов, сети, Wi-Fi, system methods, события |
| Жизненный цикл сервисов | `service`, `/etc/init.d/*`, `reload_config` | Старт, стоп, reload, triggers |
| Сеть | `ifup`, `ifdown`, `ifstatus`, `devstatus` | Работа `netifd` и интерфейсов |
| Беспроводная часть | `wifi`, `iwinfo` | Радио, SSID, ассоциации, состояние радио-стека |
| Firewall | `fw4`, `nft` | Правила `firewall4` и результирующий `nftables` ruleset |
| Пакеты | `opkg` | Репозитории, установка, удаление, файлы пакетов |
| Storage и mount | `block`, `mount`, `df`, `swapon`, `swapoff` | Overlay, extroot, swap, fstab |
| Firmware lifecycle | `sysupgrade`, `firstboot` | Проверка образа, backup, flash, reset |
| Диагностика | `logread`, `dmesg`, `ip`, `bridge`, `ss`, `ps`, `top` | Быстрое понимание состояния системы |

Вся эта база знаний описывает именно эти слои.

## 2. Почему нельзя честно описать "все команды"

Если понимать задачу буквально, "все команды" на OpenWrt включают:

- полный набор BusyBox applets
- общелинуксовые утилиты ядра и userland
- все бинарники из каждого установленного пакета
- все CLI сторонних приложений

Это неоперабельная цель и плохая база знаний.

Поэтому рабочая граница здесь такая:

- покрываем control plane OpenWrt 24.xx
- покрываем штатные и общеупотребимые инструменты диагностики
- не смешиваем базовое администрирование OpenWrt со CLI произвольных приложений

## 3. Главная модель: config plane против runtime plane

Самая важная мысль при работе с OpenWrt:

- `uci` меняет постоянную конфигурацию
- `ubus` и сервисные обертки меняют или читают текущее runtime-состояние

Пример:

```sh
uci set network.lan.ipaddr='192.168.2.1'
uci commit network
```

После этого файл `/etc/config/network` уже изменен, но интерфейс еще не обязан автоматически перезапуститься. Дальше нужно отдельно инициировать применение изменений, например:

```sh
reload_config
/etc/init.d/network restart
```

Или, в зависимости от подсистемы:

```sh
wifi reload
fw4 reload
/etc/init.d/dnsmasq restart
```

## 4. Базовые пути и точки входа

| Путь / сущность | Роль |
|-----------------|------|
| `/etc/config/*` | постоянная конфигурация UCI |
| `/etc/init.d/*` | init-скрипты сервисов |
| `/etc/rc.common` | общая логика жизненного цикла init-скриптов |
| `/etc/hotplug.d/*` | реакция на события ядра и подсистем |
| `/sbin/ifup`, `/sbin/ifdown`, `/sbin/ifstatus`, `/sbin/devstatus` | тонкие обертки к `netifd` через `ubus` |
| `/sbin/wifi` | shell front-end для Wi-Fi control plane |
| `/sbin/sysupgrade` | безопасная штатная прошивка и backup/restore |
| `/sbin/firstboot` | reset overlay к дефолтному состоянию |
| `/sbin/block` | fstools, storage, fstab, extroot, swap |
| `/sbin/fw4` | CLI `firewall4` |
| `/usr/bin/ubus` | RPC/event bus клиент |
| `/sbin/uci` или `/usr/sbin/uci` | CLI конфигурационной подсистемы |

## 5. Что изменять через UCI, а что только смотреть низовыми командами

Есть полезное правило:

- Постоянные сетевые и firewall-настройки менять через `uci`
- Низовые инструменты `ip`, `bridge`, `nft` использовать главным образом для inspection, диагностики и проверки результата

Пример правильного мышления:

- не настраивать firewall вручную `nft add rule ...` как постоянный способ
- а задавать policy через `uci` в `/etc/config/firewall`, затем применять через `fw4 reload`

То же самое по сети:

- не считать `ip addr add ...` постоянной настройкой LAN
- а менять `network.lan.*` через `uci`

## 6. Практический порядок работы

### 6.1 Узнать, где мы вообще находимся

```sh
ubus call system board
ubus call system info
cat /etc/openwrt_release
opkg print-architecture
uname -m
```

### 6.2 Проверить, что сейчас происходит

```sh
service
ifstatus lan
wifi status
fw4 print
logread -l 100
```

### 6.3 Внести конфигурационные изменения

```sh
uci show network
uci set network.lan.ipaddr='192.168.2.1'
uci commit network
```

### 6.4 Применить и проверить

```sh
reload_config
ifstatus lan
logread -e netifd
```

## 7. Опасные команды и почему они опасны

| Команда | Риск |
|---------|------|
| `firstboot` | сброс overlay, потеря текущей конфигурации |
| `sysupgrade -F` | прошивка даже при провале проверок совместимости |
| `fw4 stop` / `fw4 flush` | можно временно обнулить защиту и потерять удаленный доступ |
| `opkg upgrade` без понимания зависимостей | возможен дрейф от штатного образа и неожиданные конфликты |
| прямые `nft add/delete rule` | изменения не переживут нормальный reload и могут конфликтовать с `fw4` |
| прямые `ip addr` / `ip route` изменения как "настройка" | это только runtime, после reload/reboot они исчезнут |

## 8. Особенности именно для Filogic-класса

Для Xiaomi AX3000T и похожих роутеров нужно сразу держать в голове:

- обычно используется DSA, а не `swconfig`
- архитектура пакетов для AX3000T ожидается как `aarch64_cortex-a53`
- Wi-Fi и switch стоит диагностировать через `ubus`, `iwinfo`, `bridge`, `ip`, `logread`
- sysupgrade и reset лучше делать строго штатными инструментами, а не ручной записью flash

Подробности вынесены в [05-filogic-ax3000t.md](05-filogic-ax3000t.md).

## 9. Минимальный operational-тезис

Если нужно запомнить только одну схему:

1. Идентификация платформы: `ubus call system board`
2. Конфиг: `uci`
3. Runtime и статус: `ubus`, `ifstatus`, `wifi status`, `fw4 print`
4. Применение: `reload_config`, `wifi reload`, `fw4 reload`, `/etc/init.d/<service> restart`
5. Диагностика: `logread`, `dmesg`, `ip`, `bridge`, `iwinfo`
6. Прошивка: `sysupgrade`
7. Сброс: `firstboot`

