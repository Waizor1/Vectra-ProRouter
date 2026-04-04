# Сеть, firewall и Wi-Fi в OpenWrt 24.xx

Дата подготовки: 2026-04-04

## 1. Сетевой слой OpenWrt: что чем управляет

Основная связка такая:

- постоянная конфигурация сети хранится в `/etc/config/network`
- runtime сетью управляет `netifd`
- shell-точки входа администратора — это `ifup`, `ifdown`, `ifstatus`, `devstatus`
- низовая инспекция делается через `ip`, `bridge`, `ss`

### Важный operational-принцип

- `uci` задает, как сеть должна выглядеть постоянно
- `ifstatus` и `devstatus` показывают, как она реально поднята сейчас
- `ip` и `bridge` дают самый низкий уровень фактического состояния

## 2. Команды netifd: ifup, ifdown, ifstatus, devstatus

### 2.1 ifup и ifdown

По исходнику:

- `ifup` является оболочкой над `ubus call network.interface`
- перед подъемом интерфейса `ifup` выполняет `ubus call network reload`
- `ifdown` использует ту же связку control plane для логического интерфейса, но опускает его вместо подъема
- поддерживается `ifup -a`, который проходит по всем `network.interface.*`

Примеры:

```sh
ifup wan
ifdown wan
ifup lan
ifup -a
```

Практика:

- `ifup wan` обычно эквивалентен мягкому переподъему конкретного logical interface
- `ifup -a` уместен для широкого переподъема интерфейсов после правок network-конфига

### 2.2 ifstatus

`ifstatus <iface>` проверяет существование `network.interface.<iface>` на ubus и затем вызывает:

```sh
ubus call network.interface status '{ "interface": "<iface>" }'
```

Примеры:

```sh
ifstatus lan
ifstatus wan
ifstatus wan6
```

Что искать в выводе:

- `up`
- `pending`
- `available`
- список `ipv4-address`
- список `ipv6-address`
- маршруты и DNS
- физический `device`

Пример фильтрации адреса:

```sh
ifstatus wan | jsonfilter -e '@["ipv4-address"][0].address'
```

### 2.3 devstatus

`devstatus <device>` передает имя устройства в:

```sh
ubus call network.device status '{ "name": "<device>" }'
```

Примеры:

```sh
devstatus br-lan
devstatus wan
devstatus eth0
```

Это полезно, когда нужен именно device-layer, а не logical interface.

## 3. Низовая сетевая диагностика

Это уже не OpenWrt-специфичные команды, но без них реальная диагностика почти невозможна.

```sh
ip link show
ip addr show
ip route show
ip route show table all
bridge link show
bridge vlan show
ss -ltnup
ping -c 3 1.1.1.1
ping -c 3 openwrt.org
```

Как их правильно использовать:

- `ifstatus` — проверить, что думает о себе `netifd`
- `ip` — увидеть фактические адреса и маршруты ядра
- `bridge vlan show` — особенно важен на DSA-платформах, включая Filogic
- `ss` — быстро понять, какие порты реально слушаются

## 4. Wi-Fi: команды и их реальный смысл

### 4.1 Основной CLI: wifi

По исходнику `wifi` поддерживает:

- `wifi config`
- `wifi up`
- `wifi down`
- `wifi reconf`
- `wifi reload`
- `wifi status`
- `wifi isup`

Также в коде есть `detect`, но она помечена как deprecated, и скрипт прямо просит использовать `wifi config`.

### 4.2 Практический смысл действий wifi

| Команда | Что делает |
|---------|------------|
| `wifi config` | генерирует `/etc/config/wireless` на основе обнаруженного оборудования |
| `wifi up [radio]` | включает радио/конкретное устройство |
| `wifi down [radio]` | выключает радио/конкретное устройство |
| `wifi reconf [radio]` | реконфигурирует Wi-Fi стек |
| `wifi reload [radio]` | вызывает `ubus call network reload` |
| `wifi status [radio]` | возвращает `network.wireless status` через ubus |
| `wifi isup [radio]` | код возврата показывает, поднято ли радио |

Примеры:

```sh
wifi status
wifi down
wifi up
wifi reconf
wifi reload
wifi isup radio0
wifi config
```

### 4.3 Реальная диагностика Wi-Fi

```sh
wifi status
iwinfo wlan0 info
iwinfo wlan0 assoclist
iwinfo wlan0 scan
logread -e hostapd
logread -e wpa_supplicant
logread -e wireless
```

