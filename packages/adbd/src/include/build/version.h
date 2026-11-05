// OpenWrt Linux adbd: soong's build/version.h (adb --version prints this as the build)
#pragma once

#include <string>

namespace android {
namespace build {
inline std::string GetBuildNumber() { return "openwrt-a8"; }
}  // namespace build
}  // namespace android
