# Storage, пакеты, sysupgrade и логи

Дата подготовки: 2026-04-04

## 1. Storage и overlay: что важно понимать

На OpenWrt крайне важно различать:

- базовую read-only систему
- overlay, куда ложатся изменения
- внешние накопители, если есть extroot или отдельные mountpoints

Минимальный набор команд для первичной картины:

```sh
mount
df -h
ubus call system board
```

Особенно смотри:

- `rootfs_type`
- сколько места осталось на overlay
- куда примонтированы USB/extroot/swap

## 2. block и fstools

### 2.1 Команды block

По `fstools` доступны:

- `block info`
- `block detect`
- `block hotplug`
- `block autofs`
- `block extroot`
- `block mount`
- `block umount`
- `block remount`

Примеры:

```sh
block info
block detect
block mount
block umount
block remount
```

Практический смысл:

- `block info` — увидеть блочные устройства и что о них знает система
- `block detect` — сгенерировать шаблон под `fstab`
- `block mount` — смонтировать описанное в конфиге
- `block umount` — размонтировать
- `block remount` — пересобрать mount state
- `block extroot` — логика extroot

### 2.2 Swap

По тому же коду доступны:

```sh
swapon -s
swapon -a
swapon -p 10 /dev/sda2
swapoff -a
swapoff /dev/sda2
```

На роутерах swap используют осторожно. Это не бесплатная память, а компромисс ради устойчивости, особенно если swap лежит на flash.

## 3. Пакетный менеджер OpenWrt 24.xx: opkg

### 3.1 Главное правило поколения 24.xx

Для OpenWrt `24.xx` штатный пакетный менеджер — `opkg`.

`apk` — это уже линия более новых поколений и не должен использоваться как дефолтный совет для 24.xx.

### 3.2 Главные команды opkg

По CLI `opkg` практическое ядро такое:

- `opkg update`
- `opkg upgrade <pkg>`
- `opkg install <pkg>`
- `opkg configure <pkg>`
- `opkg remove <pkg|regexp>`
- `opkg flag <flag> <pkg>`
- `opkg list`
- `opkg list-installed`
- `opkg list-upgradable`
- `opkg list-changed-conffiles`
- `opkg files <pkg>`
- `opkg search <file|regexp>`
- `opkg find <regexp>`
- `opkg info [pkg|regexp]`
- `opkg status [pkg|regexp]`
- `opkg download <pkg>`
- `opkg compare-versions <v1> <op> <v2>`
- `opkg print-architecture`
- `opkg depends`
- `opkg whatdepends`
- `opkg whatdependsrec`
- `opkg whatrecommends`
- `opkg whatsuggests`
- `opkg whatprovides`
- `opkg whatconflicts`
- `opkg whatreplaces`

### 3.3 Что использовать чаще всего

```sh
opkg update
opkg list-installed
opkg list-upgradable
opkg info dnsmasq-full
opkg status firewall4
opkg files wpad-openssl
opkg search /etc/init.d/uhttpd
opkg install /tmp/package.ipk
opkg remove adblock
opkg print-architecture
```

### 3.4 Осторожно с opkg upgrade

`opkg upgrade` на OpenWrt нужно использовать осмысленно.

Почему:

- можно получить partial-upgrade и уйти от согласованного состояния образа
- некоторые обновления безопаснее делать через полноценный `sysupgrade`
- критичные пакеты, связанные с libc, kernel modules или firmware image composition, могут требовать более аккуратного пути

Практический safe-path:

- обновить feeds через `opkg update`
- изучить `opkg list-upgradable`
- выборочно обновлять конкретные пакеты при понимании последствий
- при крупных изменениях платформы использовать `sysupgrade`

## 4. sysupgrade: штатная прошивка и backup

### 4.1 Главные опции sysupgrade

По скрипту `sysupgrade` подтверждены:

- `-i` — interactive
- `-v` — more verbose
- `-q` — less verbose
- `-n` — не сохранять конфиг
- `-c` — пытаться сохранить измененные файлы из `/etc`
- `-o` — пытаться сохранить измененные файлы из `/`
- `-p` — не восстанавливать partition table
- `-k` — включить в backup список установленных пакетов
- `-u` — пропускать файлы backup, совпадающие с `/rom`
- `-b`, `--create-backup` — создать backup archive
- `-r`, `--restore-backup` — восстановить backup archive
- `-l`, `--list-backup` — перечислить, что войдет в backup
- `-f` — подать config archive для восстановления во время прошивки
- `-s` — остаться на текущем firmware partition на dual-image устройствах
- `-F`, `--force` — прошить даже при провале проверки совместимости
- `-T`, `--test` — проверить image и config archive без реальной прошивки
- `--ignore-minor-compat-version` — игнорировать minor compat mismatch

