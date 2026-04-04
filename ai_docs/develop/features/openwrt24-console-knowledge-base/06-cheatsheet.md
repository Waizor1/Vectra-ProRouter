# OpenWrt 24.xx CLI Cheatsheet

Дата подготовки: 2026-04-04

Короткая шпаргалка. Если нужен контекст и объяснения, переходи в остальные файлы этой папки.

Если задача касается live write, `sysupgrade`, recovery или firmware safety на Filogic, сначала прочитай [08-filogic-recovery-write-safety.md](08-filogic-recovery-write-safety.md).

## 1. Кто мы и что это за железо

```sh
ubus call system board
ubus call system info
cat /etc/openwrt_release
opkg print-architecture
uname -m
cat /tmp/sysinfo/board_name
cat /tmp/sysinfo/model
```

## 2. Сервисы

```sh
service
service network restart
service firewall reload
service dnsmasq status
/etc/init.d/uhttpd enabled
/etc/init.d/firewall status
reload_config
```

## 3. UCI

```sh
uci show network
uci get network.lan.ipaddr
uci set network.lan.ipaddr='192.168.2.1'
uci add firewall rule
uci add_list dhcp.lan.dhcp_option='6,1.1.1.1'
uci del_list dhcp.lan.dhcp_option='6,1.1.1.1'
uci delete firewall.@rule[-1]
uci rename network.@device[0]='brlan'
uci reorder firewall.@rule[3]=0
uci changes
uci commit network
uci revert network
uci export wireless
```

## 4. ubus

```sh
ubus list
ubus -v list network.interface
ubus call system board
ubus call system info
ubus call service list '{ "verbose": true }'
ubus call network.interface dump
ubus call network.interface status '{ "interface": "lan" }'
ubus call network.device status '{ "name": "br-lan" }'
ubus call network.wireless status
ubus listen
ubus monitor
```

## 5. Сеть и bridge

```sh
ifup wan
ifdown wan
ifup -a
ifstatus lan
devstatus br-lan
ip link show
ip addr show
ip route show
bridge link show
bridge vlan show
ss -ltnup
```

## 6. Wi-Fi

```sh
wifi status
wifi up
wifi down
wifi reconf
wifi reload
wifi isup radio0
wifi config
iwinfo wlan0 info
iwinfo wlan0 assoclist
iwinfo wlan0 scan
iwinfo wlan0 freqlist
iwinfo wlan0 txpowerlist
iwinfo wlan0 countrylist
```

## 7. Firewall

```sh
fw4 print
fw4 check
fw4 reload
fw4 restart
fw4 zone lan
fw4 network wan
fw4 device br-lan
nft list ruleset
```

Не делать без крайней причины:

```sh
fw4 stop
fw4 flush
```

## 8. Пакеты и storage

```sh
opkg update
opkg list-installed
opkg list-upgradable
opkg info firewall4
opkg status dnsmasq
opkg files wpad-openssl
opkg search /etc/init.d/uhttpd
opkg install /tmp/package.ipk
block info
block detect
block mount
block remount
swapon -s
df -h
mount
```

## 9. Прошивка и backup

```sh
sysupgrade -l
sysupgrade -b /tmp/backup-$(date +%F).tar.gz
ubus call system validate_firmware_image '{ "path": "/tmp/openwrt.bin" }'
sysupgrade -T /tmp/openwrt.bin
sysupgrade /tmp/openwrt.bin
```

Сброс:

```sh
firstboot
reboot
```

## 10. Логи и аварийная диагностика

```sh
logread -l 100
logread -f
logread -e netifd
logread -e firewall
logread -e hostapd
logread -t -l 200
dmesg | tail -n 100
ps w
top
free -m
```

## 11. Filogic / AX3000T short path

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH|TARGET)' /etc/openwrt_release
opkg print-architecture
bridge vlan show
wifi status
iwinfo wlan0 info
fw4 check
sysupgrade -T /tmp/openwrt.bin
```

Правила:

- для OpenWrt `24.xx` использовать `opkg`, не `apk`
- для Filogic смотреть DSA/`bridge`, а не `swconfig`
- для прошивки всегда сначала подтверждать board и image compatibility
- для stock-layout AX3000T нельзя использовать `ubootmod` artifacts
