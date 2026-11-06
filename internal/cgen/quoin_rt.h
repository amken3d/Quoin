/* quoin_rt.h - runtime support for C emitted by quoinc.
 *
 * Plain C99 plus GCC/Clang attributes where available. Everything is
 * static inline; nothing allocates. Checks that the compiler could not
 * discharge statically call ql_trap(), which is weak in the generated code
 * so firmware can supply its own (log, reset, breakpoint).
 *
 * SPDX-License-Identifier: MIT
 */
#ifndef QUOIN_RT_H
#define QUOIN_RT_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef uint16_t ql_err;

#if defined(__GNUC__) || defined(__clang__)
#define QL_INLINE inline __attribute__((always_inline))
#define QL_NORETURN __attribute__((noreturn))
#define QL_WEAK __attribute__((weak))
#define QL_ALIGN8 __attribute__((aligned(8)))
#define QL_DSTACK_SECTION __attribute__((section(".bss.quoin_dstack")))
/* Orders queue slot writes before index publication. On Cortex-M this is a
 * DMB, which also covers the RP2040/RP2350 second core. */
#define QL_BARRIER() __sync_synchronize()
#define QL_TRAP_BODY() __builtin_trap()
#define QL_HAVE_OVERFLOW_BUILTINS 1
#else
#define QL_INLINE inline
#define QL_NORETURN
#define QL_WEAK
#define QL_ALIGN8
#define QL_DSTACK_SECTION
#define QL_BARRIER() do { } while (0)
#define QL_TRAP_BODY() for (;;) { }
#endif

QL_NORETURN void ql_trap(void);

#define QL_CHECK(c) do { if (!(c)) ql_trap(); } while (0)
#define QL_STATIC_ASSERT(c, n) typedef char ql_static_assert_##n[(c) ? 1 : -1]

/* Range checks for arithmetic on widths up to 32 bits, done in 64 bits. */
static QL_INLINE int64_t ql_rng(int64_t v, int64_t lo, int64_t hi) {
    if (v < lo || v > hi) ql_trap();
    return v;
}
static QL_INLINE uint64_t ql_rngu(uint64_t v, uint64_t hi) {
    if (v > hi) ql_trap();
    return v;
}
static QL_INLINE int64_t ql_clamp(int64_t v, int64_t lo, int64_t hi) {
    return v < lo ? lo : (v > hi ? hi : v);
}
static QL_INLINE uint64_t ql_clampu(uint64_t v, uint64_t hi) {
    return v > hi ? hi : v;
}

/* Index and shift-amount checks. */
static QL_INLINE uint32_t ql_idx(uint32_t i, uint32_t n) {
    if (i >= n) ql_trap();
    return i;
}
static QL_INLINE uint32_t ql_shamt(uint32_t n, uint32_t bits) {
    if (n >= bits) ql_trap();
    return n;
}

/* Sign-extend the low `bits` bits of v (for iN with N not 8/16/32/64). */
static QL_INLINE int64_t ql_sext(uint64_t v, unsigned bits) {
    uint64_t m = (uint64_t)1 << (bits - 1);
    v &= ((uint64_t)1 << bits) - 1;
    return (int64_t)((v ^ m) - m);
}

/* Division: traps on divide by zero and on INT64_MIN / -1. */
static QL_INLINE int64_t ql_divs(int64_t a, int64_t b) {
    if (b == 0 || (a == INT64_MIN && b == -1)) ql_trap();
    return a / b;
}
static QL_INLINE int64_t ql_mods(int64_t a, int64_t b) {
    if (b == 0) ql_trap();
    if (b == -1) return 0;
    return a % b;
}
static QL_INLINE uint64_t ql_divu(uint64_t a, uint64_t b) {
    if (b == 0) ql_trap();
    return a / b;
}
static QL_INLINE uint64_t ql_modu(uint64_t a, uint64_t b) {
    if (b == 0) ql_trap();
    return a % b;
}

/* Checked and saturating arithmetic for widths 33..64. */
#ifdef QL_HAVE_OVERFLOW_BUILTINS
#define QL_OPS64(name, builtin)                                                         \
    static QL_INLINE int64_t ql_##name##_s64(int64_t a, int64_t b, int64_t lo, int64_t hi) { \
        int64_t r;                                                                       \
        if (builtin(a, b, &r) || r < lo || r > hi) ql_trap();                            \
        return r;                                                                        \
    }                                                                                    \
    static QL_INLINE uint64_t ql_##name##_u64(uint64_t a, uint64_t b, uint64_t hi) {     \
        uint64_t r;                                                                      \
        if (builtin(a, b, &r) || r > hi) ql_trap();                                      \
        return r;                                                                        \
    }                                                                                    \
    static QL_INLINE int64_t ql_s##name##_s64(int64_t a, int64_t b, int64_t lo, int64_t hi) { \
        int64_t r;                                                                       \
        if (builtin(a, b, &r)) {                                                         \
            /* overflow direction follows the exact result's sign */                     \
            int neg = #name[0] == 'a' ? (a < 0) : #name[0] == 's' ? (a < 0 && b > 0)     \
                                                                  : ((a < 0) != (b < 0)); \
            return neg ? lo : hi;                                                        \
        }                                                                                \
        return r < lo ? lo : (r > hi ? hi : r);                                          \
    }                                                                                    \
    static QL_INLINE uint64_t ql_s##name##_u64(uint64_t a, uint64_t b, uint64_t hi) {    \
        uint64_t r;                                                                      \
        if (builtin(a, b, &r)) return #name[0] == 's' ? 0 : hi;                          \
        return r > hi ? hi : r;                                                          \
    }
QL_OPS64(add, __builtin_add_overflow)
QL_OPS64(sub, __builtin_sub_overflow)
QL_OPS64(mul, __builtin_mul_overflow)
#undef QL_OPS64
#endif

#endif /* QUOIN_RT_H */
