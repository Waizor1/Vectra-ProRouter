# Filogic и Xiaomi AX3000T: recovery, sysupgrade и write-safety

Дата подготовки: 2026-04-04

Цель этого документа: дать будущим агентам короткую, но доказуемую модель безопасной работы с живыми Filogic-роутерами, особенно с Xiaomi AX3000T, когда вопрос уже не в том, "какая есть команда", а в том, как не потерять устройство после записи, апдейта или сетевого изменения.

Документ опирается на:

- локальный sparse mirror OpenWrt `openwrt-24.10` в `openwrt-24.10-src/`
- локальный clone `procd` в `procd-src/`
- официальный device page AX3000T
- официальные страницы OpenWrt по `sysupgrade` и `failsafe`
- живой read-only inventory конкретного AX3000T из этого workspace

## 1. Что уже подтверждено первоисточниками

### По коду OpenWrt 24.10

- `sysupgrade` в OpenWrt остается shell entrypoint, который:
  - копирует image в `/tmp`
  - вызывает `/usr/libexec/validate_firmware_image`
  - отправляет JSON в `ubus call system sysupgrade`
- `validate_firmware_image` выполняет как минимум три класса проверок:
  - `fwtool` signature
  - image metadata / supported devices / compat version
  - `platform_check_image`
- В Mediatek Filogic включено `REQUIRE_IMAGE_METADATA=1`, поэтому отсутствие metadata считается ошибкой image validation
- Для stock-layout AX3000T upgrade-path идет через NAND upgrade с раздельными UBI-partitions:
  - `CI_KERN_UBIPART=ubi_kernel`
  - `CI_ROOT_UBIPART=ubi`
- Для `ubootmod` AX3000T это уже другой device definition и другой upgrade path:
  - `fit_do_upgrade`
  - `sysupgrade.itb`
  - `preloader.bin`
  - `bl31-uboot.fip`
  - опционально `initramfs-recovery.itb`

### По живому роутеру

- board: `xiaomi,mi-router-ax3000t`
- target: `mediatek/filogic`
- arch: `aarch64_cortex-a53`
- current kernel cmdline содержит `firmware=0`
- `fw_printenv` показывает stock dual-firmware style environment
- роутер не находится на `ubootmod` layout

### По официальным страницам OpenWrt

- device page AX3000T прямо разделяет:
  - stock bootloader / stock layout path
  - `OpenWrt U-Boot` / `ubootmod` path
- device page также публикует:
  - TFTP recovery hints для stock bootloader
  - UART recovery hints
  - предупреждение, что для всех аппаратных ревизий AX3000T в целом рекомендован `25.12`
- docs по failsafe подтверждают:
  - на `24.10+` окно для входа в failsafe составляет четыре секунды
  - failsafe поднимает `192.168.1.1`
  - DHCP и Wi-Fi в failsafe не поднимаются
  - для восстановления overlay используется `mount_root`

## 2. Главный safety-тезис

Для этого workspace нужно мыслить не "AX3000T вообще", а "какой exact layout и какой exact board уже стоит на устройстве сейчас".

Для живого роутера, уже исследованного здесь, baseline такой:

- использовать только stock-layout artifact family
- не трогать `ubootmod` artifacts
- не трогать bootloader
- не строить план восстановления на непроверенной remote-only надежде

Практический вывод:

- обычный safe image family для этого роутера: `openwrt-24.10.x-mediatek-filogic-xiaomi_mi-router-ax3000t-squashfs-sysupgrade.bin`
- запрещенный по умолчанию family:
  - `xiaomi_mi-router-ax3000t-ubootmod-*`
  - `*.itb` для `ubootmod`
  - `preloader.bin`
  - `bl31-uboot.fip`

## 3. Почему stock AX3000T и `ubootmod` нельзя смешивать

В `openwrt-24.10-src/target/linux/mediatek/image/filogic.mk` AX3000T описан двумя отдельными device definitions:

