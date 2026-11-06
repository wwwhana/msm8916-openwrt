// SPDX-License-Identifier: BSD-3-Clause
#include <debug.h>
#include <dev/keys.h>
#include <kernel/thread.h>
#include <platform.h>
#include <platform/gpio.h>
#include <platform/timer.h>
#include <pm8x41.h>
#include <stdio.h>
#include <string.h>
#include <lk2nd/device/keys.h>
#include "boot-menu.h"
#include "stock-init.h"
#include "tux.h"
/* Reuse LK's existing 6x12 ASCII font inside 8x16 cells. */
#include "../../lib/font/font.h"

/* Stock SPI5 pins: GPIO16 MOSI, GPIO17 MISO (unused), 18 CS, 19 clock. */
#define MOSI 16
#define CS 18
#define CLK 19
#define RESET 11
#define DC 30
#define L6_BASE 0x4500
#define L17_BASE 0x5000
#define MPP4_BASE 0xa300
static struct a8_button button;
static enum a8_button_event queued;
static char previous[8][17];

static void sample_button(void)
{
    enum a8_button_event e = a8_button_poll(&button, lk2nd_keys_pressed(KEY_POWER), current_time());
    if (e && !queued) queued = e;
}
static void set_pin(unsigned pin, int high)
{
    gpio_set_dir(pin, high ? GPIO_OUT : 0);
}
static void output(unsigned pin, unsigned drive, int high)
{
    set_pin(pin, high);
    gpio_tlmm_config(pin, 0, GPIO_OUTPUT, GPIO_NO_PULL, drive, GPIO_DISABLE);
}
/* Mode 0, MSB first. A conservative GPIO clock avoids adding a QUP clock driver
 * just for the boot menu. Linux uses the hardware SPI controller afterwards. */
