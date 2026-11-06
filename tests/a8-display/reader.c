// SPDX-License-Identifier: GPL-2.0-only
// Compile the real reader against simulated evdev devices, never /dev/input.
#define main a8_reader_main
#define opendir test_opendir
#define readdir test_readdir
#define closedir test_closedir
#define open test_open
#define close test_close
#define ioctl test_ioctl
#define read test_read
#define clock_gettime test_clock_gettime
#include "../../packages/a8-display/src/a8-button.c"
#undef main
#include <assert.h>
#include <stdarg.h>

static struct {
    const char *name;
    unsigned keys[3];
    int grab_failed, held;
} devices[] = {
    {.name="USB keyboard", .keys={KEY_POWER}},
    {.name="pm8941_pwrkey", .keys={KEY_POWER}},
    {.name="gpio-keys", .keys={KEY_RESTART, KEY_F6}},
    {.name="gpio-keys", .keys={KEY_VOLUMEUP, KEY_VOLUMEDOWN}}
};
static int inventory=4, position, read_eof;
static uint32_t milliseconds;
static struct input_event queued[8];
static size_t queued_count;

DIR *test_opendir(const char *name) { assert(!strcmp(name, "/dev/input")); position=0; return (DIR *)devices; }
struct dirent *test_readdir(DIR *dir)
{
    static struct dirent entry;
    assert(dir == (DIR *)devices);
    if (position >= inventory) return NULL;
    snprintf(entry.d_name, sizeof(entry.d_name), "event%d", position++);
    return &entry;
}
int test_closedir(DIR *dir) { assert(dir == (DIR *)devices); return 0; }
int test_open(const char *path, int flags, ...)
{
    int index=-1;
    (void)flags;
    assert(sscanf(path, "/dev/input/event%d", &index) == 1 && index >= 0 && index < inventory);
    return 20+index;
}
int test_close(int fd) { assert(fd >= 20 && fd < 24); return 0; }
int test_clock_gettime(clockid_t id, struct timespec *ts)
{
    assert(id == CLOCK_MONOTONIC);
    ts->tv_sec=milliseconds/1000; ts->tv_nsec=(milliseconds%1000)*1000000;
    return 0;
}
int test_ioctl(int fd, unsigned long request, ...)
{
    va_list ap;
    int index=fd-20;
    assert(index >= 0 && index < inventory);
    va_start(ap, request);
    if (request == EVIOCGRAB) {
        assert(va_arg(ap,int) == 1);
        va_end(ap); return devices[index].grab_failed ? -1 : 0;
    }
    void *buffer=va_arg(ap,void *);
    va_end(ap);
    size_t size=_IOC_SIZE(request);
    memset(buffer,0,size);
    if (_IOC_NR(request) == _IOC_NR(EVIOCGNAME(0))) {
        snprintf(buffer,size,"%s",devices[index].name);
    } else {
        unsigned long *bits=buffer;
        assert(_IOC_NR(request) == _IOC_NR(EVIOCGBIT(EV_KEY,0)) || _IOC_NR(request) == _IOC_NR(EVIOCGKEY(0)));
        for (size_t i=0;i<3;++i) {
            unsigned key=devices[index].keys[i];
            if (key && (_IOC_NR(request) != _IOC_NR(EVIOCGKEY(0)) || devices[index].held))
                bits[key/(8*sizeof(*bits))] |= 1ul << (key%(8*sizeof(*bits)));
        }
    }
    return 0;
}
ssize_t test_read(int fd, void *buffer, size_t size)
{
    (void)fd;
    if (!queued_count) { errno=EAGAIN; return read_eof ? 0 : -1; }
    assert(size >= queued_count*sizeof(*queued));
    size=queued_count*sizeof(*queued); memcpy(buffer,queued,size); queued_count=0;
    return (ssize_t)size;
}
static FILE *capture;
static int saved_stdout;
static void begin(void)
{
    fflush(stdout); saved_stdout=dup(STDOUT_FILENO);
    capture=tmpfile(); assert(capture && saved_stdout >= 0);
    assert(dup2(fileno(capture),STDOUT_FILENO) >= 0);
}
static void expect(const char *text)
{
    char actual[256]={0};
    fflush(stdout); assert(dup2(saved_stdout,STDOUT_FILENO) >= 0);
    // test_close intercepts only the simulated input descriptors.
    fclose(fdopen(saved_stdout,"w"));
    rewind(capture); size_t n=fread(actual,1,sizeof(actual)-1,capture); actual[n]=0;
    fclose(capture); assert(!strcmp(text,actual));
}
static void step(struct input *input, int side, int down, uint32_t now)
{
    milliseconds=now; input->down[0]=down; poll_keys(input,side,now);
}
int main(void)
{
    assert(open_input(0,1) == 21);
    assert(open_input(1,1) == 23);
    devices[3].grab_failed=1;
    assert(open_input(1,1) == -1 && open_input(1,0) == 23);
    inventory=3; assert(open_input(1,0) == -1); inventory=4;
    struct input power={.fd=21,.count=1,.codes={KEY_POWER}};
    assert(!resync(&power));
    begin(); step(&power,0,1,100); step(&power,0,1,130);
    step(&power,0,0,200); step(&power,0,0,230);
    expect("pressed\nshort\nreleased:100\n");
    begin(); step(&power,0,1,500); step(&power,0,1,530); step(&power,0,1,1500);
    step(&power,0,0,2000); step(&power,0,0,2030);
    expect("pressed\nlong\nreleased:1500\n");
    devices[1].held=1; assert(!resync(&power));
    begin(); step(&power,0,1,10000); step(&power,0,0,10100); step(&power,0,0,10130);
    expect("");
    queued[0]=(struct input_event){.type=EV_SYN,.code=SYN_DROPPED};
    queued[1]=(struct input_event){.type=EV_KEY,.code=KEY_POWER,.value=0};
    queued[2]=(struct input_event){.type=EV_SYN,.code=SYN_REPORT};
    queued[3]=(struct input_event){.type=EV_KEY,.code=KEY_POWER,.value=2};
    queued_count=4;
    begin(); assert(!read_input(&power,0)); step(&power,0,1,20000);
    step(&power,0,0,20100); step(&power,0,0,20130); expect("resync\n");
    read_eof=1; assert(read_input(&power,0) == -1); read_eof=0;
    struct input side={.fd=23,.count=2,.codes={KEY_VOLUMEUP,KEY_VOLUMEDOWN}};
    assert(!resync(&side));
    begin(); step(&side,1,1,20200); step(&side,1,1,20230); step(&side,1,1,22000);
    step(&side,1,0,22100); step(&side,1,0,22130);
    side.down[1]=1; poll_keys(&side,1,22200); poll_keys(&side,1,22230);
    expect("up\ndown\n");
    puts("evdev device matching, short/long release, overflow and side navigation passed");
    return 0;
}
