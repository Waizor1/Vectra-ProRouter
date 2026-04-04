# Service lifecycle, UCI и LuCI

Дата подготовки: 2026-04-04

## 1. Stateful package в OpenWrt состоит не только из бинарника

Если приложение должно жить как нормальный системный компонент, обычно нужны:

- исполняемый файл или скрипт
- `/etc/config/<name>` для persistent configuration
- `/etc/init.d/<name>` для service lifecycle
- `procd` triggers и respawn policy
- `conffiles`, чтобы конфиг переживал upgrade
- возможно `/etc/uci-defaults/*` для one-shot initial setup или schema migration
- отдельный `luci-app-*` пакет, если нужен web UI

## 2. Файл `/etc/config/<name>`: штатный способ хранить конфиг

Для OpenWrt это preferred path почти для любого системного пакета.

Пример минимального UCI-конфига:

```conf
config myapp 'main'
	option enabled '1'
	option listen_addr '0.0.0.0'
	option listen_port '8080'
	option log_level 'info'
```

Почему это важно:

- LuCI и shell tooling умеют работать с UCI из коробки
- сервис можно reload/restart на основе изменения package config
- конфиг легко мигрировать и сохранять при обновлениях

## 3. Init-скрипт и `procd`

Для OpenWrt `24.xx` правильный service lifecycle обычно строится через `rc.common` + `procd`.

Минимальный skeleton:

```sh
#!/bin/sh /etc/rc.common

USE_PROCD=1
START=95

start_service() {
	config_load myapp

	procd_open_instance
	procd_set_param command /usr/bin/myapp --config /etc/config/myapp
	procd_set_param respawn
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_set_param file /etc/config/myapp
	procd_close_instance
}

service_triggers() {
	procd_add_reload_trigger myapp
}
```

Это уже достаточно, чтобы пакет имел правильную точку входа в lifecycle OpenWrt.

## 4. Что дает `procd.sh`

По `procd.sh` ключевые helper-функции такие:

- `procd_open_service`
- `procd_close_service`
- `procd_open_instance`
- `procd_close_instance`
- `procd_set_param`
- `procd_append_param`
- `procd_add_reload_trigger`
- `procd_add_interface_trigger`
- `procd_add_validation`
- `procd_running`
- `procd_kill`
- `procd_send_signal`
- `procd_set_config_changed`

На практике самые частые `procd_set_param` для приложений:

- `command`
- `respawn`
- `file`
- `env`
- `user`
- `group`
- `pidfile`
- `stdout`
- `stderr`
- `reload_signal`

## 5. Критично важное поведение install/upgrade lifecycle

Это место обычно недооценивают.

По build-system source и `default_postinst`/`default_prerm` подтверждено следующее:

- если пакет ставит файл в `/etc/init.d/*`, default lifecycle считает его сервисом
- при обычной установке init-скрипт будет `enable` + `start`
- при upgrade сервис будет `stop`, затем после установки снова `start`
- при upgrade `enable` не дергается заново, если `PKG_UPGRADE=1`
- при remove штатный lifecycle делает `disable` + `stop`

Практический вывод:

- если пакет не должен автозапускаться при install, это надо проектировать осознанно
- lifecycle надо тестировать не только на fresh install, но и на upgrade/remove

## 6. `uci-defaults`: что они реально делают

`default_postinst` в OpenWrt явно исполняет скрипты из `/etc/uci-defaults/*`, если пакет их поставил.

Поведение такое:

- scripts запускаются на живой системе при install
- после успешного выполнения удаляются
- затем делается `uci commit`

Это делает `uci-defaults` хорошим инструментом для:

- первичного seed конфигурации
- одноразовых migration шагов
- включения новых секций/опций при schema change

Но это плохой инструмент для:

- долгоживущей runtime логики
- сложных daemon actions, которые лучше держать в service logic

## 7. Когда использовать `postinst-pkg`, а когда `uci-defaults`

Рабочее правило:

- `uci-defaults` — для конфигурационных one-shot шагов
- `postinst-pkg` — для package-specific install logic, не сводимой к UCI seed

Пример use-cases для `postinst-pkg`:

- перегенерация кеша
- conditionally triggered migration outside UCI
- сервисный reload после установки дополнительного plugin package

Официальный `uhttpd-mod-ubus` показывает именно такой pattern: отдельный `postinst` reload на живой системе.

## 8. `conffiles` и migrations нужно проектировать вместе

Если схема `/etc/config/myapp` эволюционирует, нужно заранее решить:

- что сохраняется как `conffiles`
- какие старые опции надо мигрировать
- какие новые default sections надо добавлять

Хорошая стратегия:

1. держать пользовательский конфиг в `conffiles`
2. новые поля добавлять через versioned `uci-defaults`
3. reload/restart сервиса делать после migration только там, где это реально нужно

## 9. LuCI: правильный pattern для OpenWrt 24.10

Для нового приложения лучше проектировать LuCI как отдельный пакет:

- `myapp` — core package
- `luci-app-myapp` — web UI

Почему так лучше:

- core можно ставить без web UI
- UI можно обновлять независимо
- dependencies остаются чище

## 10. Что показывает `luci-app-example`

Официальный `luci-app-example` на ветке `openwrt-24.10` подтверждает современный pattern:

- отдельный LuCI package Makefile через `luci.mk`
- JS-based view (`form.Map`, `TypedSection`, `Value`, `Flag`, `ListValue`)
- отдельный ACL JSON
- menu JSON

Ключевая мысль из example view:

- LuCI UI ожидает, что underlying UCI config уже существует
- значит core package или package install lifecycle должны положить базовый `/etc/config/<name>`

## 11. Minimal LuCI package anatomy

Типовой skeleton:

```text
luci-app-myapp/
|- Makefile
|- htdocs/luci-static/resources/view/myapp/form.js
\- root/
   \- usr/share/
      |- luci/menu.d/luci-app-myapp.json
      \- rpcd/acl.d/luci-app-myapp.json
```

### 11.1 Минимальный LuCI Makefile

```make
include $(TOPDIR)/rules.mk

LUCI_TITLE:=LuCI support for MyApp
LUCI_DEPENDS:=+luci-base +myapp
LUCI_PKGARCH:=all

PKG_LICENSE:=MIT
PKG_MAINTAINER:=Your Name <you@example.com>

include ../../luci.mk

$(eval $(call BuildPackage,luci-app-myapp))
```

### 11.2 Минимальный ACL JSON

```json
{
  "luci-app-myapp": {
    "description": "Grant UCI access to MyApp",
    "read": {
      "uci": [ "myapp" ]
    },
    "write": {
      "uci": [ "myapp" ]
    }
  }
}
```

### 11.3 Минимальный menu JSON

```json
{
  "admin/services/myapp": {
    "title": "MyApp",
    "order": 60,
    "action": {
      "type": "view",
      "path": "myapp/form"
    }
  }
}
```

## 12. Что обязательно тестировать в lifecycle

- Fresh install
- Service auto-enable / auto-start behavior
- Config file creation
- `uci-defaults` one-shot execution
- LuCI page opens and reads config
- Upgrade from previous package version
- Remove / reinstall

Если хотя бы одна из этих точек не проверена, пакет еще не готов к долгой жизни в feed.