static void spi_byte(unsigned char value)
{
    unsigned bit;
    for (bit=0; bit<8; ++bit) {
        set_pin(MOSI, value & 0x80);
        udelay(1);
        set_pin(CLK, 1);
        udelay(1);
        set_pin(CLK, 0);
        value <<= 1;
    }
}
static void command(unsigned char command, const unsigned char *data, unsigned size)
{
    set_pin(CS, 0);
    set_pin(DC, 0);
    spi_byte(command);
    set_pin(DC, 1);
    while (size--) spi_byte(*data++);
    set_pin(CS, 1);
}
static int read_pmic(unsigned sid, unsigned reg, unsigned char *value)
{
    return spmi_reg_read(sid, reg, value, 0) == 0;
}
static int write_pmic(unsigned sid, unsigned reg, unsigned char value)
{
    unsigned char actual;
    return spmi_reg_write(sid, reg, &value, 0) == 0 &&
        read_pmic(sid, reg, &actual) && actual == value;
}
static int panel_init(void)
{
    unsigned offset=0;
    unsigned char range, step, enabled;
    uint32_t voltage;
    /* L6 is shared: preserve SBL's voltage. Never guess a new shared rail value. */
    if (!read_pmic(1, L6_BASE+0x40, &range) || !read_pmic(1, L6_BASE+0x41, &step)) return 0;
    switch (range & 7) {
    case 2: voltage=750000+step*12500; break;
    case 3: voltage=1500000+step*25000; break;
    case 4: voltage=1750000+step*50000; break;
    default: return 0;
    }
    if (voltage != 1800000) {
        dprintf(CRITICAL, "A8 LCD: shared L6 is %u uV; leaving display off\n", voltage);
        return 0;
    }
    if (!read_pmic(1, L6_BASE+0x46, &enabled) ||
        !write_pmic(1, L6_BASE+0x46, enabled | 0x80)) return 0;
    /* PM8916 L17 PLDO range 3: 1.5 V + 54 * 25 mV = stock 2.85 V. */
    if (!write_pmic(1, L17_BASE+0x45, 0x80) ||
        !write_pmic(1, L17_BASE+0x40, 3) || !write_pmic(1, L17_BASE+0x41, 54) ||
        !write_pmic(1, L17_BASE+0x46, 0x80)) return 0;
    /* PMIC MPP mode 6 = current sink; raw sink setting 7 = stock 40 mA.
     * Register layout matches Linux pinctrl-spmi-mpp. Enable only after drawing. */
    if (!write_pmic(0, MPP4_BASE+0x46, 0) ||
        !write_pmic(0, MPP4_BASE+0x40, 0x60) ||
        !write_pmic(0, MPP4_BASE+0x4c, 7)) return 0;
    output(CS, GPIO_2MA, 1);
    output(CLK, GPIO_12MA, 0);
    output(MOSI, GPIO_12MA, 0);
    output(DC, GPIO_8MA, 1);
    output(RESET, GPIO_8MA, 1);
    mdelay(1); set_pin(RESET, 0); mdelay(10); set_pin(RESET, 1); mdelay(120);
    while (offset < sizeof(stock_init)) {
        unsigned wait=stock_init[offset++], size=stock_init[offset++];
        unsigned char cmd=stock_init[offset++];
        command(cmd, stock_init+offset, size-1);
        offset += size-1;
        if (wait) mdelay(wait);
    }
    memset(previous, 0xff, sizeof(previous));
    return 1;
}
static void draw_row(unsigned row, const char *text)
{
    unsigned x,y;
    char line[17];
    unsigned char columns[]={0,0,0,127}, rows[]={0,row*16,0,row*16+15};
    snprintf(line, sizeof(line), "%-16.16s", text);
    if (!memcmp(line, previous[row], 17)) return;
    memcpy(previous[row], line, 17);
    command(0x2a, columns, 4); command(0x2b, rows, 4);
    set_pin(CS, 0); set_pin(DC, 0); spi_byte(0x2c); set_pin(DC, 1);
    for (y=0; y<16; ++y) {
        for (x=0; x<128; ++x) {
            unsigned char c=line[x/8], bits=0;
            int lit;
            if (c >= 32 && c < 127 && y >= 2 && y < 14)
                bits=FONT[c*12+y-2];
            lit=(x%8 < 6) && (bits & (1u << (x%8)));
            spi_byte(lit ? 0xff : 0); spi_byte(lit ? 0xff : 0);
        }
        sample_button();
    }
    set_pin(CS, 1);
}
static void draw_menu(const struct a8_boot_state *state, uint32_t now)
{
    static const char *labels[]={"OpenWrt", "Recovery", "Fastboot USB", "EDL / USB 9008"};
    char text[32];
    unsigned i, elapsed=(uint32_t)(now-state->started)/1000;
    draw_row(0, "A8 boot menu");
    for (i=0; i<4; ++i) {
        snprintf(text, sizeof(text), "%c %s", state->selected == i ? '>' : ' ', labels[i]);
        draw_row(i+1, text);
    }
    if (state->interactive) snprintf(text, sizeof(text), "Choose a mode");
    else snprintf(text, sizeof(text), "Auto boot: %us", elapsed >= 5 ? 0 : 5-elapsed);
    draw_row(5, text); draw_row(6, "Short: next"); draw_row(7, "Hold 1s: select");
}
int a8_boot_splash(const char *mode)
{
    unsigned x, y;
    unsigned char columns[]={0,0,0,127}, rows[]={0,0,0,95};
    if (!panel_init()) {
        dprintf(CRITICAL, "A8 LCD unavailable; continue boot\n");
        return 0;
    }
    command(0x2a, columns, 4); command(0x2b, rows, 4);
    set_pin(CS, 0); set_pin(DC, 0); spi_byte(0x2c); set_pin(DC, 1);
    for (y=0; y<96; ++y) {
        for (x=0; x<128; ++x) {
            unsigned short pixel=a8_splash_pixel(x, y);
            spi_byte(pixel >> 8); spi_byte(pixel);
        }
    }
    set_pin(CS, 1);
    draw_row(6, mode); draw_row(7, "Starting...");
    if (!write_pmic(0, MPP4_BASE+0x40, 0x61) || !write_pmic(0, MPP4_BASE+0x46, 0x80))
        return 0;
    thread_sleep(750);
    return 1;
}
enum a8_boot_choice a8_boot_menu(void)
{
    struct a8_boot_state state={.selected=0, .interactive=0};
    enum a8_boot_choice choice;
    if (!a8_boot_splash("OpenWrt")) return A8_BOOT;
    a8_button_reset(&button, lk2nd_keys_pressed(KEY_POWER), current_time());
    queued=A8_NONE;
    state.started=current_time();
    draw_menu(&state, state.started);
    for (;;) {
        sample_button();
        choice=a8_boot_step(&state, queued, button.armed && button.raw, current_time());
        queued=A8_NONE;
        if (choice != A8_WAIT) break;
        draw_menu(&state, current_time());
        thread_sleep(10);
    }
    /* Ignore the rest of the selecting press before handing control onward. */
    draw_row(5, "Release button");
    while (lk2nd_keys_pressed(KEY_POWER)) thread_sleep(10);
    draw_row(5, choice == A8_FASTBOOT ? "Fastboot USB" : choice == A8_EDL ? "Entering EDL" : "Booting...");
    return choice;
}
