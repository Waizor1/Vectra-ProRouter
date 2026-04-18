# OpenWrt 24.xx App Development Cheatsheet

Дата подготовки: 2026-04-04

## 1. Базовые правила

- для OpenWrt `24.xx` использовать `opkg` и `.ipk`
- для нового приложения default choice — matching SDK
- core package и `luci-app-*` лучше разделять
- первый live smoke test на AX3000T делать из `/tmp`, не через `opkg install`
- перед live package/service integration читать [08-filogic-recovery-write-safety.md](../openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md)
- конфиг держать в `/etc/config/<name>`
- stateful config объявлять в `conffiles`
- обновления выпускать через свой feed, а не через ручной copy в `/usr/bin`

## 2. Feed workflow

```sh
echo "src-link local /abs/path/to/my-feed" >> feeds.conf
./scripts/feeds update local
./scripts/feeds install -p local myapp
./scripts/feeds install -p local luci-app-myapp
```

## 3. Build loop

```sh
make menuconfig
make package/myapp/clean
make package/myapp/compile V=s
make package/luci-app-myapp/compile V=s
```

## 4. Package skeleton

```text
myapp/
|- Makefile
\- files/
   |- usr/bin/myapp
   |- etc/config/myapp
   |- etc/init.d/myapp
   \- etc/uci-defaults/99_myapp
```

## 5. Critical Makefile points

```make
PKG_NAME:=myapp
PKG_VERSION:=1.0.0
PKG_RELEASE:=1
PKGARCH:=all

define Package/myapp
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=My OpenWrt application
  DEPENDS:=+libubox +procd
endef

define Package/myapp/conffiles
/etc/config/myapp
endef

define Package/myapp/install
	...
endef

$(eval $(call BuildPackage,myapp))
```

## 6. Router install

```sh
scp bin/packages/<arch>/local/myapp_*.ipk root@router:/tmp/
ssh root@router opkg install /tmp/myapp_*.ipk
```

## 6a. Safe tmp test on live AX3000T before package install

```bash
python3 ./scripts/Manage-OpenWrtTmpProgramSession.py \
  --action start \
  --router-host <ip> \
  --router-user <user> \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519 \
  --local-path ./dist/myapp \
  --remote-command './myapp --listen 127.0.0.1:18080' \
  --listen-address 127.0.0.1 \
  --port 18080 \
  --duration-seconds 600
```

Native OpenSSH is now supported for macOS/Linux with `-Transport OpenSSH` plus `-OpenSshKnownHostsFile` and optional `-OpenSshIdentityFile`. The older PuTTY password lane remains available with `-RouterPassword` and `-RouterHostKey`.

Принцип:

- только `/tmp/codex-test/<session>`
- без package install
- без `/etc/init.d`
- без persistent config writes
- с watchdog auto-kill

## 7. Smoke checks

```sh
service myapp status
ubus call service list '{ "name": "myapp", "verbose": true }'
uci show myapp
logread -e myapp
```

## 8. Upgrade checks

```sh
opkg upgrade /tmp/myapp_new.ipk
uci show myapp
service myapp status
logread -e myapp
```

## 9. Feed publishing

```sh
./scripts/ipkg-make-index.sh /path/to/repo > /path/to/repo/Packages
gzip -9c /path/to/repo/Packages > /path/to/repo/Packages.gz
usign -G -s key-build -p key-build.pub -c "Local build key"
usign -S -m /path/to/repo/Packages -s key-build -x /path/to/repo/Packages.sig
```

## 10. Router feed setup

```conf
src/gz myfeed https://example.com/openwrt/myfeed
```

```sh
opkg-key add /tmp/key-build.pub
opkg update
opkg install myapp
```

## 11. Version bump rules

- upstream code changed -> bump `PKG_VERSION`
- only packaging changed -> bump `PKG_RELEASE`

## 12. High-risk mistakes

- не тестировать upgrade path
- не объявить `conffiles`
- смешать core и LuCI в один тяжеловесный пакет
- обновлять приложение ручной заменой файлов
- пропустить tmp-safe live test и сразу лезть в package/service integration на живом роутере
- публиковать feed без подписи и без ключевого management path
