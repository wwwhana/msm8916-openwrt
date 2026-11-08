/* Exercise the complete boot LCD driver; only hardware and time are replaced. */
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "boot-menu.h"
#include "stock-init.h"
#include "tux.h"

#define KEY_POWER 1
#define CRITICAL 0
#define INFO 1
static void panel_log(unsigned level, const char *message, ...)
{ (void)level; (void)message; }
#define dprintf panel_log
enum { GPIO_NO_PULL=0, GPIO_OUTPUT=1, GPIO_DISABLE=1,
       GPIO_OUT=2, GPIO_2MA=0, GPIO_8MA=3, GPIO_12MA=5 };
static unsigned char FONT[128*12];
static uint64_t now_us;
static unsigned scenario, configured, frames, pmic_writes;
static unsigned char regs[2][65536];
static unsigned pins[128], bits, byte, cmd, count;
static unsigned char params[4];
static unsigned x0, x1=127, y0, y1=127;
#if A8_LCD_RAM_TEST
static void (*diagnostic_command)(const char *, void *, unsigned);
static unsigned progress, passed, failed;
static void fastboot_register(const char *name, void (*handler)(const char *, void *, unsigned))
{
    assert(!strcmp(name, "oem a8-lcd"));
    diagnostic_command=handler;
}
static void fastboot_info(const char *message) { assert(message && *message); progress++; }
static void fastboot_okay(const char *message) { assert(!*message); passed++; }
static void fastboot_fail(const char *message) { assert(*message); failed++; }
#define FASTBOOT_INIT(function)
#endif

static uint32_t current_time(void) { return now_us/1000; }
static void udelay(unsigned us) { now_us+=us; }
static void mdelay(unsigned ms) { now_us+=(uint64_t)ms*1000; }
static void thread_sleep(unsigned ms) { mdelay(ms); assert(now_us < 9000000); }
static int lk2nd_keys_pressed(unsigned key) { assert(key==KEY_POWER); return 0; }

static int spmi_reg_read(unsigned sid, unsigned reg, unsigned char *v, unsigned priority)
{
    assert(sid<2 && reg<65536 && !priority);
    if (scenario==1 && reg==0x4540) return 1;
    if (scenario==2 && reg==0x5046) return 1;
    *v=regs[sid][reg];
    if (scenario==3 && reg==0x5041) *v^=1;
    if (scenario==9 && reg==0xa340) *v^=0x80;
    return 0;
}
static int spmi_reg_write(unsigned sid, unsigned reg, unsigned char *v, unsigned priority)
{
    assert(!priority);
    assert(sid==0 && (reg==0xa340 || reg==0xa346 || reg==0xa34c));
    pmic_writes++;
    if (scenario==8 && reg==0xa34c) return 1;
    regs[sid][reg]=*v;
    return 0;
}

static void spi_received(unsigned value)
{
    if (!pins[30]) { cmd=value; count=0; return; }
    if (cmd==0x2a || cmd==0x2b) {
        assert(count<4);
        params[count]=value;
    }
    if (cmd==0x2c && y0==0 && y1==95) {
        unsigned short pixel=a8_splash_pixel((count/2)%128, (count/2)/128);
        assert(value==(count%2 ? pixel&255 : pixel>>8));
    }
    count++;
}
static void gpio_set_dir(unsigned pin, unsigned value)
{
    unsigned high=!!value;
    assert(pin==11 || pin==16 || pin==18 || pin==19 || pin==30);
    assert(value==0 || value==GPIO_OUT);
    if (pin==19 && high && !pins[19] && !pins[18]) {
        byte=(byte<<1)|pins[16];
        if (++bits==8) { spi_received(byte); bits=byte=0; }
    }
    if (pin==18 && high && !pins[18]) {
        assert(!bits);
        if (cmd==0x2a && count) {
            assert(count==4); x0=params[0]*256+params[1]; x1=params[2]*256+params[3];
        } else if (cmd==0x2b && count) {
            assert(count==4); y0=params[0]*256+params[1]; y1=params[2]*256+params[3];
        } else if (cmd==0x2c && count) {
            assert(x0<=x1 && x1<128 && y0<=y1 && y1<128);
            assert(count==(x1-x0+1)*(y1-y0+1)*2);
            if (y0==0 && y1==95) frames++;
        }
        cmd=count=0;
    }
    pins[pin]=high;
}
static void gpio_tlmm_config(unsigned pin, unsigned func, unsigned dir,
                             unsigned pull, unsigned drive, unsigned enable)
{
    (void)pin; (void)drive;
    assert(!func && dir==GPIO_OUTPUT && pull==GPIO_NO_PULL && enable==GPIO_DISABLE);
    configured++;
}

/* The production driver is inserted here by test_display.py. */

int main(int argc, char **argv)
{
    assert(argc==2);
    scenario=atoi(argv[1]);
    regs[1][0x4541]=4; regs[1][0x4546]=0xa0;
    regs[1][0x5041]=88; regs[1][0x5046]=0x80;
    if (scenario==4) regs[1][0x4546]=0;
    if (scenario==5) regs[1][0x4541]=5;
    if (scenario==6) regs[1][0x5046]=0;
    if (scenario==7) regs[1][0x5040]=2;
    memset(FONT, 0x15, sizeof(FONT));
#if A8_LCD_RAM_TEST
    lcd_diagnostic_register();
    assert(diagnostic_command && !pmic_writes && !configured && !frames);
    diagnostic_command("", NULL, 0);
    assert(!diagnostic_running && progress);
    assert(passed==!scenario && failed==!!scenario);
#else
    assert(a8_boot_menu()==A8_BOOT);
#endif
    if (scenario) {
        assert(!configured && !frames);
        assert(now_us==0);
        if (scenario<8) assert(!pmic_writes);
    } else {
        assert(configured==5 && frames==1);
        assert(regs[0][0xa346]==0x80 && regs[0][0xa340]==0x61);
        assert(pins[11]==1 && pins[18]==1 && pins[19]==0);
#if A8_LCD_RAM_TEST
        assert(progress>7);
        assert(!strncmp(previous[6], "Fastboot USB    ", 16));
        assert(current_time()>=1500 && current_time()<2500);
#else
        assert(!strncmp(previous[0], "Boot menu       ", 16));
        assert(!strncmp(previous[5], "Booting...      ", 16));
        assert(current_time()>=6500 && current_time()<8000);
#endif
        /* Fixed cells truncate long strings and clear the old suffix. */
        draw_row(2, "123456789012345678901234567890");
        assert(!memcmp(previous[2], "1234567890123456\0", 17));
        draw_row(2, "OK");
        assert(!memcmp(previous[2], "OK              \0", 17));
    }
    return 0;
}
