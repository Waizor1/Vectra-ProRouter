# OpenWrt 24.xx: база знаний по управлению из консоли

Дата подготовки: 2026-04-04

Эта папка нужна как опорная база знаний для дальнейшей работы с OpenWrt из shell на роутерах класса Xiaomi AX3000T и близких платформах MediaTek Filogic. Акцент сделан не на LuCI, а на реальном контрольном плейне OpenWrt: `uci`, `ubus`, `procd`, `netifd`, `wifi`, `fw4`, `sysupgrade`, `block`, `opkg`, `logread`.

## Что именно изучено

- Ветка OpenWrt `openwrt-24.10` как конкретная и актуальная реализация семейства `24.xx`
- Исходники CLI и служебных скриптов:
  - `rc.common`
  - `service`
  - `reload_config`
  - `ifup`, `ifdown`, `ifstatus`, `devstatus`
  - `wifi`
  - `sysupgrade`
  - `firstboot`
  - `hotplug-call`
  - `led.sh`
  - `board_detect`
  - `config_generate`
  - `uci`
  - `ubus`
  - `fw4`
  - `iwinfo`
  - `block`
  - `logread`
  - `opkg`
  - `procd.sh`
  - `procd` system ubus methods
- Официальные страницы OpenWrt:
  - релизная ветка `24.10`
  - документация по `opkg`
  - документация по `apk` для сравнения поколений
  - device page Xiaomi AX3000T

Полный перечень источников приведен в [sources.md](sources.md).

## Что покрывает эта база

- Практическую модель управления OpenWrt 24.xx из консоли
- Полный каталог основных команд штатного control plane OpenWrt
- Разницу между постоянной конфигурацией и runtime-состоянием
- Реальные безопасные workflows: сеть, Wi-Fi, firewall, storage, пакеты, upgrade, диагностика
- Специфику Xiaomi AX3000T и похожих Filogic-роутеров

## Что сознательно не покрывается

- Не каталогизируется каждая утилита BusyBox и каждый Linux-инструмент в системе
- Не каталогизируются CLI сторонних пакетов вроде `mosquitto`, `tailscale`, `passwall2`, `adguardhome`, если они не входят в базовый OpenWrt control plane
- Не утверждается, что команды из этой папки были выполнены на реальном AX3000T в этом workspace

Иными словами: это не "список всех бинарников в `/bin`", а детальная карта именно тех команд, через которые OpenWrt обычно администрируют и которыми реально управляют его подсистемами.

## Главные выводы

- Для OpenWrt `24.xx` практическая базовая линия сейчас равна `24.10.x`
- Главная связка управления в OpenWrt: `uci` для конфигурации и `ubus` для runtime/RPC/event bus
- `/etc/init.d/*` и `service` управляют сервисами через `rc.common` и `procd`
- `ifup`/`ifdown`/`ifstatus`/`devstatus` и `wifi` являются тонкими shell-обертками над `ubus`
- `fw4` является штатной точкой управления firewall в OpenWrt 24.xx, а низовой движок правил — `nftables`
- Для OpenWrt `24.xx` пакетный менеджер по умолчанию — `opkg`, а не `apk`
- Для Filogic-класса нужно мыслить в терминах DSA, `bridge vlan`, `ubus`, `netifd`, а не `swconfig`

## Как читать эту папку

1. Начать с [06-cheatsheet.md](06-cheatsheet.md), если нужен краткий набор ежедневных команд
2. Прочитать [01-scope-and-command-model.md](01-scope-and-command-model.md), чтобы понимать архитектуру управления
3. Дальше идти в профильные разделы:
   - [02-system-services-uci-ubus.md](02-system-services-uci-ubus.md)
   - [03-network-firewall-wifi.md](03-network-firewall-wifi.md)
   - [04-storage-packages-upgrade-logs.md](04-storage-packages-upgrade-logs.md)
   - [05-filogic-ax3000t.md](05-filogic-ax3000t.md)
4. Если работа идет с живым роутером, сначала заполнить [07-router-intake-template.md](07-router-intake-template.md)
5. Если работа предполагает firmware, recovery, `sysupgrade`, `failsafe` или любые write-операции на Filogic-роутере, прочитать [08-filogic-recovery-write-safety.md](08-filogic-recovery-write-safety.md)
6. Если нужен структурированный индекс для автоматизации или будущих агентов, использовать [openwrt24-agent-index.json](openwrt24-agent-index.json)
7. При спорных вопросах или при необходимости доказать источник открыть [sources.md](sources.md)

## Структура папки

- [01-scope-and-command-model.md](01-scope-and-command-model.md) — границы исследования, архитектура control plane, модель "постоянная конфигурация против runtime"
- [02-system-services-uci-ubus.md](02-system-services-uci-ubus.md) — системная идентификация, сервисы, `rc.common`, `service`, `reload_config`, `uci`, `ubus`, hotplug и вспомогательные команды
- [03-network-firewall-wifi.md](03-network-firewall-wifi.md) — `netifd`, `ifup`, `ifstatus`, Wi-Fi, `iwinfo`, `fw4`, `nft`, сетевые и беспроводные workflows
- [04-storage-packages-upgrade-logs.md](04-storage-packages-upgrade-logs.md) — `block`, overlay, swap, `sysupgrade`, `firstboot`, `opkg`, `logread`, аварийная диагностика
- [05-filogic-ax3000t.md](05-filogic-ax3000t.md) — отдельный operational-профиль под Xiaomi AX3000T и похожие MT798x
- [06-cheatsheet.md](06-cheatsheet.md) — короткая рабочая шпаргалка
- [07-router-intake-template.md](07-router-intake-template.md) — шаблон сбора on-device фактов перед изменениями на живом роутере
- [08-filogic-recovery-write-safety.md](08-filogic-recovery-write-safety.md) — recovery matrix, write preflight, contact-loss decision tree и boundary между stock layout и `ubootmod`
- [openwrt24-agent-index.json](openwrt24-agent-index.json) — machine-readable navigation index для будущих агентов и автоматизации
- [sources.md](sources.md) — перечень источников и что именно по ним подтверждено

## Быстрый старт: минимальный срез команд

```sh
ubus call system board
ubus call system info
service
uci changes
ifstatus lan
devstatus br-lan
wifi status
fw4 check
logread -f
opkg list-installed
sysupgrade -l
```

## О чем помнить при любой работе

- Временные изменения смотрим и проверяем командами `ubus`, `ifstatus`, `wifi status`, `fw4 print`, `ip`, `bridge`, `logread`
- Постоянные изменения вносим через `uci` и commit
- После `uci commit` сервис не всегда перечитывает конфиг сам: иногда нужен `reload_config`, `fw4 reload`, `wifi reload`, `/etc/init.d/<service> reload` или restart
- Для Filogic не надо полагаться на старые инструкции под `swconfig`
- Перед прошивкой нужно подтверждать и board, и архитектуру, и совместимость образа
- Для AX3000T и похожих Filogic-устройств нельзя смешивать stock-layout images и `ubootmod` artifacts
