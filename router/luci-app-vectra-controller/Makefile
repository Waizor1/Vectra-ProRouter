include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-vectra-controller
VECTRA_VERSION?=0.1.13
VECTRA_RELEASE?=18
PKG_VERSION:=$(VECTRA_VERSION)
PKG_RELEASE:=$(VECTRA_RELEASE)
PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)

PKG_LICENSE:=MIT
PKG_MAINTAINER:=Vectra
PKG_URL:=https://router.vectra-pro.net

include $(INCLUDE_DIR)/package.mk

define Package/$(PKG_NAME)
  SECTION:=luci
  CATEGORY:=LuCI
  SUBMENU:=3. Applications
  TITLE:=LuCI bootstrap and rescue UI for Vectra Controller
  URL:=$(PKG_URL)
  MAINTAINER:=$(PKG_MAINTAINER)
  DEPENDS:=+luci-base +rpcd +vectra-controller-agent
  PKGARCH:=all
endef

define Package/$(PKG_NAME)/description
  Local bootstrap, diagnostics, and rescue console for the Vectra router agent.
endef

define Build/Prepare
	$(call Build/Prepare/Default)
	$(INSTALL_DIR) $(PKG_BUILD_DIR)/htdocs
	$(INSTALL_DIR) $(PKG_BUILD_DIR)/root
	$(CP) ./htdocs/. $(PKG_BUILD_DIR)/htdocs/
	$(CP) ./root/. $(PKG_BUILD_DIR)/root/
endef

define Build/Compile
endef

define Package/$(PKG_NAME)/install
	$(INSTALL_DIR) $(1)/www
	$(CP) $(PKG_BUILD_DIR)/htdocs/. $(1)/www/
	$(INSTALL_DIR) $(1)/
	$(CP) $(PKG_BUILD_DIR)/root/. $(1)/
endef

define Package/$(PKG_NAME)/postinst
#!/bin/sh

[ -n "$${IPKG_INSTROOT}" ] || { \
	rm -f /tmp/luci-indexcache.*; \
	rm -rf /tmp/luci-modulecache/; \
	/etc/init.d/rpcd reload 2>/dev/null; \
	exit 0; \
}
endef

$(eval $(call BuildPackage,$(PKG_NAME)))
