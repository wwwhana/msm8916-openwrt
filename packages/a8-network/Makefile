# SPDX-License-Identifier: GPL-2.0-only
include $(TOPDIR)/rules.mk
PKG_NAME:=a8-network
PKG_VERSION:=1.0
PKG_RELEASE:=3
PKG_LICENSE:=GPL-2.0-only
include $(INCLUDE_DIR)/package.mk

define Package/a8-network
	SECTION:=net
	CATEGORY:=Network
	TITLE:=A8 USB Ethernet LAN and WAN
	DEPENDS:=+lua +libuci-lua +libubus-lua +luci-lib-jsonc +luci-lib-nixio +msm89xx-scripts +uci-usb-gadget
	PKGARCH:=all
endef
define Build/Compile
endef
define Package/a8-network/install
	$(INSTALL_DIR) $(1)
	$(CP) ./root/* $(1)/
endef
$(eval $(call BuildPackage,a8-network))
