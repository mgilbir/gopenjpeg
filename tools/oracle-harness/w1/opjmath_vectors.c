/* Oracle harness (W1): dumps opj_intmath.h edge-case vectors.
 *
 * Compile:
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w1/opjmath_vectors.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/opjmath_vectors
 *   /tmp/opjmath_vectors > testdata/vectors/opjmath/intmath.txt
 *
 * Line format: "<name> <arg...> <result>" with type-appropriate decimals.
 */
#include "opj_includes.h"
#include <stdio.h>
#include <stdint.h>

static const int32_t iset[] = {
    INT32_MIN, INT32_MIN + 1, -65536, -4097, -4096, -3, -2, -1,
    0, 1, 2, 3, 4096, 65536, 0x7FFFFFFE, 0x7FFFFFFF
};
static const uint32_t uset[] = {
    0u, 1u, 2u, 3u, 4095u, 4096u, 4097u, 65535u, 65536u,
    0x7FFFFFFFu, 0x80000000u, 0xFFFFFFFFu
};
static const int32_t bset[] = { 0, 1, 2, 3, 13, 16, 30, 31 };
static const int32_t cset[] = { INT32_MIN, -100, -1, 0, 1, 100, INT32_MAX };
static const int64_t i64set[] = {
    INT64_MIN, -0x100000000LL, -65536LL, -1LL, 0LL, 1LL, 4096LL, 65536LL,
    0x7FFFFFFFLL, 0x80000000LL, 0x100000000LL, 0x7FFFFFFFFFFFFFFFLL
};
static const uint64_t u64set[] = {
    0u, 1u, 3u, 4096u, 65536u, 0xFFFFFFFEu, 0xFFFFFFFFu,
    0x100000000ULL, 0x7FFFFFFFFFFFFFFFULL, 0xFFFFFFFFFFFFFFFFULL
};

#define NI (int)(sizeof(iset)/sizeof(iset[0]))
#define NU (int)(sizeof(uset)/sizeof(uset[0]))
#define NB (int)(sizeof(bset)/sizeof(bset[0]))
#define NC (int)(sizeof(cset)/sizeof(cset[0]))
#define NI64 (int)(sizeof(i64set)/sizeof(i64set[0]))
#define NU64 (int)(sizeof(u64set)/sizeof(u64set[0]))

int main(void)
{
    int a, b, c;

    for (a = 0; a < NI; a++) {
        for (b = 0; b < NI; b++) {
            printf("int_min %d %d %d\n", iset[a], iset[b], opj_int_min(iset[a], iset[b]));
            printf("int_max %d %d %d\n", iset[a], iset[b], opj_int_max(iset[a], iset[b]));
            printf("int_fix_mul %d %d %d\n", iset[a], iset[b], opj_int_fix_mul(iset[a], iset[b]));
            printf("int_fix_mul_t1 %d %d %d\n", iset[a], iset[b], opj_int_fix_mul_t1(iset[a], iset[b]));
            printf("int_add_no_overflow %d %d %d\n", iset[a], iset[b], opj_int_add_no_overflow(iset[a], iset[b]));
            printf("int_sub_no_overflow %d %d %d\n", iset[a], iset[b], opj_int_sub_no_overflow(iset[a], iset[b]));
            if (iset[b] != 0) {
                printf("int_ceildiv %d %d %d\n", iset[a], iset[b], opj_int_ceildiv(iset[a], iset[b]));
            }
        }
    }

    for (a = 0; a < NU; a++) {
        for (b = 0; b < NU; b++) {
            printf("uint_min %u %u %u\n", uset[a], uset[b], opj_uint_min(uset[a], uset[b]));
            printf("uint_max %u %u %u\n", uset[a], uset[b], opj_uint_max(uset[a], uset[b]));
            printf("uint_adds %u %u %u\n", uset[a], uset[b], opj_uint_adds(uset[a], uset[b]));
            printf("uint_subs %u %u %u\n", uset[a], uset[b], opj_uint_subs(uset[a], uset[b]));
            if (uset[b] != 0) {
                printf("uint_ceildiv %u %u %u\n", uset[a], uset[b], opj_uint_ceildiv(uset[a], uset[b]));
            }
        }
    }

    for (a = 0; a < NC; a++) {
        for (b = 0; b < NC; b++) {
            for (c = 0; c < NC; c++) {
                printf("int_clamp %d %d %d %d\n", cset[a], cset[b], cset[c],
                       opj_int_clamp(cset[a], cset[b], cset[c]));
                printf("int64_clamp %lld %lld %lld %lld\n",
                       (long long)cset[a], (long long)cset[b], (long long)cset[c],
                       (long long)opj_int64_clamp(cset[a], cset[b], cset[c]));
            }
        }
    }

    for (a = 0; a < NI; a++) {
        printf("int_abs %d %d\n", iset[a], opj_int_abs(iset[a]));
        printf("int_floorlog2 %d %d\n", iset[a], opj_int_floorlog2(iset[a]));
    }
    for (a = 0; a < NU; a++) {
        printf("uint_floorlog2 %u %u\n", uset[a], opj_uint_floorlog2(uset[a]));
    }

    for (a = 0; a < NI; a++) {
        for (b = 0; b < NB; b++) {
            printf("int_ceildivpow2 %d %d %d\n", iset[a], bset[b], opj_int_ceildivpow2(iset[a], bset[b]));
            printf("int_floordivpow2 %d %d %d\n", iset[a], bset[b], opj_int_floordivpow2(iset[a], bset[b]));
        }
    }
    for (a = 0; a < NU; a++) {
        for (b = 0; b < NB; b++) {
            printf("uint_ceildivpow2 %u %u %u\n", uset[a], (uint32_t)bset[b], opj_uint_ceildivpow2(uset[a], (uint32_t)bset[b]));
            printf("uint_floordivpow2 %u %u %u\n", uset[a], (uint32_t)bset[b], opj_uint_floordivpow2(uset[a], (uint32_t)bset[b]));
        }
    }
    for (a = 0; a < NI64; a++) {
        for (b = 0; b < NB; b++) {
            printf("int64_ceildivpow2 %lld %d %d\n", (long long)i64set[a], bset[b],
                   opj_int64_ceildivpow2(i64set[a], bset[b]));
        }
    }
    for (a = 0; a < NU64; a++) {
        for (b = 0; b < NU64; b++) {
            if (u64set[b] != 0) {
                printf("uint64_ceildiv_res_uint32 %llu %llu %u\n",
                       (unsigned long long)u64set[a], (unsigned long long)u64set[b],
                       opj_uint64_ceildiv_res_uint32(u64set[a], u64set[b]));
            }
        }
    }

    return 0;
}