### 4.2 Безопасный workflow перед прошивкой

```sh
ubus call system board
opkg print-architecture
sysupgrade -l
sysupgrade -b /tmp/openwrt-backup-$(date +%F).tar.gz
ubus call system validate_firmware_image '{ "path": "/tmp/openwrt.bin" }'
sysupgrade -T /tmp/openwrt.bin
```

И только затем:

```sh
sysupgrade /tmp/openwrt.bin
```

### 4.3 Когда использовать `-n`

```sh
sysupgrade -n /tmp/openwrt.bin
```

Это правильно, когда:

- переход между сильно разными конфигурационными состояниями
- есть сомнение, что старый конфиг сломан
- устройство уже "грязное" и перенос старого overlay только перенесет проблемы

### 4.4 Когда использовать `-k`

```sh
sysupgrade -k -b /tmp/backup-with-pkgs.tar.gz
```

Опция полезна, если нужно сохранить список установленных пакетов. Но это не равнозначно гарантированному успешному восстановлению всего программного окружения на новой прошивке.

### 4.5 Опасные варианты sysupgrade

```sh
sysupgrade -F /tmp/openwrt.bin
sysupgrade --ignore-minor-compat-version /tmp/openwrt.bin
```

Использовать их без очень ясной причины нельзя.

Для Filogic-класса особенно важно:

- не прошивать image от другого board
- не путать factory/initramfs/sysupgrade образы
- не исходить только из маркетингового имени роутера

## 5. ubus и sysupgrade

`sysupgrade` внутри опирается на:

```sh
ubus call system validate_firmware_image '{ "path": "/tmp/openwrt.bin" }'
ubus call system sysupgrade '{ ... }'
```

Практический вывод:

- `ubus` — это не только диагностика, а и фактический backend upgrade-механизма
- вручную дергать `system sysupgrade` напрямую обычно не нужно: для административной работы безопаснее использовать штатный `sysupgrade`

## 6. firstboot: reset к чистому состоянию

По коду `firstboot` — это оболочка над:

```sh
/sbin/jffs2reset
```

Практически это означает:

- сброс overlay
- возврат к состоянию "как после первой загрузки" для пользовательской части
- потерю текущей конфигурации, если она не сохранена отдельно

Пример:

```sh
firstboot
reboot
```

Это destructive-операция. На рабочем роутере сначала нужен backup.

## 7. logread и практическая диагностика

### 7.1 Главные опции logread

По `logread` подтверждены опции:

- `-s <path>` — ubus socket
- `-l <count>` — последние N сообщений
- `-e <pattern>` — regexp filter
- `-r <server> <port>` — remote stream
- `-F <file>` — log file
- `-S <bytes>` — log size
- `-p <file>` — PID file
- `-h <hostname>` — hostname для remote logging
- `-P <prefix>` — prefix
- `-z <facility>` — включить facility
- `-Z <facility>` — исключить facility
- `-f` — follow
- `-u` — UDP
- `-t` — extra timestamp
- `-0` — NUL trailer over TCP

### 7.2 Ежедневные шаблоны logread

```sh
logread -l 100
logread -f
logread -e netifd
logread -e dnsmasq
logread -e firewall
logread -e hostapd
logread -t -l 200
```

### 7.3 Чем дополнять logread

```sh
dmesg
dmesg | tail -n 50
ps w
top
free -m
df -h
```

Эти команды не заменяют `logread`, а дополняют его:

- `logread` — userland/system log buffer
- `dmesg` — kernel ring buffer
- `ps`/`top` — процессы и CPU
- `free`/`df` — память и storage pressure

## 8. Аварийный набор команд

Если роутер "ведет себя странно", первым делом:

```sh
ubus call system info
logread -l 200
dmesg | tail -n 100
df -h
free -m
mount
block info
opkg list-changed-conffiles
```

Этого обычно достаточно, чтобы быстро понять, проблема в памяти, overlay, пакетах, монтировании или недавних правках конфига.

