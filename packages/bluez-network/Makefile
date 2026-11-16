# SPDX-License-Identifier: GPL-2.0-only
include $(TOPDIR)/rules.mk

PKG_NAME:=bluez-network
PKG_VERSION:=2020.10.25
PKG_RELEASE:=4
PKG_SOURCE_VERSION:=f65321736475429316f07ee94ec0deac8e46ec4a
PKG_SOURCE:=$(PKG_SOURCE_VERSION).tar.gz
PKG_SOURCE_URL:=https://github.com/khvzak/bluez-tools/archive/
PKG_HASH:=dd5acf9e501514f4ad1136af966d2723f3270e0e2fffd6d29da93fa76c9fafa3
PKG_BUILD_DIR:=$(BUILD_DIR)/bluez-tools-$(PKG_SOURCE_VERSION)
PKG_FIXUP:=autoreconf
PKG_LICENSE:=GPL-2.0-only
PKG_LICENSE_FILES:=COPYING

include $(INCLUDE_DIR)/package.mk
include $(INCLUDE_DIR)/nls.mk

define Package/bluez-network
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Bluetooth PAN Internet sharing
  URL:=https://github.com/khvzak/bluez-tools
  DEPENDS:=+bluez-daemon +bluez-tools +bluez-utils +glib2 +kmod-bluetooth
endef

define Package/bluez-network/conffiles
/etc/config/bt-nap
endef

define Package/bluez-network/install
	$(INSTALL_DIR) $(1)/usr/bin $(1)/usr/sbin $(1)/etc/init.d $(1)/etc/config
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/src/bt-network $(1)/usr/bin/
	$(INSTALL_BIN) ./files/bt-nap $(1)/usr/sbin/bt-nap
	$(INSTALL_BIN) ./files/bt-compat $(1)/usr/sbin/bt-compat
	$(INSTALL_BIN) ./files/bt-nap.init $(1)/etc/init.d/bt-nap
	$(INSTALL_CONF) ./files/bt-nap.config $(1)/etc/config/bt-nap
endef

$(eval $(call BuildPackage,bluez-network))
