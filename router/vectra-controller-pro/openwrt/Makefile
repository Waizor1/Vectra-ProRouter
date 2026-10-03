include $(TOPDIR)/rules.mk

PKG_NAME:=vectra-controller-pro
# Versioned independently from vectra-controller-agent: this is a separate,
# additive controller package (xray-direct engine), not a replacement.
VECTRA_VERSION?=0.7.0
VECTRA_RELEASE?=13
PKG_VERSION:=$(VECTRA_VERSION)
PKG_RELEASE:=$(VECTRA_RELEASE)

PKG_LICENSE:=MIT
PKG_MAINTAINER:=Vectra
PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0
VECTRA_PRO_SOURCE_DIR?=$(CURDIR)/..

GO_PKG:=vectra-controller-pro
# Build the vctl entrypoint. GoPackage names the installed binary after the
# last path component of GO_PKG_BUILD_PKG, so the installed binary is
# /usr/sbin/vctl (NOT /usr/sbin/vectra-controller-pro). init.d invokes that
# exact path.
GO_PKG_BUILD_PKG:=vectra-controller-pro/cmd/vctl
GO_PKG_INSTALL_BIN_PATH:=/usr/sbin
GO_PKG_LDFLAGS_X:=main.Version=$(PKG_VERSION)-r$(PKG_RELEASE) main.runtimeVersion=$(PKG_VERSION)-r$(PKG_RELEASE)

include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk
include $(INCLUDE_DIR)/package.mk

define Package/$(PKG_NAME)
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Routing and Redirection
  TITLE:=Vectra Controller Pro (xray-direct)
  URL:=https://router.vectra-pro.net
  DEPENDS:=+ca-bundle +jsonfilter +jshn +procd +procd-ujail +uci +xray-core +kmod-nft-tproxy +kmod-nft-socket +kmod-nft-nat +dnsmasq-full +rpcd-mod-iwinfo +zram-swap
endef

define Package/$(PKG_NAME)/description
  Standalone Go controller that owns Xray end-to-end (config, process,
  firewall/TPROXY, DNS, subscriptions, geo, health) and polls the Vectra
  control plane directly, without PassWall2.
endef

define Package/$(PKG_NAME)/conffiles
/etc/config/vectra-controller-pro
endef

define Package/$(PKG_NAME)/prerm
#!/bin/sh

# Removing vctl must hand the router back to vectra-controller-agent.
# start_service() disables the legacy agent so the two never check in with the
# same identity; without this hook an `opkg remove` would leave the router with
# no controller at all — recoverable only with physical access, i.e. a site
# visit on a customer router. The init script's service_stopped() does the
# restore — rc.common's stop runs it once procd has killed vctl, and it waits
# for vctl and its xray to be gone — and is guarded by the marker it wrote, so
# an agent the operator disabled deliberately is never switched back on.
#
# stop_service() unloads the data plane (nft table + fwmark policy route)
# BEFORE that, which is why this hook runs `stop` rather than just `disable`:
# prerm still has /usr/libexec/vectra-controller-pro/ on disk, and a removal
# that left the ruleset loaded would strand the restored agent — it sets no
# SO_MARK, so the output chain's control-plane exemption does not cover it.
#
# AN UPGRADE IS NOT A REMOVAL. opkg runs the OLD package's prerm on every
# upgrade as well — `prerm upgrade <version>`, with PKG_UPGRADE=1 — and a stop
# here handed the router back for the seconds of the file swap (PassWall and
# the legacy agent up next to the outgoing xray), and killed the opkg of a
# vctl updating itself (update_controller runs it as vctl's own child). The
# new postinst restarts the running vctl instead: a restart is no hand-back.
#
# The router's tune (`vctl tune`) is put back here, on a removal only: what
# it changed and is still as it left it (its backup, /etc/vectra-controller-
# pro/tune-backup.json). Here, because the postrm runs once the package's
# files are gone, vctl with them; before the hand-back, which starts the
# stack that takes the router back.
[ -n "$${IPKG_INSTROOT}" ] || { \
	if [ "$${PKG_UPGRADE:-0}" = "1" ] || [ "$${1:-}" = "upgrade" ]; then exit 0; fi; \
	vctl="$${VCTL:-/usr/sbin/vctl}"; \
	if [ -x "$$vctl" ]; then "$$vctl" tune undo 2>&1 | logger -t vectra-controller-pro; fi; \
	/etc/init.d/vectra-controller-pro stop >/dev/null 2>&1 || true; \
	/etc/init.d/vectra-controller-pro disable >/dev/null 2>&1 || true; \
	exit 0; \
}
endef

