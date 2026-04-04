# Package layout и Makefile

Дата подготовки: 2026-04-04

## 1. Как обычно выглядит пакет OpenWrt

Минимальный skeleton userspace-пакета:

```text
myapp/
|- Makefile
\- files/
   |- etc/
   |  |- config/
   |  |  \- myapp
   |  \- init.d/
   |     \- myapp
   \- etc/
      \- uci-defaults/
         \- 99_myapp
```

Если приложение компилируется из исходников, добавляются:

```text
myapp/
|- Makefile
|- src/                # packaging helper files or bundled sources
\- files/
```

Если LuCI выносится отдельно:

```text
luci-app-myapp/
|- Makefile
|- htdocs/
|- root/
\- po/
```

## 2. Что обязательно должно быть в package Makefile

Из `package.mk` и официальных package examples видно, что нормальный пакет должен явно задавать хотя бы:

- `PKG_NAME`
- `PKG_VERSION`
- `PKG_RELEASE`
- `PKG_LICENSE`
- `PKG_MAINTAINER`
- `SECTION`
- `CATEGORY`
- `TITLE`
- `DEPENDS`, если есть runtime-зависимости
- `Package/<name>/install`

Без `install` package build-system просто не сможет собрать нормальный артефакт.

## 3. Базовый Makefile для script/service package

```make
include $(TOPDIR)/rules.mk

PKG_NAME:=myapp
PKG_VERSION:=1.0.0
PKG_RELEASE:=1
PKG_LICENSE:=MIT
PKG_MAINTAINER:=Your Name <you@example.com>
PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)-$(PKG_VERSION)
PKGARCH:=all

include $(INCLUDE_DIR)/package.mk

define Package/myapp
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=My OpenWrt application
  DEPENDS:=+libubox +procd
endef

define Package/myapp/description
 My OpenWrt application packaged for OpenWrt 24.xx.
endef

define Package/myapp/conffiles
/etc/config/myapp
endef

define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
endef

define Build/Compile
endef

define Package/myapp/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/usr/bin/myapp $(1)/usr/bin/myapp
	$(INSTALL_CONF) ./files/etc/config/myapp $(1)/etc/config/myapp
	$(INSTALL_BIN) ./files/etc/init.d/myapp $(1)/etc/init.d/myapp
	$(INSTALL_BIN) ./files/etc/uci-defaults/99_myapp $(1)/etc/uci-defaults/99_myapp
endef

$(eval $(call BuildPackage,myapp))
```

Этот шаблон хорош для shell/ucode/python-like wrapper пакетов, которые не требуют отдельного compile step.

## 4. Что означает `PKGARCH:=all`

Если пакет архитектурно независим, это правильная настройка.

Обычно это:

- shell scripts
- LuCI-only packages
- config packages
- data-only packages

Если внутри есть target binary, `PKGARCH:=all` ставить нельзя.

## 5. Makefile для нативного daemon-пакета

Для C/C++/Go/Rust-пакета packaging обычно выглядит ближе к официальным package examples:

- задается source origin
- используется подходящий helper, например `cmake.mk`
- есть `Build/Compile`
- есть install step в filesystem пакета

Признаки зрелого package recipe:

- pinned source version
- hash / integrity control
- четкое разделение build-time и runtime dependencies

## 6. Что означают `PKG_VERSION` и `PKG_RELEASE`

Это одна из главных точек для будущих обновлений.

Правило:

- `PKG_VERSION` меняется, когда меняется upstream application version
- `PKG_RELEASE` меняется, когда меняется packaging без изменения upstream version

Примеры bump logic:

- поменяли init-скрипт, `DEPENDS`, `conffiles`, install path: bump `PKG_RELEASE`
- поменяли исходники приложения на новую upstream версию: bump `PKG_VERSION`, затем начать `PKG_RELEASE` заново с `1`

## 7. `conffiles`: что должно переживать upgrade

Если пакет хранит админскую конфигурацию, ее нужно явно описать.

Пример:

```make
define Package/myapp/conffiles
/etc/config/myapp
/etc/myapp/custom.yaml
endef
```

Это связывает пакет с нормальным upgrade lifecycle, где пользовательская конфигурация не должна теряться при обычном обновлении пакета.

## 8. Runtime dependencies и split packages

OpenWrt packages очень часто разделяют на несколько подпакетов:

- `myapp` — core daemon
- `myapp-cli` — optional CLI
- `luci-app-myapp` — web UI
- `myapp-helper` — optional helpers

Это правильный pattern, потому что:

- не все устройства тянут UI
- core должен жить отдельно от frontend
- feed updates проще делать адресно

## 9. `BuildPackage` как точка сборки

В конце recipe должен быть:

```make
$(eval $(call BuildPackage,myapp))
```

Если пакетов несколько, `BuildPackage` вызывается для каждого.

Так это устроено и в официальных примерах вроде `uhttpd` или LuCI packages.

## 10. Полезные build helpers

В зависимости от типа проекта обычно подключают:

- `include $(INCLUDE_DIR)/package.mk`
- `include $(INCLUDE_DIR)/cmake.mk`
- `include $(INCLUDE_DIR)/autotools.mk`
- `include ../../luci.mk` для LuCI packages

Главная идея:

- не изобретать свою сборку поверх OpenWrt build system
- а выбирать штатный helper под build system приложения

## 11. Быстрый локальный цикл через `USE_SOURCE_DIR`

`package.mk` подтверждает advanced-путь через `USE_SOURCE_DIR`.

Это полезно, когда надо быстро итерироваться по локальным исходникам без упаковки нового source archive каждый раз.

Типовой смысл:

- recipe остается в feed
- исходники берутся из локальной директории
- compile loop ускоряется

Но для release recipe все равно должен оставаться reproducible без локальных абсолютных путей.

## 12. Чеклист хорошего package Makefile

- Есть стабильный `PKG_NAME`
- Правильно выставлены `PKG_VERSION` и `PKG_RELEASE`
- Есть `PKG_LICENSE` и `PKG_MAINTAINER`
- Есть корректные `DEPENDS`
- Есть `Package/<name>/install`
- Есть `conffiles`, если пакет stateful
- Нет лишнего `PKGARCH:=all` для binary package
- Пакет готов к install/remove/upgrade, а не только к compile

