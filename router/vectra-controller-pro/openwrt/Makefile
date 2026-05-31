include $(TOPDIR)/rules.mk

PKG_NAME:=vectra-controller-pro
# Versioned independently from vectra-controller-agent: this is a separate,
# additive controller package (xray-direct engine), not a replacement.
VECTRA_VERSION?=0.2.0
VECTRA_RELEASE?=1
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
  DEPENDS:=+ca-bundle +jsonfilter +jshn +procd +procd-ujail +uci +xray-core +kmod-nft-tproxy +kmod-nft-socket +kmod-nft-nat +dnsmasq-full
endef

define Package/$(PKG_NAME)/description
  Standalone Go controller that owns Xray end-to-end (config, process,
  firewall/TPROXY, DNS, subscriptions, geo, health) and polls the Vectra
  control plane directly, without PassWall2.
endef

define Package/$(PKG_NAME)/conffiles
/etc/config/vectra-controller-pro
endef

define Package/$(PKG_NAME)/postinst
#!/bin/sh

[ -n "$${IPKG_INSTROOT}" ] || { \
	if [ -x /etc/uci-defaults/90_vectra_controller_pro_defaults ]; then \
		/etc/uci-defaults/90_vectra_controller_pro_defaults >/dev/null 2>&1 || true; \
	fi; \
	if [ "$${VECTRA_SKIP_POSTINST_RESTART}" = "1" ] || [ -f /tmp/vectra-skip-postinst-restart ]; then \
		exit 0; \
	fi; \
	/etc/init.d/vectra-controller-pro enable >/dev/null 2>&1 || true; \
	if /etc/init.d/vectra-controller-pro running >/dev/null 2>&1; then \
		/etc/init.d/vectra-controller-pro restart >/dev/null 2>&1 || true; \
	else \
		/etc/init.d/vectra-controller-pro start >/dev/null 2>&1 || true; \
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
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/vectra-controller-pro $(1)/etc/init.d/vectra-controller-pro
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/etc/uci-defaults/90_vectra_controller_pro_defaults $(1)/etc/uci-defaults/90_vectra_controller_pro_defaults
	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) ./files/lib/upgrade/keep.d/vectra-controller-pro $(1)/lib/upgrade/keep.d/vectra-controller-pro
	$(INSTALL_DIR) $(1)/usr/libexec/vectra-controller-pro
	$(INSTALL_BIN) ./files/usr/libexec/vectra-controller-pro/render-xray-config.sh $(1)/usr/libexec/vectra-controller-pro/render-xray-config.sh
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) ./files/usr/sbin/vctl-xray-wrapper $(1)/usr/sbin/vctl-xray-wrapper
endef

$(eval $(call BuildPackage,$(PKG_NAME)))