- `Device/xiaomi_mi-router-ax3000t`
- `Device/xiaomi_mi-router-ax3000t-ubootmod`

Это не просто разные file names. У них различается модель хранения и загрузки:

- stock-layout AX3000T:
  - `IMAGE/sysupgrade.bin := sysupgrade-tar | append-metadata`
  - обычный `squashfs-sysupgrade.bin`
  - initramfs factory image в `.ubi`
- `ubootmod` AX3000T:
  - `IMAGES := sysupgrade.itb`
  - `KERNEL_IN_UBI := 1`
  - `UBOOTENV_IN_UBI := 1`
  - отдельные bootloader artifacts
  - отдельный recovery artifact

Значит ошибка выбора family здесь означает не "не тот файл для той же схемы", а "другая схема layout и boot path".

Это критический boundary.

## 4. Что реально проверяет normal `sysupgrade`

По `openwrt-24.10-src/package/base-files/files/sbin/sysupgrade` и `openwrt-24.10-src/package/base-files/files/usr/libexec/validate_firmware_image`:

1. Image приводится к локальному пути в `/tmp`
2. Вызывается `validate_firmware_image`
3. Внутри validation идут:
   - `fwtool_check_signature`
   - `fwtool_check_image`
   - `platform_check_image`
4. Только после этого запрос уходит в `procd`

### Что это значит practically

- `sysupgrade -T <image>` нужно считать обязательной dry-run проверкой перед любым реальным flash
- `sysupgrade -F` является escape hatch, а не normal workflow
- если image metadata говорит, что compat version несовместима:
  - major mismatch блокирует upgrade
  - minor mismatch обычно требует `sysupgrade -n`

### Важная деталь про `-s`

На live router `sysupgrade -h` показывает `-s` как "stay on current partition (for dual firmware devices)".

В локально исследованном pipeline подтверждено следующее:

- `sysupgrade` пробрасывает `use_curr_part` в JSON для `ubus system sysupgrade`
- `procd` превращает option в env-style `UPGRADE_OPT_*`

Но в локально исследованных shell upgrade paths для `mediatek/filogic` прямой обработки `UPGRADE_OPT_USE_CURR_PART` не найдено.

Поэтому operational rule для будущих агентов:

- считать `-s` advanced board-dependent feature
- не строить safety strategy вокруг `-s`, пока ее поведение не подтверждено именно для нужного board/layout и именно на нужной версии

Иначе говоря, `-s` можно помнить, но нельзя считать доказанным механизмом защиты для этого AX3000T.

## 5. Когда OpenWrt реально трогает Xiaomi boot env

Это важное уточнение для future change planning.

В `openwrt-24.10-src/target/linux/mediatek/filogic/base-files/lib/upgrade/platform.sh` функция `xiaomi_initial_setup()`:

- форматирует `ubi` и `ubi_kernel`
- пишет Xiaomi-related boot env values
- пишет `mtdparts`

Но она начинается с проверки:

- `[ "$(rootfs_type)" = "tmpfs" ] || return 0`

А вызывается через `platform_pre_upgrade()`.

Практический вывод:

- normal sysupgrade из уже установленного squashfs-based OpenWrt не должен входить в опасную ветку Xiaomi initial setup
- эта ветка относится к initramfs / early factory-style / layout-transition сценарию
- повышенная опасность возникает не от "любого sysupgrade", а именно от:
  - initramfs/factory-like переходов
  - layout migrations
  - bootloader operations

Это снижает неопределенность для обычного package/service work, но не делает safe bootloader/layout writes.

## 6. AX3000T и совместимая версия OpenWrt: как понимать правильно

Официальный device page AX3000T на дату исследования говорит:

- для совместимости со всеми существующими hardware types AX3000T рекомендована линия `25.12`
- отдельно предупреждает про `RD03v2` на Qualcomm, который не поддерживается тем же способом
- отдельно отмечает, что `RD03` и `RD23` имеют одинаковое MediaTek hardware base

