# SPDX-License-Identifier: GPL-2.0-only
include $(TOPDIR)/rules.mk

PKG_NAME:=81voltd
PKG_VERSION:=1.2.0
PKG_RELEASE:=1
PKG_SOURCE:=81voltd_$(PKG_VERSION).orig.tar.gz
PKG_SOURCE_URL:=https://deb.debian.org/debian/pool/main/8/81voltd/
PKG_HASH:=3f0b2889d7b503f2fc27f7c976240903dce8ef73ce825db3181dadd2e7ef3f72
PKG_BUILD_DIR:=$(BUILD_DIR)/81voltd-v1.2.0-7c0cd9442d71b55260c9917c89ebc4bd20e283a8
PKG_LICENSE:=GPL-2.0-or-later
PKG_LICENSE_FILES:=LICENSE

include $(INCLUDE_DIR)/package.mk
include $(INCLUDE_DIR)/meson.mk

define Package/81voltd
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=WWAN
  TITLE:=Qualcomm IMS data connection service
  URL:=https://gitlab.postmarketos.org/modem/81voltd
  DEPENDS:=+modemmanager +libqmi +libqrtr +glib2
endef

define Package/81voltd/install
	$(INSTALL_DIR) $(1)/usr/sbin $(1)/etc/init.d
	$(INSTALL_BIN) $(MESON_BUILD_DIR)/81voltd $(1)/usr/sbin/81voltd
	$(INSTALL_BIN) ./files/81voltd.init $(1)/etc/init.d/81voltd
endef

$(eval $(call BuildPackage,81voltd))
