# OpenWrt 24.xx Router Intake Template

Дата подготовки: 2026-04-04

Назначение: собрать минимальный, но достаточный набор фактов с реального роутера перед изменениями сети, Wi-Fi, firewall, storage, пакетного состава или перед `sysupgrade`.

Статус: использовать как обязательный intake для Xiaomi AX3000T и похожих Filogic-роутеров, если работа идет не только по исходникам, а по живому устройству.

## 1. Карточка устройства

- Модель роутера:
- SoC:
- CPU / architecture:
- OpenWrt release:
- `DISTRIB_TARGET`:
- `DISTRIB_ARCH`:
- `board_name`:
- `rootfs_type`:
- Пакетный менеджер:
- Есть ли внешний storage/extroot:
- Есть ли резервное окно на перезагрузку:

## 2. Что именно нужно сделать

- Изменение сети:
- Изменение Wi-Fi:
- Изменение firewall:
- Установка / удаление пакетов:
- Прошивка / `sysupgrade`:
- Диагностика / triage:
- Нужен ли откатный план:

## 3. Команды для копипаста с роутера

Запустить на роутере и сохранить полный вывод:

```sh
echo '--- system board ---'
ubus call system board
echo '--- system info ---'
ubus call system info
echo '--- openwrt_release ---'
grep -E 'DISTRIB_(ID|RELEASE|REVISION|TARGET|ARCH|DESCRIPTION)' /etc/openwrt_release
echo '--- os-release ---'
grep -E 'OPENWRT_ARCH|NAME|VERSION' /usr/lib/os-release 2>/dev/null
echo '--- uname ---'
uname -a
uname -m
echo '--- service list ---'
service
echo '--- package manager ---'
opkg --version 2>/dev/null || true
apk --version 2>/dev/null || true
echo '--- architectures ---'
opkg print-architecture 2>/dev/null || true
echo '--- network dump ---'
ubus call network.interface dump
echo '--- ifstatus lan ---'
ifstatus lan 2>/dev/null || true
echo '--- ifstatus wan ---'
ifstatus wan 2>/dev/null || true
echo '--- devstatus br-lan ---'
devstatus br-lan 2>/dev/null || true
echo '--- wireless status ---'
wifi status 2>/dev/null || true
echo '--- iwinfo wlan0 ---'
iwinfo wlan0 info 2>/dev/null || true
echo '--- iwinfo wlan1 ---'
iwinfo wlan1 info 2>/dev/null || true
echo '--- firewall zone lan ---'
fw4 zone lan 2>/dev/null || true
echo '--- firewall zone wan ---'
fw4 zone wan 2>/dev/null || true
echo '--- firewall check ---'
fw4 check 2>/dev/null || true
echo '--- mount/df ---'
mount
df -h
echo '--- block info ---'
block info 2>/dev/null || true
echo '--- memory ---'
free -m
echo '--- listening sockets ---'
ss -ltnup 2>/dev/null || true
echo '--- logs tail ---'
logread -t -l 200
echo '--- dmesg tail ---'
dmesg | tail -n 120
```

## 4. Дополнительные команды по задаче

### 4.1 Если задача про bridge/VLAN/DSA

```sh
echo '--- network config relevant ---'
uci show network | grep -E 'device|interface|bridge|vlan'
echo '--- bridge link ---'
bridge link show
echo '--- bridge vlan ---'
bridge vlan show
echo '--- ip link detailed ---'
ip -d link show
```

### 4.2 Если задача про Wi-Fi

```sh
echo '--- wireless config ---'
uci show wireless
echo '--- assoclist wlan0 ---'
iwinfo wlan0 assoclist 2>/dev/null || true
echo '--- assoclist wlan1 ---'
iwinfo wlan1 assoclist 2>/dev/null || true
echo '--- hostapd logs ---'
logread -e hostapd
echo '--- mt76 logs ---'
logread -e mt76
```

### 4.3 Если задача про firewall и доступность

```sh
echo '--- firewall config ---'
uci show firewall
echo '--- fw4 print ---'
fw4 print
echo '--- routes ---'
ip route show
echo '--- all routes ---'
ip route show table all
```

### 4.4 Если задача про пакеты

```sh
echo '--- installed packages ---'
opkg list-installed 2>/dev/null || true
apk list -I 2>/dev/null || true
echo '--- upgradable packages ---'
opkg list-upgradable 2>/dev/null || true
echo '--- changed conffiles ---'
opkg list-changed-conffiles 2>/dev/null || true
```

### 4.5 Если задача про прошивку или rollback

```sh
echo '--- backup list ---'
sysupgrade -l
echo '--- validate image placeholder ---'
echo "После копирования образа в /tmp использовать:"
echo "ubus call system validate_firmware_image '{ \"path\": \"/tmp/openwrt.bin\" }'"
echo "sysupgrade -T /tmp/openwrt.bin"
```

## 5. Что обязательно зафиксировать в заметке после сбора

- Подтвержденный board и architecture
- Это точно OpenWrt `24.xx`, а не другая линия
- Используется `opkg` или уже `apk`
- Есть ли DSA/bridge/VLAN контекст
- Есть ли storage pressure на overlay
- Есть ли признаки нестабильности по `logread` / `dmesg`
- Можно ли безопасно делать reload/restart
- Нужен ли backup до изменений

## 6. Минимальный safe-pass перед серьезным изменением

Перед любым действием уровня `network`, `wireless`, `firewall`, `sysupgrade`:

```sh
ubus call system board
opkg print-architecture 2>/dev/null || true
ifstatus lan 2>/dev/null || true
wifi status 2>/dev/null || true
fw4 check 2>/dev/null || true
df -h
logread -l 100
```

## 7. Напоминания

- Для OpenWrt `24.xx` базовый пакетный путь — `opkg` + `.ipk`
- Для Filogic нужно мыслить через DSA и `bridge`, а не через `swconfig`
- Перед `sysupgrade` сначала проверяется board/image compatibility, потом только реальная прошивка
- `firstboot` и `sysupgrade -F` считаются destructive/high-risk действиями

