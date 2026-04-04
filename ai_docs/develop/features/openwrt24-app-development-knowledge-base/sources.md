# Источники и что по ним подтверждено

Дата подготовки: 2026-04-04

Эта база знаний опирается на официальные документы OpenWrt, официальные репозитории `openwrt` и `luci`, а также на OpenWrt-devel mailing list там, где нужен точный operational flow подписи feed.

## 1. Официальные страницы OpenWrt

| Источник | URL | Что использовано |
|----------|-----|------------------|
| OpenWrt 24.10 release page | <https://openwrt.org/releases/24.10/start> | базовая релизная линия для семейства 24.xx |
| Using the SDK | <https://openwrt.org/docs/guide-developer/toolchain/using_the_sdk> | позиционирование SDK как основного developer path для package build |
| Creating a package / developer guide start | <https://openwrt.org/docs/guide-developer/start> | вход в package development workflow |
| Package policies | <https://openwrt.org/docs/guide-developer/package-policies> | package metadata, versioning и policy expectations |
| Feeds | <https://openwrt.org/docs/guide-developer/feeds> | feed model и lifecycle package sources |
| Building a single package | <https://openwrt.org/docs/guide-developer/toolchain/single.package> | точечная сборка пакета |
| GUI Development with LuCI | <https://openwrt.org/docs/guide-developer/luci> | high-level LuCI development reference |
| UCI techref | <https://openwrt.org/docs/techref/uci> | модель persistent configuration |
| Init scripts techref | <https://openwrt.org/docs/techref/initscripts> | lifecycle init/service layer |
| Package signatures | <https://openwrt.org/docs/guide-user/security/signatures> | signed repository trust model |
| How to install packages | <https://openwrt.org/faq/how_to_install_packages> | opkg installation/update orientation |

## 2. OpenWrt `openwrt-24.10`: build system и package lifecycle

| Компонент | URL | Что подтверждено |
|-----------|-----|------------------|
| `include/package.mk` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/include/package.mk> | build lifecycle, `BuildPackage`, `USE_SOURCE_DIR`, default prepare/configure/compile/install flow |
| `include/package-defaults.mk` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/include/package-defaults.mk> | default package metadata behavior |
| `include/package-pack.mk` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/include/package-pack.mk> | packaging, `conffiles`, `postinst-pkg`, `.ipk` assembly, default hooks |
| `scripts/feeds` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/scripts/feeds> | `update`, `install`, `src-link`, local feed workflow |
| `scripts/ipkg-make-index.sh` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/scripts/ipkg-make-index.sh> | package repository index generation |
| `include/version.mk` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/include/version.mk> | version substitution patterns used in package/config templating |

## 3. Service/install/update behavior

| Компонент | URL | Что подтверждено |
|-----------|-----|------------------|
| `procd.sh` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/procd/files/procd.sh> | `procd_*` helper API and supported param types |
| `functions.sh` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/lib/functions.sh> | `default_postinst`, `default_prerm`, execution of `/etc/uci-defaults/*`, enable/start and disable/stop lifecycle |
| `uhttpd` package Makefile | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/network/services/uhttpd/Makefile> | real package example with `conffiles`, split packages, `postinst`, install sections |
| `uhttpd.init` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/network/services/uhttpd/files/uhttpd.init> | real `procd` init script pattern with config-driven command construction and reload triggers |
| `uci-defaults.sh` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/base-files/files/lib/functions/uci-defaults.sh> | board/default config helpers and shape of UCI-defaults ecosystem |

## 4. Package manager, feeds и trust path

| Компонент | URL | Что подтверждено |
|-----------|-----|------------------|
| `opkg` package Makefile | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/opkg/Makefile> | `conffiles`, `customfeeds.conf`, `opkg-key`, signature-check option |
| `opkg.conf` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/opkg/files/opkg.conf> | base opkg configuration path |
| `customfeeds.conf` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/opkg/files/customfeeds.conf> | intended custom feed syntax |
| `opkg-key` | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/opkg/files/opkg-key> | trusted key management and signature verification flow |
| `usign` package Makefile | <https://raw.githubusercontent.com/openwrt/openwrt/openwrt-24.10/package/system/usign/Makefile> | `usign` as OpenWrt signature utility |

## 5. LuCI app examples

| Компонент | URL | Что подтверждено |
|-----------|-----|------------------|
| `luci.mk` | <https://raw.githubusercontent.com/openwrt/luci/openwrt-24.10/luci.mk> | LuCI package build helper and package assembly rules |
| `luci-app-example/Makefile` | <https://raw.githubusercontent.com/openwrt/luci/openwrt-24.10/applications/luci-app-example/Makefile> | minimal LuCI package Makefile pattern |
| `luci-app-example form.js` | <https://raw.githubusercontent.com/openwrt/luci/openwrt-24.10/applications/luci-app-example/htdocs/luci-static/resources/view/example/form.js> | JS view pattern and UCI-backed form usage |
| `luci-app-example ACL` | <https://raw.githubusercontent.com/openwrt/luci/openwrt-24.10/applications/luci-app-example/root/usr/share/rpcd/acl.d/luci-app-example.json> | ACL structure for UCI/RPC access |
| `luci-app-example menu` | <https://raw.githubusercontent.com/openwrt/luci/openwrt-24.10/applications/luci-app-example/root/usr/share/luci/menu.d/luci-app-example.json> | JSON-based LuCI menu structure |

## 6. Official OpenWrt-devel references for repository signing flow

| Источник | URL | Что использовано |
|----------|-----|------------------|
| OpenWrt-devel patch discussing `usign -S` for `Packages.sig` | <https://lists.openwrt.org/pipermail/openwrt-devel/2019-September/024819.html> | подтверждение detached-signature flow для package index |
| OpenWrt-devel patch discussing `usign -G -s ... -p ...` | <https://lists.openwrt.org/pipermail/openwrt-devel/2020-November/032272.html> | подтверждение команды генерации локальной build key pair |

## 7. Что именно было извлечено из источников

### 7.1 Build system

- `BuildPackage` является стандартной точкой materialize package target
- `Package/<name>/install` обязателен для нормального package artifact
- `USE_SOURCE_DIR` подтвержден как supported fast local-source path

### 7.2 Install/upgrade lifecycle

- package build system генерирует default `postinst`/`prerm`
- `default_postinst` запускает `/etc/uci-defaults/*` и затем делает `uci commit`
- init scripts из `/etc/init.d/*` автоматически enable/start на fresh install
- upgrade path ведет себя отдельно от fresh install через `PKG_UPGRADE`

### 7.3 Feed publishing

- `scripts/ipkg-make-index.sh` строит `Packages`
- `customfeeds.conf` предназначен для объявления своих package feed'ов
- `opkg-key` управляет trusted keys и verification path

### 7.4 LuCI

- актуальный `openwrt-24.10` example показывает JS-based view pattern
- ACL и menu JSON являются first-class частями LuCI app package
- LuCI package logic поддерживается через `luci.mk`

