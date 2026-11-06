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
static int pressed(int fd)
{
    unsigned long bits[NBITS] = {0};
    if (ioctl(fd, EVIOCGKEY(sizeof(bits)), bits) < 0) return -1;
    return has_key(bits, KEY_POWER);
}
static int open_power(void)
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
            !strcmp(name, "pm8941_pwrkey") &&
            ioctl(fd, EVIOCGBIT(EV_KEY, sizeof(bits)), bits) >= 0 &&
            has_key(bits, KEY_POWER) && ioctl(fd, EVIOCGRAB, 1) == 0) {
            found = fd;
            break;
        }
        close(fd);
    }
    closedir(dir);
    return found;
}

int main(void)
{
    struct a8_button button;
    uint32_t retry = now_ms() - 5000, tick = now_ms();
    int fd = -1, down = 0, dropped = 0;
    if (prctl(PR_SET_PDEATHSIG, SIGTERM) || getppid() == 1) return 1;
    setvbuf(stdout, NULL, _IOLBF, 0);
    a8_button_reset(&button, 0, tick);
    for (;;) {
        uint32_t now = now_ms();
        struct pollfd p = {.fd = fd, .events = POLLIN};
        enum a8_button_event event;
        if (fd < 0 && (uint32_t)(now - retry) >= 5000) {
            retry = now;
            fd = open_power();
            if (fd >= 0) {
                down = pressed(fd);
                if (down < 0) { close(fd); fd = -1; }
                else { a8_button_reset(&button, down, now); dropped = 0; }
            }
            puts(fd >= 0 ? "connected" : "disconnected");
            p.fd = fd;
        }
        if (poll(&p, 1, 10) < 0 && errno != EINTR) return 1;
        if (fd >= 0 && (p.revents & (POLLIN | POLLERR | POLLHUP | POLLNVAL))) {
            struct input_event events[32];
            ssize_t size;
            while ((size = read(fd, events, sizeof(events))) > 0) {
                for (size_t i = 0; i < (size_t)size / sizeof(*events); ++i) {
                    struct input_event *e = &events[i];
                    if (e->type == EV_SYN && e->code == SYN_DROPPED) { dropped = 1; continue; }
                    if (dropped) {
                        if (e->type == EV_SYN && e->code == SYN_REPORT) {
                            down = pressed(fd);
                            if (down < 0) break;
                            a8_button_reset(&button, down, now_ms());
                            dropped = 0;
                        }
                    } else if (e->type == EV_KEY && e->code == KEY_POWER && e->value != 2) {
                        down = !!e->value;
                        /* The separate reader never blocks on modem queries. */
                        event = a8_button_poll(&button, down, now_ms());
                        if (event) puts(event == A8_LONG ? "long" : "short");
                    }
                }
            }
            if (!size || down < 0 || (size < 0 && errno != EAGAIN && errno != EINTR) ||
                (p.revents & (POLLERR | POLLHUP | POLLNVAL))) {
                close(fd); fd = -1; puts("disconnected");
            }
        }
        now = now_ms();
        if (fd >= 0 && !dropped) {
            event = a8_button_poll(&button, down, now);
            if (event) puts(event == A8_LONG ? "long" : "short");
        }
        if ((uint32_t)(now - tick) >= 1000) { tick = now; puts("tick"); }
        if (ferror(stdout)) return 1;
    }
}
