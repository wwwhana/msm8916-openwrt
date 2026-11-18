# SPDX-License-Identifier: GPL-2.0-only
include $(TOPDIR)/rules.mk

PKG_NAME:=a8-image
PKG_VERSION:=1.0
PKG_RELEASE:=2
PKG_LICENSE:=GPL-2.0-only BSD-3-Clause
PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
GO_PKG:=a8-image
GO_PKG_BUILD_PKG:=a8-image
GO_PKG_INSTALL_BIN_PATH:=/usr/libexec

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/a8-image
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=MiFi LCD message image previews
  DEPENDS:=$(GO_ARCH_DEPENDS)
endef

define Build/Prepare
	$(call Build/Prepare/Default)
	$(CP) ./src/. $(PKG_BUILD_DIR)/
endef

define Package/a8-image/install
	$(call GoPackage/Package/Install/Bin,$(1))
	$(INSTALL_DIR) $(1)/usr/share/a8-display
	$(INSTALL_DATA) ./src/LICENSE.x-image $(1)/usr/share/a8-display/a8-image-LICENSE
endef

$(eval $(call GoBinPackage,a8-image))
$(eval $(call BuildPackage,a8-image))
