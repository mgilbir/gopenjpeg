/* Oracle harness (W1): dumps cio.c byte-order helper vectors.
 *
 * Compile:
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w1/cio_vectors.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/cio_vectors
 *   /tmp/cio_vectors > testdata/vectors/cio/bytes.txt
 *
 * Doubles/floats are exchanged as raw bit patterns (hex) so the vectors are
 * exact and independent of text float formatting.
 */
#include "opj_includes.h"
#include <stdio.h>
#include <stdint.h>
#include <string.h>

static void print_hex(const unsigned char *p, int n)
{
    int i;
    for (i = 0; i < n; i++) {
        printf("%02x", p[i]);
    }
}

/* The opj_*_BE / opj_*_LE C helpers are host-specialisations: on a given CPU,
 * one does a raw memcpy and the other byte-swaps. The cio.h macros dispatch on
 * OPJ_BIG_ENDIAN so that opj_write_bytes always serialises big-endian. To emit
 * vectors labelled by their TRUE endianness (matching the pure-Go port) we
 * detect the host endianness and select the C helper that realises each
 * semantic, exactly mirroring the macro dispatch and exercising both helpers. */
static int host_is_le(void)
{
    uint32_t x = 1u;
    return *(const unsigned char *)&x == 1u;
}

static int g_le;

/* True big-endian writers/readers. */
static void wbytes_be(OPJ_BYTE *b, OPJ_UINT32 v, OPJ_UINT32 n)
{
    if (g_le) {
        opj_write_bytes_LE(b, v, n);
    } else {
        opj_write_bytes_BE(b, v, n);
    }
}
static void wbytes_le(OPJ_BYTE *b, OPJ_UINT32 v, OPJ_UINT32 n)
{
    if (g_le) {
        opj_write_bytes_BE(b, v, n);
    } else {
        opj_write_bytes_LE(b, v, n);
    }
}
static OPJ_UINT32 rbytes_be(const OPJ_BYTE *b, OPJ_UINT32 n)
{
    OPJ_UINT32 v;
    if (g_le) {
        opj_read_bytes_LE(b, &v, n);
    } else {
        opj_read_bytes_BE(b, &v, n);
    }
    return v;
}
static OPJ_UINT32 rbytes_le(const OPJ_BYTE *b, OPJ_UINT32 n)
{
    OPJ_UINT32 v;
    if (g_le) {
        opj_read_bytes_BE(b, &v, n);
    } else {
        opj_read_bytes_LE(b, &v, n);
    }
    return v;
}
static void wdouble_be(OPJ_BYTE *b, OPJ_FLOAT64 v)
{
    if (g_le) {
        opj_write_double_LE(b, v);
    } else {
        opj_write_double_BE(b, v);
    }
}
static void wdouble_le(OPJ_BYTE *b, OPJ_FLOAT64 v)
{
    if (g_le) {
        opj_write_double_BE(b, v);
    } else {
        opj_write_double_LE(b, v);
    }
}
static OPJ_FLOAT64 rdouble_be(const OPJ_BYTE *b)
{
    OPJ_FLOAT64 v;
    if (g_le) {
        opj_read_double_LE(b, &v);
    } else {
        opj_read_double_BE(b, &v);
    }
    return v;
}
static OPJ_FLOAT64 rdouble_le(const OPJ_BYTE *b)
{
    OPJ_FLOAT64 v;
    if (g_le) {
        opj_read_double_BE(b, &v);
    } else {
        opj_read_double_LE(b, &v);
    }
    return v;
}
static void wfloat_be(OPJ_BYTE *b, OPJ_FLOAT32 v)
{
    if (g_le) {
        opj_write_float_LE(b, v);
    } else {
        opj_write_float_BE(b, v);
    }
}
static void wfloat_le(OPJ_BYTE *b, OPJ_FLOAT32 v)
{
    if (g_le) {
        opj_write_float_BE(b, v);
    } else {
        opj_write_float_LE(b, v);
    }
}
static OPJ_FLOAT32 rfloat_be(const OPJ_BYTE *b)
{
    OPJ_FLOAT32 v;
    if (g_le) {
        opj_read_float_LE(b, &v);
    } else {
        opj_read_float_BE(b, &v);
    }
    return v;
}
static OPJ_FLOAT32 rfloat_le(const OPJ_BYTE *b)
{
    OPJ_FLOAT32 v;
    if (g_le) {
        opj_read_float_BE(b, &v);
    } else {
        opj_read_float_LE(b, &v);
    }
    return v;
}