Если в системе несколько радиомодулей:

```sh
iwinfo wlan0 info
iwinfo wlan1 info
```

### 4.4 iwinfo: основные команды

По CLI `iwinfo` особенно полезны:

- `iwinfo <device> info`
- `iwinfo <device> scan`
- `iwinfo <device> txpowerlist`
- `iwinfo <device> freqlist`
- `iwinfo <device> assoclist`
- `iwinfo <device> countrylist`
- `iwinfo <device> htmodelist`
- `iwinfo <backend> phyname <section>`

Примеры:

```sh
iwinfo wlan0 info
iwinfo wlan0 assoclist
iwinfo wlan0 scan
iwinfo wlan0 freqlist
iwinfo wlan0 txpowerlist
iwinfo wlan0 countrylist
iwinfo wlan0 htmodelist
iwinfo nl80211 phyname radio0
```

Что `iwinfo` дает лучше всего:

- фактический режим и канал
- link quality и signal
- список ассоциированных клиентов
- доступные страны, мощности и HT/HE режимы

## 5. Firewall: fw4 и nftables

### 5.1 Главный штатный CLI firewall4

По `fw4` доступны команды:

- `fw4 start`
- `fw4 stop`
- `fw4 flush`
- `fw4 restart`
- `fw4 reload`
- `fw4 reload-sets`
- `fw4 print`
- `fw4 check`
- `fw4 network <net>`
- `fw4 device <dev>`
- `fw4 zone <zone> [dev]`

### 5.2 Что из этого использовать чаще всего

Безопасный ежедневный набор:

```sh
fw4 print
fw4 check
fw4 reload
fw4 zone lan
fw4 network lan
fw4 device br-lan
```

Смысл:

- `fw4 print` — показать сгенерированный ruleset
- `fw4 check` — проверить конфиг и правила до применения
- `fw4 reload` — перечитать UCI и перезалить правила
- `fw4 zone` / `network` / `device` — быстро понять привязку zone/network/device

### 5.3 Опасные firewall-команды

```sh
fw4 stop
fw4 flush
```

Их нельзя использовать без понимания последствий:

- можно открыть роутер наружу
- можно потерять удаленный доступ
- можно разрушить диагностику, если потом сложно понять, какие правила были штатными

### 5.4 Практический workflow для firewall

```sh
uci show firewall
uci changes firewall
fw4 print
fw4 check
fw4 reload
logread -e firewall
```

Лучший порядок именно такой:

1. редактировать через `uci`
2. сначала `fw4 print`
3. затем `fw4 check`
4. только потом `fw4 reload`

### 5.5 nft как низовой слой

На OpenWrt 24.xx `fw4` управляет `nftables`.

Полезные команды инспекции:

```sh
nft list ruleset
nft list table inet fw4
```

Но важный принцип:

- руками редактировать `nft` допустимо только как временную низовую отладку
- постоянная политика должна жить в UCI и применяться через `fw4`

## 6. Типовые сетевые workflows

### 6.1 Изменить LAN IP безопасно

```sh
uci set network.lan.ipaddr='192.168.2.1'
uci set network.lan.netmask='255.255.255.0'
uci commit network
ifup lan
ifstatus lan
```

Если это удаленный роутер, сначала нужно продумать, как вы не потеряете доступ после смены адреса.

### 6.2 Проверить, что WAN реально поднят

```sh
ifstatus wan
devstatus wan
ip addr show
ip route show
ping -c 3 1.1.1.1
logread -e netifd
```

### 6.3 Перечитать Wi-Fi после правок UCI

```sh
uci changes wireless
uci commit wireless
wifi reload
wifi status
logread -e hostapd
```

### 6.4 Понять VLAN/bridge на DSA-платформе

```sh
uci show network | grep -E 'device|bridge|vlan'
bridge link show
bridge vlan show
devstatus br-lan
```

Это особенно актуально для Filogic-класса, где старые инструкции под `swconfig` больше не помогают.

## 7. Что почти всегда открыть первым при сетевой проблеме

```sh
ifstatus lan
ifstatus wan
wifi status
fw4 print
logread -l 100
bridge vlan show
ss -ltnup
```

Этого набора обычно достаточно, чтобы понять, проблема в конфиге, runtime, firewall, bridge или приложении.
