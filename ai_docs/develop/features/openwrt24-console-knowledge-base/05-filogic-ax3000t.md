# Xiaomi AX3000T и похожие роутеры на Filogic

Дата подготовки: 2026-04-04

## 1. Для чего нужен отдельный профиль Filogic

На уровне OpenWrt общие команды одинаковы, но у Filogic-класса есть несколько operational-особенностей:

- современная DSA-модель switching
- типичная архитектура пакетов `aarch64_cortex-a53`
- board-specific нюансы flash layout и sysupgrade
- Wi-Fi стек на базе `mac80211` / `mt76`

Для Xiaomi AX3000T официальная device page OpenWrt указывает платформу класса MT7981B / Cortex-A53, а в operational-плане это значит, что пакетную совместимость нужно проверять через реальные данные системы, а не только по названию модели.

## 2. Что нужно подтвердить на реальном устройстве

Минимальный набор:

```sh
ubus call system board
ubus call system info
grep -E 'DISTRIB_(RELEASE|ARCH|TARGET)' /etc/openwrt_release
opkg print-architecture
uname -m
cat /tmp/sysinfo/board_name
cat /tmp/sysinfo/model
```

Что мы ожидаем увидеть для AX3000T-класса:

- релиз OpenWrt линии `24.10.x`
- архитектуру пакетов `aarch64_cortex-a53`
- board/model, соответствующие Xiaomi AX3000T

Но именно "ожидаем" не равно "надо верить без проверки". Перед выбором пакетов и образов сначала подтверждаем on-device факты.

## 3. DSA вместо swconfig

Для Filogic-роутеров старая модель `swconfig` обычно уже неактуальна. Практический набор для switching и VLAN здесь такой:

```sh
uci show network | grep -E 'device|bridge|vlan'
ip -d link show
bridge link show
bridge vlan show
devstatus br-lan
ifstatus lan
```

Правильный operational-вывод:

- VLAN и bridge нужно смотреть через `bridge` и `network device` модель UCI
- логическую связность проверять через `ifstatus`
- инструкции "сделай через `swconfig dev switch0 ...`" для Filogic обычно нерелевантны

## 4. Wi-Fi на Filogic: что смотреть первым

```sh
wifi status
iwinfo wlan0 info
iwinfo wlan1 info
iwinfo wlan0 assoclist
iwinfo wlan1 assoclist
logread -e hostapd
logread -e mt76
logread -e wireless
```

Что особенно важно:

- реальный канал и ширина канала
- страна и regulatory domain
- список ассоциированных клиентов
- сообщения `hostapd`, `wpa_supplicant`, `mt76`

Если после правок UCI радио "не встало", рабочая последовательность такая:

```sh
uci changes wireless
uci commit wireless
wifi reload
wifi status
logread -e hostapd
```

## 5. Flash, прошивки и осторожность

### 5.1 Никогда не полагаться только на имя модели

Перед `sysupgrade` для AX3000T и похожих устройств нужны минимум эти проверки:

```sh
ubus call system board
opkg print-architecture
sysupgrade -l
ubus call system validate_firmware_image '{ "path": "/tmp/openwrt.bin" }'
sysupgrade -T /tmp/openwrt.bin
```

### 5.2 Чего не делать без крайней причины

- не использовать `mtd write` как обычный путь обновления
- не форсить `sysupgrade -F`, если board/image не совпали
- не переносить сломанный конфиг через обычный backup/restore "по инерции"
- не смешивать инструкции для NAND/UBI, dual-image и single-image устройств, если layout не подтвержден

### 5.3 Что смотреть в логах вокруг storage и boot

```sh
dmesg | grep -Ei 'ubi|ubifs|nand|spi|partition|fit'
logread -e sysupgrade
logread -e compat
df -h
mount
```

Это дает быструю картину:

- какой тип rootfs/overlay
- были ли замечания по image compatibility
- нет ли проблем с UBI/flash/storage

## 6. Практический снимок состояния перед любой серьезной операцией

Перед upgrade, reset, сетевой реконфигурацией или установкой большого пакета имеет смысл собрать минимум:

```sh
ubus call system board
ubus call system info
grep -E 'DISTRIB_(RELEASE|ARCH|TARGET)' /etc/openwrt_release
opkg print-architecture
ifstatus lan
ifstatus wan
wifi status
fw4 print
df -h
free -m
logread -l 100
```

Если нужно больше platform-specific деталей:

```sh
cat /proc/cpuinfo
ip -d link show
bridge vlan show
dmesg | tail -n 100
```

## 7. Рабочие правила именно для AX3000T-подобных устройств

1. Для выбора пакетов доверять `opkg print-architecture`, а не названию роутера в магазине.
2. Для управления switch/VLAN использовать DSA-путь: `bridge`, `ip`, `uci`, `ifstatus`.
3. Для Wi-Fi диагностики смотреть не только `wifi status`, но и `iwinfo` плюс `logread`.
4. Для прошивки опираться на `sysupgrade` и предварительную проверку образа.
5. При сомнительном наследованном конфиге рассматривать `sysupgrade -n`, а не перенос старых проблем в новую систему.

## 8. Минимальный safe-path для реального AX3000T

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH|TARGET)' /etc/openwrt_release
opkg print-architecture
sysupgrade -b /tmp/backup-ax3000t-$(date +%F).tar.gz
ubus call system validate_firmware_image '{ "path": "/tmp/openwrt.bin" }'
sysupgrade -T /tmp/openwrt.bin
```

Если все проверки зеленые, только тогда переходить к фактической прошивке или к установке board-specific пакетов.