define Package/$(PKG_NAME)/postinst
#!/bin/sh

# During a trial (`vectra on --trial`: /tmp/vectra-trial.json, the init
# script's TRIAL_FILE) vctl has no boot links on purpose — a reboot must give
# the router back — and an upgrade in the middle of one gives it none either.
#
# Once vctl runs, the router is tuned (`vctl tune apply`, internal/tune):
# zram, the kernel's memory settings, packet steering, flow offloading —
# what nobody set otherwise. One line per change goes to the log. A standby
# install, a start that gave the router back and a trial tune nothing.
[ -n "$${IPKG_INSTROOT}" ] || { \
	if [ -x /etc/uci-defaults/90_vectra_controller_pro_defaults ]; then \
		/etc/uci-defaults/90_vectra_controller_pro_defaults >/dev/null 2>&1 || true; \
	fi; \
	rm -f /tmp/luci-indexcache* 2>/dev/null; \
	rm -rf /tmp/luci-modulecache/ 2>/dev/null; \
	killall -HUP rpcd 2>/dev/null || true; \
	sysctl -q -p /etc/sysctl.d/90-vectra-controller-pro.conf >/dev/null 2>&1 || true; \
	if [ "$${VECTRA_SKIP_POSTINST_RESTART}" = "1" ] || [ -f /tmp/vectra-skip-postinst-restart ]; then \
		exit 0; \
	fi; \
	[ -f /tmp/vectra-trial.json ] || /etc/init.d/vectra-controller-pro enable >/dev/null 2>&1 || true; \
	if /etc/init.d/vectra-controller-pro running >/dev/null 2>&1; then \
		/etc/init.d/vectra-controller-pro restart >/dev/null 2>&1 || true; \
	else \
		/etc/init.d/vectra-controller-pro start >/dev/null 2>&1 || true; \
	fi; \
	vctl="$${VCTL:-/usr/sbin/vctl}"; \
	if [ -x "$$vctl" ] && /etc/init.d/vectra-controller-pro running >/dev/null 2>&1; then \
		"$$vctl" tune apply 2>&1 | logger -t vectra-controller-pro; \
	fi; \
	exit 0; \
}
endef

define Package/$(PKG_NAME)/postrm
#!/bin/sh