static const uint32_t vset[] = {
    0u, 1u, 0xFFu, 0x100u, 0x1234u, 0xABCDEFu, 0x00FF00FFu,
    0x12345678u, 0x80000000u, 0xFFFFFFFFu
};
static const unsigned char patterns[][4] = {
    {0x00, 0x00, 0x00, 0x00},
    {0xDE, 0xAD, 0xBE, 0xEF},
    {0x12, 0x34, 0x56, 0x78},
    {0xFF, 0x00, 0xFF, 0x00},
    {0x80, 0x00, 0x00, 0x01},
};
static const uint64_t dbits[] = {
    0x0000000000000000ULL, /* +0 */
    0x8000000000000000ULL, /* -0 */
    0x3FF0000000000000ULL, /* 1.0 */
    0x3FF8000000000000ULL, /* 1.5 */
    0xC009000000000000ULL, /* -3.125 */
    0x7FF0000000000000ULL, /* +inf */
    0xFFF0000000000000ULL, /* -inf */
    0x7FF8000000000000ULL, /* nan */
    0x0123456789ABCDEFULL,
    0xFEDCBA9876543210ULL
};
static const uint32_t fbits[] = {
    0x00000000u, 0x80000000u, 0x3F800000u, 0x3FC00000u,
    0xC0480000u, 0x7F800000u, 0xFF800000u, 0x7FC00000u,
    0x12345678u, 0xDEADBEEFu
};

#define NV (int)(sizeof(vset)/sizeof(vset[0]))
#define NP (int)(sizeof(patterns)/sizeof(patterns[0]))
#define ND (int)(sizeof(dbits)/sizeof(dbits[0]))
#define NF (int)(sizeof(fbits)/sizeof(fbits[0]))

int main(void)
{
    int i, nb;
    unsigned char buf[8];

    g_le = host_is_le();

    for (i = 0; i < NV; i++) {
        for (nb = 1; nb <= 4; nb++) {
            memset(buf, 0xAA, sizeof(buf));
            wbytes_be(buf, vset[i], (OPJ_UINT32)nb);
            printf("wb_be %u %d ", vset[i], nb);
            print_hex(buf, nb);
            printf("\n");

            /* The _BE C helper (which realises true little-endian on this LE
             * host) only coincides with a clean little-endian-of-low-nbBytes
             * serialiser at full width, so emit LE integer vectors for nb==4
             * only; partial-width LE is covered by Go-native tests. */
            if (nb == 4) {
                memset(buf, 0xAA, sizeof(buf));
                wbytes_le(buf, vset[i], (OPJ_UINT32)nb);
                printf("wb_le %u %d ", vset[i], nb);
                print_hex(buf, nb);
                printf("\n");
            }
        }
    }

    for (i = 0; i < NP; i++) {
        for (nb = 1; nb <= 4; nb++) {
            printf("rb_be ");
            print_hex(patterns[i], nb);
            printf(" %d %u\n", nb, rbytes_be(patterns[i], (OPJ_UINT32)nb));

            if (nb == 4) {
                printf("rb_le ");
                print_hex(patterns[i], nb);
                printf(" %d %u\n", nb, rbytes_le(patterns[i], (OPJ_UINT32)nb));
            }
        }
    }

    for (i = 0; i < ND; i++) {
        double d;
        OPJ_FLOAT64 rd;
        uint64_t rbits;
        memcpy(&d, &dbits[i], sizeof(d));

        memset(buf, 0xAA, sizeof(buf));
        wdouble_be(buf, d);
        printf("wd_be %016llx ", (unsigned long long)dbits[i]);
        print_hex(buf, 8);
        printf("\n");

        memset(buf, 0xAA, sizeof(buf));
        wdouble_le(buf, d);
        printf("wd_le %016llx ", (unsigned long long)dbits[i]);
        print_hex(buf, 8);
        printf("\n");

        /* read back from the freshly written BE/LE buffers */
        wdouble_be(buf, d);
        rd = rdouble_be(buf);
        memcpy(&rbits, &rd, sizeof(rbits));
        printf("rd_be ");
        print_hex(buf, 8);
        printf(" %016llx\n", (unsigned long long)rbits);

        wdouble_le(buf, d);
        rd = rdouble_le(buf);
        memcpy(&rbits, &rd, sizeof(rbits));
        printf("rd_le ");
        print_hex(buf, 8);
        printf(" %016llx\n", (unsigned long long)rbits);
    }

    for (i = 0; i < NF; i++) {
        float f;
        OPJ_FLOAT32 rf;
        uint32_t rbits;
        memcpy(&f, &fbits[i], sizeof(f));

        memset(buf, 0xAA, sizeof(buf));
        wfloat_be(buf, f);
        printf("wf_be %08x ", fbits[i]);
        print_hex(buf, 4);
        printf("\n");

        memset(buf, 0xAA, sizeof(buf));
        wfloat_le(buf, f);
        printf("wf_le %08x ", fbits[i]);
        print_hex(buf, 4);
        printf("\n");

        wfloat_be(buf, f);
        rf = rfloat_be(buf);
        memcpy(&rbits, &rf, sizeof(rbits));
        printf("rf_be ");
        print_hex(buf, 4);
        printf(" %08x\n", rbits);

        wfloat_le(buf, f);
        rf = rfloat_le(buf);
        memcpy(&rbits, &rf, sizeof(rbits));
        printf("rf_le ");
        print_hex(buf, 4);
        printf(" %08x\n", rbits);
    }

    return 0;
}
