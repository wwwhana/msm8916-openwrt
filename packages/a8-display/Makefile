# SPDX-License-Identifier: GPL-2.0-only

include $(TOPDIR)/rules.mk

PKG_NAME:=a8-display
PKG_VERSION:=1.0
PKG_RELEASE:=6
PKG_LICENSE:=GPL-2.0-only

include $(INCLUDE_DIR)/kernel.mk
include $(INCLUDE_DIR)/package.mk

# OpenWrt already packages panel-mipi-dbi; only GPIO backlight is missing.
define KernelPackage/a8-backlight
  SUBMENU:=Video Support
  TITLE:=A8 GPIO controlled MPP4 backlight
  DEPENDS:=@TARGET_msm89xx +kmod-backlight
  KCONFIG:=CONFIG_BACKLIGHT_GPIO CONFIG_PINCTRL_QCOM_SPMI_PMIC=y
  FILES:=$(LINUX_DIR)/drivers/video/backlight/gpio_backlight.ko
  AUTOLOAD:=$(call AutoProbe,gpio_backlight)
endef

define Package/a8-display
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=A8 GC9107 display and one-button menu
  DEPENDS:=@TARGET_msm89xx +kmod-a8-backlight +kmod-drm-panel-mipi-dbi +kmod-input-evdev +jsonfilter +lua +libuci-lua +luci-lib-jsonc
endef

define Package/a8-display/conffiles
/etc/config/a8_device
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
	$(INSTALL_DATA) ./files/model.lua ./files/view.lua ./files/service.lua ./files/device.lua $(1)/usr/lib/lua/a8_display/
	$(INSTALL_DIR) $(1)/etc/config $(1)/usr/libexec/rpcd $(1)/usr/share/rpcd/acl.d $(1)/usr/share/luci/menu.d $(1)/www/luci-static/resources/view
	$(INSTALL_CONF) ./files/a8_device.config $(1)/etc/config/a8_device
	$(INSTALL_BIN) ./files/a8-device $(1)/usr/libexec/a8-device
	$(INSTALL_BIN) ./files/a8.device.rpc $(1)/usr/libexec/rpcd/a8.device
	$(INSTALL_DATA) ./files/a8-device.acl.json $(1)/usr/share/rpcd/acl.d/a8-device.json
	$(INSTALL_DATA) ./files/a8-device.menu.json $(1)/usr/share/luci/menu.d/a8-device.json
	$(INSTALL_DATA) ./files/a8-device.js $(1)/www/luci-static/resources/view/a8-device.js
endef

$(eval $(call KernelPackage,a8-backlight))
$(eval $(call BuildPackage,a8-display))
