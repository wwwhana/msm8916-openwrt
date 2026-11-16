# SPDX-License-Identifier: GPL-2.0-only
include $(TOPDIR)/rules.mk
PKG_NAME:=a8-network
PKG_VERSION:=1.0
PKG_RELEASE:=2
PKG_LICENSE:=GPL-2.0-only
include $(INCLUDE_DIR)/package.mk

define Package/a8-network
	SECTION:=net
	CATEGORY:=Network
	TITLE:=A8 USB Ethernet LAN and WAN
	DEPENDS:=+lua +libuci-lua +libubus-lua +luci-lib-jsonc +luci-lib-nixio +msm89xx-scripts +uci-usb-gadget \
		+r8152-firmware +kmod-usb-net-rtl8152 +kmod-usb-net-rtl8150 \
		+kmod-usb-net-asix +kmod-usb-net-asix-ax88179 +kmod-usb-net-aqc111 \
		+kmod-usb-net-cdc-ether +kmod-usb-net-cdc-ncm +kmod-usb-net-cdc-eem \
		+kmod-usb-net-cdc-subset +kmod-usb-net-dm9601-ether +kmod-usb-net-smsc95xx \
		+kmod-usb-net-smsc75xx +kmod-usb-net-mcs7830 +kmod-usb-net-pegasus \
		+kmod-usb-net-sr9700 +kmod-usb-net-sr9800 +kmod-usb-net-ch9200 \
		+kmod-usb-net-lan78xx +kmod-usb-net-rndis
	PKGARCH:=all
endef
define Build/Compile
endef
define Package/a8-network/install
	$(INSTALL_DIR) $(1)
	$(CP) ./root/* $(1)/
endef
$(eval $(call BuildPackage,a8-network))
