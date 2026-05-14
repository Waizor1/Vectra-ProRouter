include $(TOPDIR)/rules.mk

PKG_NAME:=vectra-controller-agent
VECTRA_VERSION?=0.1.13
VECTRA_RELEASE?=23
PKG_VERSION:=$(VECTRA_VERSION)
PKG_RELEASE:=$(VECTRA_RELEASE)

PKG_LICENSE:=MIT
PKG_MAINTAINER:=Vectra
PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0
VECTRA_AGENT_SOURCE_DIR?=$(CURDIR)/..

GO_PKG:=vectra-controller-agent
GO_PKG_BUILD_PKG:=vectra-controller-agent/cmd/vectra-controller-agent
GO_PKG_INSTALL_BIN_PATH:=/usr/sbin

include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk
include $(INCLUDE_DIR)/package.mk

define Package/$(PKG_NAME)
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Routing and Redirection
  TITLE:=Vectra outbound controller agent
  URL:=https://router.vectra-pro.net
  DEPENDS:=+ca-bundle +jsonfilter +jshn +procd +procd-ujail +uci
endef

define Package/$(PKG_NAME)/description
  Outbound HTTPS polling agent for the Vectra PassWall2 control plane.
endef

define Package/$(PKG_NAME)/conffiles
/etc/config/vectra-controller
endef

define Package/$(PKG_NAME)/postinst
#!/bin/sh

[ -n "$${IPKG_INSTROOT}" ] || { \
	if [ -x /etc/uci-defaults/90_vectra_controller_defaults ]; then \
		/etc/uci-defaults/90_vectra_controller_defaults >/dev/null 2>&1 || true; \
	fi; \
	if [ "$${VECTRA_SKIP_POSTINST_RESTART}" = "1" ] || [ -f /tmp/vectra-skip-postinst-restart ]; then \
		exit 0; \
	fi; \
	/etc/init.d/vectra-controller enable >/dev/null 2>&1 || true; \
	if /etc/init.d/vectra-controller running >/dev/null 2>&1; then \
		/etc/init.d/vectra-controller restart >/dev/null 2>&1 || true; \
	else \
		/etc/init.d/vectra-controller start >/dev/null 2>&1 || true; \
	fi; \
	exit 0; \
}
endef

define Build/Prepare
	$(RM) -r $(PKG_BUILD_DIR)
	$(INSTALL_DIR) $(PKG_BUILD_DIR)
	$(INSTALL_DIR) $(PKG_BUILD_DIR)/.go_work/build
	[ -f "$(VECTRA_AGENT_SOURCE_DIR)/go.mod" ]
	$(CP) $(VECTRA_AGENT_SOURCE_DIR)/go.mod $(PKG_BUILD_DIR)/go.mod
	[ ! -f "$(VECTRA_AGENT_SOURCE_DIR)/go.sum" ] || $(CP) $(VECTRA_AGENT_SOURCE_DIR)/go.sum $(PKG_BUILD_DIR)/go.sum
	$(CP) $(VECTRA_AGENT_SOURCE_DIR)/cmd $(PKG_BUILD_DIR)/cmd
	$(CP) $(VECTRA_AGENT_SOURCE_DIR)/internal $(PKG_BUILD_DIR)/internal
endef

define Build/Compile
	$(call GoPackage/Build/Compile)
endef

define Build/InstallDev
endef

define Package/$(PKG_NAME)/install
	$(call GoPackage/Package/Install/Bin,$(1))
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/etc/config/vectra-controller $(1)/etc/config/vectra-controller
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/vectra-controller $(1)/etc/init.d/vectra-controller
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/etc/uci-defaults/90_vectra_controller_defaults $(1)/etc/uci-defaults/90_vectra_controller_defaults
	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) ./files/lib/upgrade/keep.d/vectra-controller $(1)/lib/upgrade/keep.d/vectra-controller
	$(INSTALL_DIR) $(1)/usr/libexec/vectra-controller
	$(INSTALL_BIN) ./files/usr/libexec/vectra-controller/render-config.sh $(1)/usr/libexec/vectra-controller/render-config.sh
endef

$(eval $(call BuildPackage,$(PKG_NAME)))