# The rpcd plugin and ACL are gone: let rpcd drop the "vectra" object and
# LuCI forget the menu entry. SIGHUP makes rpcd re-exec itself with its
# sessions kept, so nobody is logged out of LuCI.
#
# vectra.lan and my.vectra-pro.net go with the package (the uci-defaults
# script added them) — on a removal only. opkg runs the OLD package's postrm
# on every upgrade too, as `postrm upgrade <version>` with PKG_UPGRADE=1
# (libopkg/opkg_install.c), and an upgrade keeps the names. So does the
# dead-man's block in root's crontab.
# And the tune's own sysctl file and backup: the prerm's `vctl tune undo`
# took them already; whatever it could not goes here.
[ -n "$${IPKG_INSTROOT}" ] || { \
	rm -f /tmp/luci-indexcache* 2>/dev/null; \
	rm -rf /tmp/luci-modulecache/ 2>/dev/null; \
	killall -HUP rpcd 2>/dev/null || true; \
	if [ "$$1" != "upgrade" ] && [ "$${PKG_UPGRADE:-0}" != "1" ]; then \
		crontab="$${CRONTAB:-/etc/crontabs/root}"; \
		cron="$${CRON_INIT:-/etc/init.d/cron}"; \
		if grep -qxF '# >>> vectra-controller-pro (managed) >>>' "$$crontab" 2>/dev/null && \
			sed '/^# >>> vectra-controller-pro (managed) >>>$$/,/^# <<< vectra-controller-pro (managed) <<<$$/d' "$$crontab" >"$$crontab.vectra-controller-pro" && \
			mv "$$crontab.vectra-controller-pro" "$$crontab"; then \
			[ -x "$$cron" ] && "$$cron" running >/dev/null 2>&1 && "$$cron" restart >/dev/null 2>&1; \
		fi; \
		named=""; \
		for s in $$(uci -q -X show dhcp 2>/dev/null | sed -n "s/^dhcp\.\([^.=]*\)\.interface_name=.*/\1/p"); do \
			for n in vectra.lan my.vectra-pro.net; do \
				case " $$(uci -q get "dhcp.$$s.interface_name") " in \
				*" $$n "*) uci -q del_list "dhcp.$$s.interface_name=$$n" && named=1 ;; \
				esac; \
			done; \
		done; \
		if [ -n "$$named" ] && uci -q commit dhcp; then \
			dnsmasq="$${DNSMASQ_INIT:-/etc/init.d/dnsmasq}"; \
			[ -x "$$dnsmasq" ] && "$$dnsmasq" running >/dev/null 2>&1 && "$$dnsmasq" reload >/dev/null 2>&1; \
		fi; \
		rm -f "$${TUNECONF:-/etc/sysctl.d/95-vectra-tune.conf}" "$${TUNEBACKUP:-/etc/vectra-controller-pro/tune-backup.json}"; \
		memconf="$${MEMCONF:-/etc/sysctl.d/99-vectra-pro-memory.conf}"; \
		if grep -q '^# vectra-controller-pro:' "$$memconf" 2>/dev/null; then \
			rm -f "$$memconf"; \
			lowmem="$${LOWMEMCONF:-/etc/sysctl.d/99-vectra-lowmem.conf}"; \
			[ -f "$$lowmem" ] && sysctl -q -p "$$lowmem" >/dev/null 2>&1; \
		fi; \
	fi; \
	exit 0; \
}
endef

define Build/Prepare
	$(RM) -r $(PKG_BUILD_DIR)
	$(INSTALL_DIR) $(PKG_BUILD_DIR)
	$(INSTALL_DIR) $(PKG_BUILD_DIR)/.go_work/build
	[ -f "$(VECTRA_PRO_SOURCE_DIR)/go.mod" ]
	$(CP) $(VECTRA_PRO_SOURCE_DIR)/go.mod $(PKG_BUILD_DIR)/go.mod
	# vectra-controller-pro is stdlib-only and ships without go.sum; copy it
	# only if a future dependency introduces one.
	[ ! -f "$(VECTRA_PRO_SOURCE_DIR)/go.sum" ] || $(CP) $(VECTRA_PRO_SOURCE_DIR)/go.sum $(PKG_BUILD_DIR)/go.sum
	$(CP) $(VECTRA_PRO_SOURCE_DIR)/cmd $(PKG_BUILD_DIR)/cmd
	$(CP) $(VECTRA_PRO_SOURCE_DIR)/internal $(PKG_BUILD_DIR)/internal
endef

define Build/Compile
	$(call GoPackage/Build/Compile)
endef

define Build/InstallDev
endef

