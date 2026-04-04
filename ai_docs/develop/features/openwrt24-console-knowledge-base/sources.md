# Источники и что по ним подтверждено

Дата подготовки: 2026-04-04

Эта база знаний опирается на первичные источники: официальные страницы OpenWrt и исходники CLI/скриптов. В качестве практической реализации семейства `24.xx` взята ветка `openwrt-24.10`.

## 1. Официальные страницы OpenWrt

| Источник | URL | Что использовано |
|----------|-----|------------------|
| OpenWrt 24.10 release page | <https://openwrt.org/releases/24.10/start> | базовая релизная линия для семейства 24.xx |
| Документация по opkg | <https://openwrt.org/docs/guide-user/additional-software/opkg> | подтверждение, что для 24.10 и старше используется `opkg` |
| Документация по apk | <https://openwrt.org/docs/guide-user/additional-software/apk> | разграничение поколений `opkg` и `apk` |
| Sysupgrade technical reference | <https://openwrt.org/docs/techref/sysupgrade> | валидация image, backup/restore/test flags, общий upgrade pipeline |
| Failsafe and factory reset | <https://openwrt.org/docs/guide-user/troubleshooting/failsafe_and_factory_reset> | вход в failsafe, `mount_root`, reset/recovery behavior на `24.10+` |
| Device page Xiaomi AX3000T | <https://openwrt.org/inbox/toh/xiaomi/ax3000t> | board-specific ориентиры по AX3000T, TFTP/UART recovery и warning по hardware variants |
| OpenWrt 24.10.5 Filogic downloads | <https://downloads.openwrt.org/releases/24.10.5/targets/mediatek/filogic/> | подтверждение, что для AX3000T существуют отдельные stock-layout и `ubootmod` artifact families |
| OpenWrt 25.12.2 Filogic downloads | <https://downloads.openwrt.org/releases/25.12.2/targets/mediatek/filogic/> | подтверждение текущей published image family для AX3000T и `ubootmod` в новой линии |

## 2. OpenWrt branch openwrt-24.10: системные shell entrypoints

| Компонент | URL | Что подтверждено |
|-----------|-----|------------------|
| `rc.common` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/etc/rc.common> | стандартные действия init-скриптов и procd-расширения |
| `reload_config` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/procd/files/reload_config> | отправка `config.change` через `ubus call service event` |
| `service` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/procd/files/service> | обертка над `/etc/init.d/*`, listing и статус через ubus |
| `ifup` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/network/config/netifd/files/sbin/ifup> | `ifup -a`, `ubus call network reload`, вызов `network.interface up/down` |
| `ifdown` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/network/config/netifd/files/sbin/ifdown> | companion entrypoint для опускания logical interface через ту же netifd/ubus связку |
| `ifstatus` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/network/config/netifd/files/sbin/ifstatus> | проверка `network.interface.<iface>` и вызов `network.interface status` |
| `devstatus` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/network/config/netifd/files/sbin/devstatus> | вызов `network.device status` |
| `wifi` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/network/config/wifi-scripts/files/sbin/wifi> | поддерживаемые команды `config|up|down|reconf|reload|status|isup`, deprecated `detect` |
| `sysupgrade` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/sbin/sysupgrade> | полный набор ключей backup/test/flash и вызов `system sysupgrade` |
| `firstboot` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/sbin/firstboot> | то, что `firstboot` является оболочкой над `/sbin/jffs2reset` |
| `hotplug-call` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/sbin/hotplug-call> | ручной прогон `/etc/hotplug.d/<subsystem>` |
| `led.sh` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/sbin/led.sh> | `set`/`clear` LED helper |
| `board_detect` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/bin/board_detect> | board bootstrap logic |
| `config_generate` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/bin/config_generate> | генерация базового UCI-конфига |

## 3. UCI, ubus, firewall, Wi-Fi, storage, logging, packages

| Компонент | URL | Что подтверждено |
|-----------|-----|------------------|
| `uci` CLI | <https://raw.githubusercontent.com/openwrt/uci/master/cli.c> | набор подкоманд `show/get/set/add/delete/commit/revert/export/import/batch` и ключевые опции |
| `ubus` CLI | <https://raw.githubusercontent.com/openwrt/ubus/master/cli.c> | набор подкоманд `list/call/subscribe/listen/send/wait_for/monitor` и CLI options |
| `fw4` | <https://raw.githubusercontent.com/openwrt/firewall4/master/root/sbin/fw4> | команды `start/stop/flush/restart/reload/reload-sets/print/check/network/device/zone` |
| `iwinfo` CLI | <https://raw.githubusercontent.com/openwrt/iwinfo/master/iwinfo_cli.c> | `info/scan/txpowerlist/freqlist/assoclist/countrylist/htmodelist/phyname` |
| `block` / fstools | <https://raw.githubusercontent.com/openwrt/fstools/master/block.c> | `block info/detect/hotplug/autofs/extroot/mount/umount/remount`, swap operations |
| `logread` | <https://raw.githubusercontent.com/openwrt/ubox/master/log/logread.c> | CLI опции `-l/-e/-f/-t/...` |
| `opkg` CLI | <https://raw.githubusercontent.com/openwrt/opkg-lede/master/src/opkg-cl.c> | набор подкоманд установки, удаления, поиска, статуса, зависимостей |

