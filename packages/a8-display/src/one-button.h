/* SPDX-License-Identifier: BSD-3-Clause */
#ifndef A8_ONE_BUTTON_H
#define A8_ONE_BUTTON_H
#include <stdint.h>

#define A8_DEBOUNCE_MS 30u
#define A8_LONG_MS 1000u
enum a8_button_event { A8_NONE, A8_SHORT, A8_LONG };
struct a8_button {
    uint32_t changed, pressed;
    int raw, stable, armed, fired;
};

static inline void a8_button_reset(struct a8_button *b, int down, uint32_t now)
{
    b->changed = b->pressed = now;
    b->raw = b->stable = !!down;
    b->armed = !down;
    b->fired = 0;
}

/* Also used by lk1st: a held startup key must be released before navigating. */
static inline enum a8_button_event a8_button_poll(struct a8_button *b, int down, uint32_t now)
{
    enum a8_button_event event = A8_NONE;
    down = !!down;
    if (down != b->raw) {
        b->raw = down;
        b->changed = now;
    }
    if (b->raw != b->stable && (uint32_t)(now - b->changed) >= A8_DEBOUNCE_MS) {
        b->stable = b->raw;
        if (b->stable) {
            b->pressed = b->changed;
            b->fired = 0;
        } else {
            if (b->armed && !b->fired)
                event = (uint32_t)(b->changed - b->pressed) >= A8_LONG_MS ? A8_LONG : A8_SHORT;
            b->armed = 1;
        }
    }
    /* Don't select while a release is still being debounced. */
    if (b->stable && b->raw && b->armed && !b->fired &&
        (uint32_t)(now - b->pressed) >= A8_LONG_MS) {
        b->fired = 1;
        event = A8_LONG;
    }
    return event;
}

static inline int a8_navigation_poll(struct a8_button *b, int down, uint32_t now)
{
    int was_down = b->stable, armed = b->armed;
    a8_button_poll(b, down, now);
    return armed && !was_down && b->stable;
}
#endif
