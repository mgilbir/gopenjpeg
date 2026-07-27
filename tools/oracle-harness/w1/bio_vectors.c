/* Oracle harness (W1): dumps bio.c encode/decode vectors.
 *
 * Compile:
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w1/bio_vectors.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/bio_vectors
 *   /tmp/bio_vectors > testdata/vectors/bio/bio.txt
 *
 * Each case is two lines:
 *   SEQ <n1>:<v1> <n2>:<v2> ...   (n = bit count 1..32, v = value)
 *   ENC <hexbytes>                (opj_bio_write of the sequence, then flush)
 * A round-trip decode with opj_bio_read of the same n-sequence reproduces the
 * values, so the Go test checks both directions.
 */
#include "opj_includes.h"
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>

static uint32_t st;
static uint32_t nextrand(void)
{
    st = st * 1103515245u + 12345u;
    return st;
}

#define MAXSYM 256
static void emit_case(uint32_t seed, int count, int forced_n, uint32_t forced_v)
{
    OPJ_UINT32 ns[MAXSYM];
    OPJ_UINT32 vs[MAXSYM];
    OPJ_BYTE buf[MAXSYM * 8 + 16];
    opj_bio_t *bio;
    int i;
    ptrdiff_t nbytes;

    st = seed;
    for (i = 0; i < count; i++) {
        OPJ_UINT32 n, v;
        if (forced_n > 0) {
            n = (OPJ_UINT32)forced_n;
            v = forced_v;
        } else {
            n = 1u + (nextrand() % 32u);
            v = nextrand();
        }
        if (n < 32u) {
            v &= ((1u << n) - 1u);
        }
        ns[i] = n;
        vs[i] = v;
    }

    bio = opj_bio_create();
    opj_bio_init_enc(bio, buf, (OPJ_UINT32)count * 8u + 16u);
    for (i = 0; i < count; i++) {
        opj_bio_write(bio, vs[i], ns[i]);
    }
    opj_bio_flush(bio);
    nbytes = opj_bio_numbytes(bio);
    opj_bio_destroy(bio);

    printf("SEQ");
    for (i = 0; i < count; i++) {
        printf(" %u:%u", ns[i], vs[i]);
    }
    printf("\n");

    printf("ENC ");
    for (i = 0; i < (int)nbytes; i++) {
        printf("%02x", buf[i]);
    }
    printf("\n");
}

int main(void)
{
    int lens[] = { 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 100, 200 };
    int nl = (int)(sizeof(lens) / sizeof(lens[0]));
    int i;
    uint32_t seed = 0x1234567u;

    for (i = 0; i < nl; i++) {
        emit_case(seed, lens[i], 0, 0);
        seed = seed * 2654435761u + 1u;
    }

    /* Forced patterns to exercise the 0xFF stuffing rule aggressively. */
    emit_case(0, 64, 1, 1u);    /* 64 one-bits */
    emit_case(0, 40, 8, 0xFFu); /* many 0xFF bytes */
    emit_case(0, 20, 32, 0xFFFFFFFFu);
    emit_case(0, 30, 4, 0xFu);
    emit_case(0, 17, 1, 0u); /* 17 zero-bits */

    return 0;
}
