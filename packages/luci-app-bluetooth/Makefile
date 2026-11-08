# See /LICENSE for more information.
# This is free software, licensed under the GNU General Public License v3.

include $(TOPDIR)/rules.mk

LUCI_TITLE:=LuCI support for bluetooth
LUCI_DEPENDS:=+luci-base +bluez-daemon +bluez-utils +bluez-network +rpcd-mod-ucode +ucode-mod-fs +jshn +coreutils-timeout
LUCI_PKGARCH:=all
PKG_VERSION:=1.0
PKG_RELEASE:=3

PKG_LICENSE:=GPL-3.0
PKG_MAINTAINER:=sbwml <admin@cooluc.com>

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
