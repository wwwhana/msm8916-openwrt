# SPDX-License-Identifier: GPL-2.0-or-later
include $(TOPDIR)/rules.mk

PKG_NAME:=bluealsa
PKG_VERSION:=4.3.1
PKG_RELEASE:=2
PKG_SOURCE:=bluez-alsa-$(PKG_VERSION).tar.gz
PKG_SOURCE_URL:=https://codeload.github.com/arkq/bluez-alsa/tar.gz/refs/tags/v$(PKG_VERSION)?
PKG_HASH:=933fe898dfac21fdfeb5f4ffa685c2aa2db9c064d639170ac2652f156e956a2a
PKG_BUILD_DIR:=$(BUILD_DIR)/bluez-alsa-$(PKG_VERSION)
PKG_FIXUP:=autoreconf
PKG_BUILD_PARALLEL:=1
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE

include $(INCLUDE_DIR)/package.mk

define Package/bluealsa
  SECTION:=sound
  CATEGORY:=Sound
  TITLE:=Bluetooth HFP headset audio
  URL:=https://github.com/arkq/bluez-alsa
  DEPENDS:=+bluez-daemon +bluez-libs +glib2 +libdbus +sbc +libspandsp +alsa-lib
endef

CONFIGURE_ARGS += --enable-msbc --enable-cli --disable-aplay --disable-manpages

# ModemConfig-KO uses the PCM D-Bus pipes directly. No local sound card or
# ALSA PCM plug-in is needed for the headset microphone and speaker.
define Build/Compile
	+$(MAKE_VARS) $(MAKE) $(PKG_JOBS) -C $(PKG_BUILD_DIR)/src $(MAKE_FLAGS) bluealsa
	+$(MAKE_VARS) $(MAKE) $(PKG_JOBS) -C $(PKG_BUILD_DIR)/utils/cli $(MAKE_FLAGS) bluealsa-cli
endef

define Package/bluealsa/install
	$(INSTALL_DIR) $(1)/usr/bin $(1)/etc/init.d $(1)/etc/dbus-1/system.d
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/src/bluealsa $(1)/usr/bin/bluealsa
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/utils/cli/bluealsa-cli $(1)/usr/bin/bluealsa-cli
	$(INSTALL_BIN) ./files/bluealsa.init $(1)/etc/init.d/bluealsa
	$(INSTALL_CONF) ./files/bluealsa.conf $(1)/etc/dbus-1/system.d/bluealsa.conf
endef

$(eval $(call BuildPackage,bluealsa))
