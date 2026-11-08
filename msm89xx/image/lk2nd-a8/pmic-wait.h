/* SPDX-License-Identifier: BSD-3-Clause */
#ifndef A8_PMIC_WAIT_H
#define A8_PMIC_WAIT_H

#if SPMI_CORE_V2
/* Unmapped PPIDs must never select a different peripheral's write channel. */
static inline int a8_pmic_channel_matches(uint32_t channel, uint32_t count,
        unsigned sid, unsigned peripheral)
{
    if (channel >= count ||
        (readl(PMIC_ARB_REG_CHLN(channel)) & 0xfff00) !=
        ((sid << 16) | (peripheral << 8))) {
        dprintf(CRITICAL, "A8 SPMI: no channel for %u:%02x\n", sid, peripheral);
        return 0;
    }
    return 1;
}
#endif

/* A missing/denied PMIC channel must not strand the device before USB starts.
 * Normal SPMI transfers finish in microseconds; allow up to 10 ms per command.
 * Callers keep the existing nonzero-error convention and can skip the LCD.
 */
static inline int a8_pmic_wait_status(uint32_t address, uint32_t *status)
{
    unsigned remaining = 10000;
    do {
        *status = readl(address);
        if (*status) return 1;
        udelay(1);
    } while (--remaining);
    dprintf(CRITICAL, "A8 SPMI: timeout at 0x%x\n", address);
    return 0;
}
#endif
