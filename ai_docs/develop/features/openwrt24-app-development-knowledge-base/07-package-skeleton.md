# Package skeleton и стартовые шаблоны

Дата подготовки: 2026-04-04

Этот файл не заменяет предыдущие разделы, а дает быстрый стартовый skeleton, от которого удобно оттолкнуться при создании нового пакета.

## 1. Core package tree

```text
myapp/
|- Makefile
\- files/
   |- usr/bin/myapp
   |- etc/config/myapp
   |- etc/init.d/myapp
   \- etc/uci-defaults/99_myapp
```

## 2. Makefile skeleton

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
 My OpenWrt application.
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

## 3. `/usr/bin/myapp` skeleton

```sh
#!/bin/sh

echo "myapp started"
sleep infinity
```

Если это daemon wrapper, он должен либо сам удерживать процесс в foreground, либо вы должны запускать реальный foreground binary через init script.

## 4. `/etc/config/myapp` skeleton

```conf
config myapp 'main'
	option enabled '1'
	option listen_addr '0.0.0.0'
	option listen_port '8080'
	option log_level 'info'
```

## 5. `/etc/init.d/myapp` skeleton

```sh
#!/bin/sh /etc/rc.common

USE_PROCD=1
START=95

start_service() {
	config_load myapp

	local enabled
	config_get_bool enabled main enabled 1
	[ "$enabled" = "1" ] || return 0

	local addr port level
	config_get addr  main listen_addr '0.0.0.0'
	config_get port  main listen_port '8080'
	config_get level main log_level 'info'

	procd_open_instance
	procd_set_param command /usr/bin/myapp --listen "${addr}:${port}" --log-level "$level"
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

## 6. `/etc/uci-defaults/99_myapp` skeleton

```sh
#!/bin/sh

uci -q get myapp.main >/dev/null || {
	uci batch <<'EOF'
set myapp.main=myapp
set myapp.main.enabled='1'
set myapp.main.listen_addr='0.0.0.0'
set myapp.main.listen_port='8080'
set myapp.main.log_level='info'
EOF
}

uci commit myapp
exit 0
```

Этот script должен быть one-shot и idempotent.

## 7. LuCI package skeleton

### 7.1 Tree

```text
luci-app-myapp/
|- Makefile
|- htdocs/luci-static/resources/view/myapp/form.js
\- root/usr/share/
   |- luci/menu.d/luci-app-myapp.json
   \- rpcd/acl.d/luci-app-myapp.json
```

### 7.2 LuCI Makefile

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

### 7.3 `form.js`

```javascript
'use strict';
'require view';
'require form';

return view.extend({
	render: function() {
		let m, s, o;

		m = new form.Map('myapp', _('MyApp'));

		s = m.section(form.TypedSection, 'myapp', _('Main settings'));
		s.anonymous = true;

		o = s.option(form.Flag, 'enabled', _('Enabled'));
		o.rmempty = false;
		o.default = '1';

		o = s.option(form.Value, 'listen_addr', _('Listen address'));
		o.placeholder = '0.0.0.0';

		o = s.option(form.Value, 'listen_port', _('Listen port'));
		o.datatype = 'port';
		o.placeholder = '8080';

		return m.render();
	}
});
```

### 7.4 ACL JSON

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

### 7.5 Menu JSON

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

## 8. Стартовый checklist перед первым release

- Package собирается
- Core package ставится на роутер
- Service lifecycle работает
- `uci-defaults` не ломают повторную установку
- `conffiles` объявлены
- LuCI UI читает и пишет UCI
- Upgrade со старой версии протестирован
- Feed publication path готов