Практический вывод для этого workspace:

- нельзя автоматически переносить "AX3000T" как marketing name на любой future unit
- для нашего уже исследованного роутера совместимость доказана live-фактами:
  - `board_name=xiaomi,mi-router-ax3000t`
  - `mediatek/filogic`
  - `aarch64_cortex-a53`
  - stock layout
- значит для этого конкретного устройства допустимо планировать OpenWrt `24.10.x` stock sysupgrade image family
- но для "другого AX3000T из коробки" перед любым firmware advice нужно заново проверять hardware variant

Operational inference:

- для текущего live-router безопаснее мыслить как "уже подтвержденный MT7981 stock-layout unit"
- для будущих новых AX3000T устройств безопаснее мыслить как "нужно сперва исключить RD03v2/Qualcomm и layout variance"

## 7. Write preflight checklist перед любым non-read-only действием

1. Подтвердить wired access.
   Wi-Fi management через тот же роутер для сетевых изменений не допускается.
2. Снять fresh identity:
   - `ubus call system board`
   - `grep -E 'DISTRIB_(RELEASE|ARCH)' /etc/openwrt_release`
   - `opkg print-architecture`
   - `uname -m`
3. Подтвердить boot state:
   - `cat /proc/cmdline`
   - `fw_printenv | grep -E 'flag_|bootmenu|boot_fw|bootcmd'`
4. Зафиксировать backup surface:
   - `sysupgrade -l`
   - при явном разрешении: `sysupgrade -b /tmp/backup-YYYYMMDD.tar.gz`
5. Если речь о firmware image:
   - положить файл в `/tmp`
   - выполнить `sysupgrade -T /tmp/<image>`
   - отдельно сверить board/layout family
6. Если речь о firewall/network:
   - `fw4 print`
   - `fw4 check`
   - только потом reload/restart
7. Если речь о PassWall2 components:
   - сравнить `opkg list-installed`
   - сравнить self-reported versions binaries
8. Если речь о custom app:
   - сначала только `/tmp`
   - без service registration
   - без package-owned path replacement

## 8. Contact-loss decision tree

### Сценарий A: тест идет только из `/tmp` через local harness

Самый безопасный вариант.

Действия:

- ждать watchdog timeout
- потом `status` / `stop` / `cleanup`
- при необходимости power-cycle

Почему это приемлемо:

- `/tmp` volatile
- package DB не меняется
- init scripts не меняются
- после reboot persistent state остается прежним

### Сценарий B: сломан только конфиг или runtime, но OpenWrt still boots

Действия:

- попытаться вернуть доступ обычным SSH
- если есть shell: `uci revert`, ручной fix, либо controlled service stop
- если обычный доступ потерян: идти в failsafe

Failsafe facts по официальной docs:

- IP: `192.168.1.1`
- DHCP нет
- Wi-Fi нет
- на `24.10+` window четыре секунды
- после входа нужен `mount_root`

Дальше:

- править `/overlay/upper/etc/config/*`
- либо выполнять `firstboot && reboot`, если нужен clean reset

### Сценарий C: overlay настолько плох, что `mount_root` не работает

Официальная failsafe docs отмечает edge case с full overlay.

Тогда:

- проверить `df -h`
- если overlay забит и `mount_root` не поднимается, последняя software-only мера:
  - `mtd -r erase rootfs_data`

Это destructive action.
Использовать только как recovery operation, не как обычный reset ritual.

### Сценарий D: OpenWrt image/boot broken, но stock bootloader цел

Официальный AX3000T page документирует два семейства recovery:

- UART load path
- TFTP recovery path stock bootloader

Stock bootloader TFTP rule:

- роутер получает IP по DHCP
- затем просит файл, имя которого зависит от выданного IP в hex-форме
- recovery требует заранее подготовленный TFTP/DHCP host и физический доступ к reset/power cycle

Operational rule:

- считать это real fallback
- но не считать это remote rollback mechanism

### Сценарий E: сломан bootloader