define Package/$(PKG_NAME)/install
	$(call GoPackage/Package/Install/Bin,$(1))
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/etc/config/vectra-controller-pro $(1)/etc/config/vectra-controller-pro
	$(INSTALL_DIR) $(1)/etc/sysctl.d
	$(INSTALL_DATA) ./files/etc/sysctl.d/90-vectra-controller-pro.conf $(1)/etc/sysctl.d/90-vectra-controller-pro.conf
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/vectra-controller-pro $(1)/etc/init.d/vectra-controller-pro
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/etc/uci-defaults/90_vectra_controller_pro_defaults $(1)/etc/uci-defaults/90_vectra_controller_pro_defaults
	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) ./files/lib/upgrade/keep.d/vectra-controller-pro $(1)/lib/upgrade/keep.d/vectra-controller-pro
	$(INSTALL_DIR) $(1)/usr/libexec/vectra-controller-pro
	$(INSTALL_BIN) ./files/usr/libexec/vectra-controller-pro/render-xray-config.sh $(1)/usr/libexec/vectra-controller-pro/render-xray-config.sh
	$(INSTALL_BIN) ./files/usr/libexec/vectra-controller-pro/dataplane-teardown.sh $(1)/usr/libexec/vectra-controller-pro/dataplane-teardown.sh
	# The takeover's watch: cron runs it every minute (the uci-defaults script
	# writes its crontab block), ntpd's hotplug on a clock step.
	$(INSTALL_BIN) ./files/usr/libexec/vectra-controller-pro/deadman.sh $(1)/usr/libexec/vectra-controller-pro/deadman.sh
	$(INSTALL_DIR) $(1)/etc/hotplug.d/ntp
	$(INSTALL_DATA) ./files/etc/hotplug.d/ntp/60-vectra-controller-pro $(1)/etc/hotplug.d/ntp/60-vectra-controller-pro
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) ./files/usr/sbin/vctl-xray-wrapper $(1)/usr/sbin/vctl-xray-wrapper
	# `vectra on|off|keep|status`: vctl power, for a person at the console.
	$(INSTALL_BIN) ./files/usr/sbin/vectra $(1)/usr/sbin/vectra
	# The router UI: the ubus object "vectra" (rpcd exec plugin + ACL) and the
	# LuCI page that renders it (menu entry, view, prebuilt app bundle).
	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) ./files/usr/libexec/rpcd/vectra $(1)/usr/libexec/rpcd/vectra
	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d
	$(INSTALL_DATA) ./files/usr/share/rpcd/acl.d/vectra-controller-pro.json $(1)/usr/share/rpcd/acl.d/vectra-controller-pro.json
	$(INSTALL_DIR) $(1)/usr/share/luci/menu.d
	$(INSTALL_DATA) ./files/usr/share/luci/menu.d/luci-app-vectra-controller-pro.json $(1)/usr/share/luci/menu.d/luci-app-vectra-controller-pro.json
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view/vectra
	$(INSTALL_DATA) ./files/www/luci-static/resources/view/vectra/app.js $(1)/www/luci-static/resources/view/vectra/app.js
	$(INSTALL_DIR) $(1)/www/luci-static/vectra
	$(INSTALL_DATA) ./files/www/luci-static/vectra/vectra-app.js $(1)/www/luci-static/vectra/vectra-app.js
	# Vectra's brand: the official wordmark (a mask) and Montserrat (SIL OFL 1.1,
	# Latin + Cyrillic subset, three weights, ~17 KB each).
	$(INSTALL_DATA) ./files/www/luci-static/vectra/vectra-wordmark.png $(1)/www/luci-static/vectra/vectra-wordmark.png
	$(INSTALL_DIR) $(1)/www/luci-static/vectra/fonts
	$(INSTALL_DATA) ./files/www/luci-static/vectra/fonts/montserrat-500.woff2 $(1)/www/luci-static/vectra/fonts/montserrat-500.woff2
	$(INSTALL_DATA) ./files/www/luci-static/vectra/fonts/montserrat-600.woff2 $(1)/www/luci-static/vectra/fonts/montserrat-600.woff2
	$(INSTALL_DATA) ./files/www/luci-static/vectra/fonts/montserrat-700.woff2 $(1)/www/luci-static/vectra/fonts/montserrat-700.woff2
	$(INSTALL_DATA) ./files/www/luci-static/vectra/fonts/OFL.txt $(1)/www/luci-static/vectra/fonts/OFL.txt
endef

$(eval $(call BuildPackage,$(PKG_NAME)))
