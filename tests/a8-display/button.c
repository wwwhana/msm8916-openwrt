// SPDX-License-Identifier: BSD-3-Clause
#include <assert.h>
#include "one-button.h"
#include "boot-menu.h"

int main(void)
{
    struct a8_button b;
    struct a8_boot_state boot={.started=0, .selected=0, .interactive=0};
    a8_button_reset(&b, 0, 0);
    assert(a8_button_poll(&b, 1, 100) == A8_NONE);
    assert(a8_button_poll(&b, 1, 130) == A8_NONE);
    assert(a8_button_poll(&b, 0, 200) == A8_NONE);
    assert(a8_button_poll(&b, 0, 230) == A8_SHORT);
    assert(a8_button_poll(&b, 0, 300) == A8_NONE);
    /* Bounce does not create a press; a sub-threshold release is still short. */
    a8_button_reset(&b, 0, 0);
    assert(a8_button_poll(&b, 1, 10) == A8_NONE);
    assert(a8_button_poll(&b, 0, 20) == A8_NONE);
    assert(a8_button_poll(&b, 0, 60) == A8_NONE);
    assert(a8_button_poll(&b, 1, 100) == A8_NONE);
    assert(a8_button_poll(&b, 1, 130) == A8_NONE);
    assert(a8_button_poll(&b, 0, 1099) == A8_NONE);
    assert(a8_button_poll(&b, 0, 1129) == A8_SHORT);
    /* Exactly one long event, even when held for several seconds. */
    a8_button_reset(&b, 0, 0);
    assert(a8_button_poll(&b, 1, 100) == A8_NONE);
    assert(a8_button_poll(&b, 1, 130) == A8_NONE);
    assert(a8_button_poll(&b, 1, 1099) == A8_NONE);
    assert(a8_button_poll(&b, 1, 1100) == A8_LONG);
    assert(a8_button_poll(&b, 1, 5100) == A8_NONE);
    assert(a8_button_poll(&b, 0, 5200) == A8_NONE);
    assert(a8_button_poll(&b, 0, 5230) == A8_NONE);
    /* Attaching while held (or resync after SYN_DROPPED) cannot select. */
    a8_button_reset(&b, 1, 0);
    assert(a8_button_poll(&b, 1, 5000) == A8_NONE);
    assert(a8_button_poll(&b, 0, 5100) == A8_NONE);
    assert(a8_button_poll(&b, 0, 5130) == A8_NONE);
    assert(a8_button_poll(&b, 1, 5200) == A8_NONE);
    assert(a8_button_poll(&b, 1, 5230) == A8_NONE);
    assert(a8_button_poll(&b, 1, 6200) == A8_LONG);
    /* Millisecond counter wrap is safe. */
    a8_button_reset(&b, 0, UINT32_MAX-200);
    assert(a8_button_poll(&b, 1, UINT32_MAX-100) == A8_NONE);
    assert(a8_button_poll(&b, 1, UINT32_MAX-70) == A8_NONE);
    assert(a8_button_poll(&b, 1, 899) == A8_LONG);
    assert(a8_boot_step(&boot, A8_NONE, 0, 4999) == A8_WAIT);
    assert(a8_boot_step(&boot, A8_NONE, 0, 5000) == A8_BOOT);
    boot.started=0; boot.interactive=0;
    assert(a8_boot_step(&boot, A8_NONE, 1, 4999) == A8_WAIT);
    assert(a8_boot_step(&boot, A8_NONE, 0, 10000) == A8_WAIT);
    assert(a8_boot_step(&boot, A8_SHORT, 0, 10100) == A8_WAIT);
    assert(boot.selected == A8_RECOVERY);
    assert(a8_boot_step(&boot, A8_LONG, 0, 11100) == A8_RECOVERY);
    a8_boot_step(&boot, A8_SHORT, 0, 11200);
    assert(a8_boot_step(&boot, A8_LONG, 0, 12200) == A8_FASTBOOT);
    a8_boot_step(&boot, A8_SHORT, 0, 12300);
    assert(a8_boot_step(&boot, A8_LONG, 0, 13300) == A8_EDL);
    a8_boot_step(&boot, A8_SHORT, 0, 13400);
    assert(a8_boot_step(&boot, A8_LONG, 0, 14400) == A8_BOOT);
    return 0;
}
