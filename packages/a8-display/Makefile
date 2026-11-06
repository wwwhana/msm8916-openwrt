# SPDX-License-Identifier: GPL-2.0-only

include $(TOPDIR)/rules.mk

PKG_NAME:=a8-display
PKG_VERSION:=1.0
PKG_RELEASE:=3
PKG_LICENSE:=GPL-2.0-only

include $(INCLUDE_DIR)/kernel.mk
include $(INCLUDE_DIR)/package.mk

# OpenWrt already packages panel-mipi-dbi; only GPIO backlight is missing.
define KernelPackage/a8-backlight
  SUBMENU:=Video Support
  TITLE:=A8 GPIO controlled MPP4 backlight
  DEPENDS:=@TARGET_msm89xx +kmod-backlight
  KCONFIG:=CONFIG_BACKLIGHT_GPIO CONFIG_PINCTRL_SPMI_MPP=y
  FILES:=$(LINUX_DIR)/drivers/video/backlight/gpio_backlight.ko
  AUTOLOAD:=$(call AutoProbe,gpio_backlight)
endef

define Package/a8-display
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=A8 GC9107 display and one-button menu
  DEPENDS:=@TARGET_msm89xx +kmod-a8-backlight +kmod-drm-panel-mipi-dbi +kmod-input-evdev +jsonfilter +lua +libuci-lua
endef

define Build/Compile
	python3 ./gen-panel.py ./files/stock-init.hex $(PKG_BUILD_DIR)/mifib-v18,gc9107.bin
	$(TARGET_CC) $(TARGET_CFLAGS) $(TARGET_CPPFLAGS) -std=c99 -Wall -Wextra \
		-o $(PKG_BUILD_DIR)/a8-button ./src/a8-button.c $(TARGET_LDFLAGS)
endef

define Package/a8-display/install
	$(INSTALL_DIR) $(1)/lib/firmware $(1)/usr/sbin $(1)/etc/init.d $(1)/usr/libexec $(1)/usr/lib/lua/a8_display
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/mifib-v18,gc9107.bin $(1)/lib/firmware/
	$(INSTALL_BIN) ./files/a8-display $(1)/usr/sbin/a8-display
	$(INSTALL_BIN) ./files/a8-display.init $(1)/etc/init.d/a8-display
	$(INSTALL_BIN) ./files/a8-menu $(1)/usr/libexec/a8-menu
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/a8-button $(1)/usr/libexec/a8-button
	$(INSTALL_DATA) ./files/model.lua ./files/view.lua ./files/service.lua $(1)/usr/lib/lua/a8_display/
endef

$(eval $(call KernelPackage,a8-backlight))
$(eval $(call BuildPackage,a8-display))