## 4. procd: сервисная модель OpenWrt

| Компонент | URL | Что подтверждено |
|-----------|-----|------------------|
| `procd.sh` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/procd/files/procd.sh> | shell helper API `procd_open_service`, `procd_set_param`, triggers и validation |
| `procd` system ubus methods | <https://raw.githubusercontent.com/openwrt/procd/master/system.c> | `system board`, `system info`, `system reboot`, `system watchdog`, `system signal`, `system validate_firmware_image`, `system sysupgrade` |

## 5. Локальные первоисточники по Filogic upgrade/recovery

| Компонент | Локальный путь | Что подтверждено |
|-----------|----------------|------------------|
| `sysupgrade` | `openwrt-24.10-src/package/base-files/files/sbin/sysupgrade` | `-T`, `-F`, `-s`, backup flags, вызов `validate_firmware_image`, `ubus system sysupgrade` |
| `stage2` | `openwrt-24.10-src/package/base-files/files/lib/upgrade/stage2` | kill/switch-to-ramfs flow и вызов `platform_pre_upgrade` |
| `validate_firmware_image` | `openwrt-24.10-src/package/base-files/files/usr/libexec/validate_firmware_image` | signature check, metadata check и `platform_check_image` |
| `fwtool.sh` | `openwrt-24.10-src/package/base-files/files/lib/upgrade/fwtool.sh` | `REQUIRE_IMAGE_METADATA`, board/compat validation logic |
| `nand.sh` | `openwrt-24.10-src/package/base-files/files/lib/upgrade/nand.sh` | NAND sysupgrade containers, tar/ubi/fit handling, success/failure reboot behavior |
| Mediatek Filogic platform upgrade | `openwrt-24.10-src/target/linux/mediatek/filogic/base-files/lib/upgrade/platform.sh` | stock AX3000T vs `ubootmod` upgrade path, `xiaomi_initial_setup`, `platform_pre_upgrade` |
| Filogic image catalog | `openwrt-24.10-src/target/linux/mediatek/image/filogic.mk` | разные device definitions и artifact families для stock-layout и `ubootmod` AX3000T |
| `procd` sysupgrade handling | `procd-src/system.c`, `procd-src/sysupgrade.c` | parsing sysupgrade JSON/options и превращение options в `UPGRADE_OPT_*` |

## 6. Что именно было извлечено из исходников

### 6.1 Сервисы

- `service` без аргументов перечисляет init-скрипты и показывает enabled/running state
- `rc.common` задает стандартный lifecycle init-скриптов
- `reload_config` не "магически перезагружает все", а рассылает `config.change`

### 6.2 Сеть и Wi-Fi

- `ifup`/`ifdown`/`ifstatus`/`devstatus` являются ubus-frontends к `netifd`
- `wifi` сам по себе тоже является orchestration-оберткой, а не низовым драйверным CLI
- `wifi detect` в этом поколении признан deprecated внутри самого скрипта

### 6.3 Firewall

- `fw4 print` и `fw4 check` являются ключевыми безопасными командами перед применением правил
- `fw4` — штатная административная точка входа для firewall4 на OpenWrt 24.xx

### 6.4 Firmware lifecycle

- `sysupgrade` умеет backup, restore, list-backup, test-image и фактическую прошивку
- `firstboot` оборачивает reset overlay, а не "что-то мягкое и обратимое"
- на Filogic image metadata обязательна
- AX3000T stock layout и `ubootmod` являются разными upgrade families
- `xiaomi_initial_setup` относится к initramfs/layout-transition path, а не к обычному stock sysupgrade из установленной squashfs-системы

### 6.5 Пакеты и логирование

- `opkg` остается базовым пакетным менеджером для 24.xx
- `logread` имеет достаточно богатый CLI для live-tail, regex filtering и remote forwarding

## 7. Методологическое замечание

Все команды и выводы в этой папке:

- подтверждены по исходникам и официальным страницам
- описаны как operational knowledge
- не должны интерпретироваться как "все это уже прогонялось на живом Xiaomi AX3000T в данном workspace"

Если понадобится on-device runbook с реальными фактами конкретного роутера, его нужно делать как отдельный inventory/snapshot документ на основе вывода с устройства.
