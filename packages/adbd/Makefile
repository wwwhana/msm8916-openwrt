# SPDX-License-Identifier: Apache-2.0
include $(TOPDIR)/rules.mk

PKG_NAME:=adbd
PKG_VERSION:=34.0.5
PKG_RELEASE:=1
PKG_SOURCE:=android-tools-$(PKG_VERSION).tar.xz
PKG_SOURCE_URL:=https://github.com/nmeum/android-tools/releases/download/$(PKG_VERSION)
PKG_HASH:=fb09cff12cfb82acf42a8ebebbc0342671bfcd02117716368bdc73fdda60304a
PKG_BUILD_DIR:=$(BUILD_DIR)/android-tools-$(PKG_VERSION)
PKG_LICENSE:=Apache-2.0 MIT ISC BSD-3-Clause
PKG_LICENSE_FILES:=vendor/adb/NOTICE vendor/boringssl/LICENSE vendor/fmtlib/NOTICE vendor/brotli/LICENSE a8-adbd/LICENSE.mu300
PKG_BUILD_PARALLEL:=1
# BoringSSL's error-table generator uses the host Go supplied by
# devenv/dependencies.sh (Ubuntu 22.04 Go 1.18 is sufficient).
PKG_BUILD_DEPENDS:=libcap

include $(INCLUDE_DIR)/package.mk
include $(INCLUDE_DIR)/cmake.mk

define Download/adbd-brotli
	FILE:=brotli-1.1.0.tar.gz
	URL:=https://codeload.github.com/google/brotli/tar.gz/refs/tags
	URL_FILE:=v1.1.0
	HASH:=e720a6ca29428b803f4ad165371771f5398faba397edf6778837a18599ea13ff
endef
$(eval $(call Download,adbd-brotli))

CMAKE_SOURCE_SUBDIR:=a8-adbd
CMAKE_OPTIONS += \
	-DANDROID_TOOLS=$(PKG_BUILD_DIR)/vendor \
	-DOPENSSL_NO_ASM=ON \
	-DBUILD_SHARED_LIBS=OFF \
	-DBUILD_TESTING=OFF \
	-DFMT_TEST=OFF \
	-DFMT_DOC=OFF \
	-DFMT_INSTALL=OFF

define Package/adbd
	SECTION:=utils
	CATEGORY:=Utilities
	TITLE:=USB-only Android Debug Bridge daemon
	DEPENDS:=@TARGET_msm89xx +libstdcpp +libatomic +liblz4 +libzstd +kmod-usb-gadget-fs +uci-usb-gadget
endef

define Package/adbd/description
	AOSP adbd for Linux. Provides shell, push and pull over the existing USB
	composite gadget. Does not create a TCP listener. Enabled on A8 V18 only.
endef

define Package/adbd/conffiles
/etc/adb/adb_keys
endef

define Build/Prepare
	$(call Build/Prepare/Default)
	$(CP) ./src $(PKG_BUILD_DIR)/a8-adbd
	$(CP) ./LICENSE.mu300 $(PKG_BUILD_DIR)/a8-adbd/
	$(TAR) -C $(PKG_BUILD_DIR)/vendor -xzf $(DL_DIR)/brotli-1.1.0.tar.gz
	mv $(PKG_BUILD_DIR)/vendor/brotli-1.1.0 $(PKG_BUILD_DIR)/vendor/brotli
endef

define Package/adbd/install
	$(INSTALL_DIR) $(1)/usr/sbin $(1)/etc/init.d $(1)/etc/uci-defaults $(1)/etc/adb
	$(INSTALL_BIN) $(PKG_INSTALL_DIR)/usr/bin/adbd $(1)/usr/sbin/adbd
	$(INSTALL_BIN) ./files/adbd.init $(1)/etc/init.d/adbd
	$(INSTALL_BIN) ./files/98-a8-adb $(1)/etc/uci-defaults/98-a8-adb
	touch $(1)/etc/adb/adb_keys
	chmod 0600 $(1)/etc/adb/adb_keys
	$(INSTALL_DIR) $(1)/usr/share/licenses/adbd
	$(INSTALL_DATA) ./LICENSE.mu300 $(1)/usr/share/licenses/adbd/
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/vendor/adb/NOTICE $(1)/usr/share/licenses/adbd/NOTICE.AOSP
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/vendor/boringssl/LICENSE $(1)/usr/share/licenses/adbd/LICENSE.BoringSSL
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/vendor/fmtlib/NOTICE $(1)/usr/share/licenses/adbd/NOTICE.fmt
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/vendor/brotli/LICENSE $(1)/usr/share/licenses/adbd/LICENSE.Brotli
endef

$(eval $(call BuildPackage,adbd))
