// SPDX-License-Identifier: GPL-2.0-only
#define _POSIX_C_SOURCE 200809L
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/input.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/prctl.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>
#include "one-button.h"

#define NBITS (KEY_MAX / (8 * sizeof(unsigned long)) + 1)
static int has_key(const unsigned long *bits, unsigned key)
{
    return !!(bits[key / (8 * sizeof(*bits))] & (1ul << (key % (8 * sizeof(*bits)))));
}
static uint32_t now_ms(void)
{
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint32_t)((uint64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000);
}
struct input {
    int fd, dropped, count, down[2];
    unsigned codes[2];
    struct a8_button buttons[2];
};
static volatile sig_atomic_t stopping;
static void stop(int signo) { (void)signo; stopping = 1; }

static int lcd_ready(void)
{
    struct stat st;
    return access("/sys/class/graphics/fb0/name", R_OK) == 0 &&
        stat("/dev/tty1", &st) == 0 && S_ISCHR(st.st_mode);
}
static int open_input(int side, int grab)
{
    DIR *dir = opendir("/dev/input");
    struct dirent *entry;
    int found = -1;
    if (!dir) return -1;
    while ((entry = readdir(dir))) {
        unsigned long bits[NBITS] = {0};
        char path[320], name[128] = {0};
        int fd;
        if (strncmp(entry->d_name, "event", 5)) continue;
        snprintf(path, sizeof(path), "/dev/input/%s", entry->d_name);
        fd = open(path, O_RDONLY | O_NONBLOCK | O_CLOEXEC);
        if (fd < 0) continue;
        if (ioctl(fd, EVIOCGNAME(sizeof(name)), name) >= 0 &&
            !strcmp(name, side ? "gpio-keys" : "pm8941_pwrkey") &&
            ioctl(fd, EVIOCGBIT(EV_KEY, sizeof(bits)), bits) >= 0 &&
            (side ? has_key(bits, KEY_VOLUMEUP) && has_key(bits, KEY_VOLUMEDOWN) : has_key(bits, KEY_POWER)) &&
            (!grab || ioctl(fd, EVIOCGRAB, 1) == 0)) {
            found = fd;
            break;
        }
        close(fd);
    }
    closedir(dir);
    return found;
}

static int resync(struct input *input)
{
    unsigned long bits[NBITS] = {0};
    uint32_t now = now_ms();
    if (ioctl(input->fd, EVIOCGKEY(sizeof(bits)), bits) < 0) return -1;
    for (int i = 0; i < input->count; ++i) {
        input->down[i] = has_key(bits, input->codes[i]);
        a8_button_reset(&input->buttons[i], input->down[i], now);
    }
    input->dropped = 0;
    return 0;
}
static void poll_keys(struct input *input, int side, uint32_t now)
{
    for (int i = 0; i < input->count; ++i) {
        struct a8_button *b = &input->buttons[i];
        if (side) {
            if (a8_navigation_poll(b, input->down[i], now)) puts(i ? "down" : "up");
        } else {
            int was_down = b->stable, armed = b->armed;
            enum a8_button_event event = a8_button_poll(b, input->down[i], now);
            if (!was_down && b->stable) puts("pressed");
            if (event) puts(event == A8_LONG ? "long" : "short");
            if (was_down && !b->stable && armed)
                printf("released:%u\n", (unsigned)(b->changed - b->pressed));
        }
    }
}
static void close_input(struct input *input)
{
    if (input->fd >= 0) close(input->fd);
    input->fd = -1;
}
static int read_input(struct input *input, int side)
{
    struct input_event events[32];
    ssize_t size;
    while ((size = read(input->fd, events, sizeof(events))) > 0) {
        for (size_t i = 0; i < (size_t)size / sizeof(*events); ++i) {
            struct input_event *e = &events[i];
            if (e->type == EV_SYN && e->code == SYN_DROPPED) {
                input->dropped = 1; puts("resync"); continue;
            }
            if (input->dropped) {
                if (e->type == EV_SYN && e->code == SYN_REPORT && resync(input)) return -1;
            } else if (e->type == EV_KEY && e->value != 2) {
                for (int k = 0; k < input->count; ++k)
                    if (e->code == input->codes[k]) input->down[k] = !!e->value;
                poll_keys(input, side, now_ms());
            }
        }
    }
    return !size || (size < 0 && errno != EAGAIN && errno != EINTR) ? -1 : 0;
}

int main(int argc, char **argv)
{
    struct input inputs[2] = {
        {.fd=-1, .count=1, .codes={KEY_POWER}},
        {.fd=-1, .count=2, .codes={KEY_VOLUMEUP, KEY_VOLUMEDOWN}}
    };
    uint32_t retry = now_ms() - 5000, tick = now_ms();
    int want_side = 0, mode = -1, display = -1;
    if (argc == 2 && !strcmp(argv[1], "--probe")) {
        int power = open_input(0, 0), side = open_input(1, 0);
        printf("{\"power\":%s,\"side\":%s,\"lcd_ready\":%s}\n",
               power >= 0 ? "true" : "false", side >= 0 ? "true" : "false", lcd_ready() ? "true" : "false");
        if (power >= 0) close(power);
        if (side >= 0) close(side);
        return 0;
    }
    if (argc == 2 && !strcmp(argv[1], "--side")) want_side = 1;
    else if (argc != 1) return 2;
    if (prctl(PR_SET_PDEATHSIG, SIGTERM) || getppid() == 1) return 1;
    signal(SIGTERM, stop); signal(SIGINT, stop);
    setvbuf(stdout, NULL, _IOLBF, 0);
    while (!stopping) {
        uint32_t now = now_ms();
        struct pollfd p[2];
        int ready = lcd_ready();
        if (ready != display) {
            display = ready; puts(ready ? "lcd-ready" : "lcd-missing");
        }
        if (inputs[1].fd >= 0 && (!ready || inputs[0].fd < 0)) close_input(&inputs[1]);
        if ((uint32_t)(now - retry) >= 5000) {
            retry = now;
            for (int i = 0; i < 2; ++i) {
                if (inputs[i].fd >= 0 || (i && (!want_side || !ready || inputs[0].fd < 0))) continue;
                inputs[i].fd = open_input(i, 1);
                if (inputs[i].fd >= 0 && resync(&inputs[i])) close_input(&inputs[i]);
                if (!i) puts(inputs[i].fd >= 0 ? "connected" : "disconnected");
            }
        }
        if ((inputs[1].fd >= 0) != mode) {
            mode = inputs[1].fd >= 0; puts(mode ? "mode-side" : "mode-power");
        }
        for (int i = 0; i < 2; ++i) p[i] = (struct pollfd){.fd=inputs[i].fd, .events=POLLIN};
        if (poll(p, 2, 10) < 0 && errno != EINTR) break;
        for (int i = 0; i < 2; ++i) {
            if (inputs[i].fd >= 0 && (p[i].revents & (POLLIN | POLLERR | POLLHUP | POLLNVAL))) {
                if (read_input(&inputs[i], i) || (p[i].revents & (POLLERR | POLLHUP | POLLNVAL))) {
                    close_input(&inputs[i]);
                    puts(i ? "resync" : "disconnected");
                }
            }
            if (inputs[i].fd >= 0 && !inputs[i].dropped) poll_keys(&inputs[i], i, now_ms());
        }
        now = now_ms();
        if ((uint32_t)(now - tick) >= 1000) { tick = now; puts("tick"); }
        if (ferror(stdout)) break;
    }
    close_input(&inputs[0]); close_input(&inputs[1]);
    return stopping ? 0 : 1;
}
