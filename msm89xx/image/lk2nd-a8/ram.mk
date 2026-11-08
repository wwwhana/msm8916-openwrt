# SPDX-License-Identifier: BSD-3-Clause
# Default to the manual Fastboot diagnostic; zero tests the normal boot path.
A8_LCD_RAM_TEST ?= 1
DEFINES += A8_ONE_BUTTON_MENU=1 A8_LCD_RAM_TEST=$(A8_LCD_RAM_TEST) USE_TARGET_HS200_CAPS=1
MODULES += lk2nd/a8
TARGET := msm8916
ifeq ($(A8_LCD_RAM_TEST),0)
LK2ND_FORCE_FASTBOOT := 0
# The installed boot partition starts at byte zero; do not reserve lk2nd space.
LK2ND_PARTITION_SIZE :=
else
LK2ND_FORCE_FASTBOOT := 1
endif
LK2ND_DISPLAY :=
include lk2nd/project/lk2nd.mk
