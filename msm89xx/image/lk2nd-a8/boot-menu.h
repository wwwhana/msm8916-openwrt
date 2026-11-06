/* SPDX-License-Identifier: BSD-3-Clause */
#ifndef A8_BOOT_MENU_H
#define A8_BOOT_MENU_H
#include "one-button.h"
enum a8_boot_choice { A8_BOOT, A8_RECOVERY, A8_FASTBOOT, A8_EDL, A8_WAIT };
struct a8_boot_state {
    uint32_t started;
    unsigned selected;
    int interactive;
};
static inline enum a8_boot_choice a8_boot_step(struct a8_boot_state *s,
        enum a8_button_event event, int pressing, uint32_t now)
{
    if (pressing || event) s->interactive = 1;
    if (event == A8_SHORT) s->selected = (s->selected + 1) % 4;
    if (event == A8_LONG) return (enum a8_boot_choice)s->selected;
    if (!s->interactive && (uint32_t)(now - s->started) >= 5000) return A8_BOOT;
    return A8_WAIT;
}
int a8_boot_splash(const char *mode);
enum a8_boot_choice a8_boot_menu(void);
#endif