Это уже не "router admin", а hardware recovery.

Официальный AX3000T page ссылается на:

- UART direct bootloader load path
- `mtk_uartboot`

Практический вывод:

- без physical teardown, UART и recovery host такой сценарий не считается закрытым

## 9. Safe escalation ladder для будущей разработки

Переходить к более опасному уровню только если предыдущий уже проверен.

### Level 0: read-only inventory

- SSH только на чтение
- сбор board/runtime/resource facts

### Level 1: `/tmp` loopback test

- app под `/tmp`
- bind на `127.0.0.1`
- высокий port
- watchdog timeout

### Level 2: `/tmp` LAN-visible test

- все еще без package install
- все еще без init script
- все еще без firewall/DNS takeover

### Level 3: package install без service enable

- package DB mutation уже допустима
- но сервис не автозапускается на boot
- rollback пока еще мягкий

### Level 4: service integration без сетевого перехвата

- init/procd lifecycle
- логирование
- restart/reload behavior

### Level 5: DNS/firewall/router-path integration

- самый поздний этап
- сюда относятся PassWall2-sensitive изменения
- без preflight и rollback plan сюда не переходить

## 10. Короткий список команд, которые нужно помнить

### Identity и layout

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH)' /etc/openwrt_release
opkg print-architecture
cat /proc/cmdline
fw_printenv | grep -E 'flag_|bootmenu|boot_fw|bootcmd'
```

### Backup и upgrade checks

```sh
sysupgrade -l
sysupgrade -b /tmp/backup.tar.gz
sysupgrade -T /tmp/openwrt.bin
```

### Firewall / network sanity

```sh
fw4 print
fw4 check
ifstatus lan
ip route show
ss -ltnup
```

### Failsafe recovery

```sh
mount_root
firstboot && reboot
```

Не использовать как routine command:

```sh
mtd -r erase rootfs_data
```

Это recovery-only destructive action.

## 11. Что считать недоказанным и не обещать

- не обещать, что `sysupgrade -s` гарантированно удержит текущий slot именно на этом board/layout
- не обещать, что stock TFTP recovery уже practically staged и проверен на этом unit
- не обещать, что remote-only firmware upgrade безопасен без local operator рядом с устройством
- не обещать, что любой AX3000T с рынка тождественен уже исследованному роутеру

## 12. Source map

### Локальные исходники

- `openwrt-24.10-src/package/base-files/files/sbin/sysupgrade`
- `openwrt-24.10-src/package/base-files/files/lib/upgrade/stage2`
- `openwrt-24.10-src/package/base-files/files/lib/upgrade/nand.sh`
- `openwrt-24.10-src/package/base-files/files/lib/upgrade/fwtool.sh`
- `openwrt-24.10-src/package/base-files/files/usr/libexec/validate_firmware_image`
- `openwrt-24.10-src/target/linux/mediatek/filogic/base-files/lib/upgrade/platform.sh`
- `openwrt-24.10-src/target/linux/mediatek/image/filogic.mk`
- `procd-src/system.c`
- `procd-src/sysupgrade.c`

### Официальные страницы

- <https://openwrt.org/docs/techref/sysupgrade>
- <https://openwrt.org/docs/guide-user/troubleshooting/failsafe_and_factory_reset>
- <https://openwrt.org/inbox/toh/xiaomi/ax3000t>
- <https://downloads.openwrt.org/releases/24.10.5/targets/mediatek/filogic/>
- <https://downloads.openwrt.org/releases/25.12.2/targets/mediatek/filogic/>

### Связанные локальные документы

- [README.md](README.md)
- [05-filogic-ax3000t.md](05-filogic-ax3000t.md)
- [07-router-intake-template.md](07-router-intake-template.md)
- [router-xiaomi-ax3000t-live-kb.md](../router-xiaomi-ax3000t-live-kb.md)
- [router-ax3000t-safe-test-harness.md](../router-ax3000t-safe-test-harness.md)
