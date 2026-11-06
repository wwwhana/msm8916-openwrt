# SPDX-License-Identifier: GPL-2.0-only
include $(TOPDIR)/rules.mk
PKG_NAME:=a8-apn
PKG_VERSION:=1.0
PKG_RELEASE:=3
PKG_LICENSE:=GPL-2.0-only LicenseRef-Public-Domain
PKG_LICENSE_FILES:=COPYING
include $(INCLUDE_DIR)/package.mk

PROVIDERS_REV:=c6796724e78483ee34fd999fe1532f3c12d59fa6
define Download/a8-apn-providers
	FILE:=a8-apn-providers-$(PROVIDERS_REV).tar.gz
	URL:=https://codeload.github.com/GNOME/mobile-broadband-provider-info/tar.gz
	URL_FILE:=$(PROVIDERS_REV)
	HASH:=7fe9012b26de2b8c517e39338cfed770e5306d61d4206f8c092882558f499864
endef
$(eval $(call Download,a8-apn-providers))

define Package/a8-apn
	SECTION:=net
	CATEGORY:=Network
	TITLE:=Travel profiles for qcom-carrier-autocfg
	DEPENDS:=@TARGET_msm89xx +qcom-carrier-autocfg +lua +libuci-lua +luci-lib-jsonc +luci-proto-modemmanager
	PKGARCH:=all
endef
define Package/a8-apn/conffiles
/etc/config/a8_apn
endef
define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
	$(TAR) -C $(PKG_BUILD_DIR) --strip-components=1 -xzf $(DL_DIR)/a8-apn-providers-$(PROVIDERS_REV).tar.gz
endef
define Build/Compile
	python3 ./gen-providers.py $(PKG_BUILD_DIR)/serviceproviders.xml $(PKG_BUILD_DIR)/apn-db
endef
define Package/a8-apn/install
	$(INSTALL_DIR) $(1)/usr/share/a8-apn $(1)/usr/lib/lua/a8_apn $(1)/usr/sbin
	$(CP) $(PKG_BUILD_DIR)/apn-db/. $(1)/usr/share/a8-apn/
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/COPYING $(1)/usr/share/a8-apn/COPYING.providers
	$(INSTALL_DATA) ./files/model.lua ./files/service.lua $(1)/usr/lib/lua/a8_apn/
	$(INSTALL_BIN) ./files/a8-apn $(1)/usr/sbin/a8-apn
	$(INSTALL_DIR) $(1)/etc/config $(1)/etc/uci-defaults
	$(INSTALL_CONF) ./files/a8_apn.config $(1)/etc/config/a8_apn
	chmod 0600 $(1)/etc/config/a8_apn
	$(INSTALL_BIN) ./files/zzz-a8-apn $(1)/etc/uci-defaults/zzz-a8-apn
	$(INSTALL_DIR) $(1)/usr/libexec/rpcd $(1)/usr/share/rpcd/acl.d $(1)/usr/share/luci/menu.d
	$(INSTALL_BIN) ./files/a8.apn.rpc $(1)/usr/libexec/rpcd/a8.apn
	$(INSTALL_DATA) ./files/a8-apn.acl.json $(1)/usr/share/rpcd/acl.d/a8-apn.json
	$(INSTALL_DATA) ./files/a8-apn.menu.json $(1)/usr/share/luci/menu.d/a8-apn.json
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view
	$(INSTALL_DATA) ./files/a8-apn.js $(1)/www/luci-static/resources/view/a8-apn.js
endef
$(eval $(call BuildPackage,a8-apn))
