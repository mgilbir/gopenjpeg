/* W4 oracle harness: dumps MCT / custom-MCT / matrix-inversion vectors as JSON.
 *
 * Build (from repo root):
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w4/mct_gen.c oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread \
 *       -o /tmp/mct_gen
 *   /tmp/mct_gen > testdata/vectors/mct/vectors.json
 *
 * Float32 values are emitted as their raw uint32 bit pattern (decimal) so the
 * Go tests can compare bit-for-bit.
 */
#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include <stdlib.h>

#include "opj_includes.h"
#include "mct.h"
#include "invert.h"

/* deterministic xorshift64 PRNG for reproducible vectors */
static uint64_t rng_state = 0x123456789abcdef0ULL;
static uint32_t next_u32(void)
{
    uint64_t x = rng_state;
    x ^= x << 13;
    x ^= x >> 7;
    x ^= x << 17;
    rng_state = x;
    return (uint32_t)(x >> 32);
}

/* int32 in [-range, range] */
static int32_t rand_i32(int32_t range)
{
    uint32_t r = next_u32();
    int64_t span = (int64_t)range * 2 + 1;
    return (int32_t)((int64_t)(r % (uint64_t)span) - range);
}

/* float32 in [-1,1] scaled by mag */
static float rand_f32(float mag)
{
    uint32_t r = next_u32();
    double u = (double)r / (double)0xffffffffu; /* [0,1] */
    return (float)((u * 2.0 - 1.0) * mag);
}

static uint32_t f2u(float f)
{
    uint32_t u;
    memcpy(&u, &f, sizeof(u));
    return u;
}

static void print_i32_array(const int32_t *a, int n)
{
    printf("[");
    for (int i = 0; i < n; i++) {
        printf("%s%d", i ? "," : "", a[i]);
    }
    printf("]");
}

static void print_fbits_array(const float *a, int n)
{
    printf("[");
    for (int i = 0; i < n; i++) {
        printf("%s%u", i ? "," : "", f2u(a[i]));
    }
    printf("]");
}

int main(void)
{
    printf("{\n");

    /* ---------- RCT (reversible, int32) ---------- */
    {
        const int N = 64;
        int32_t c0[64], c1[64], c2[64];
        int32_t e0[64], e1[64], e2[64];
        int32_t d0[64], d1[64], d2[64];
        /* moderate range to avoid signed-overflow UB in the reversible math */
        for (int i = 0; i < N; i++) {
            c0[i] = rand_i32(1 << 20);
            c1[i] = rand_i32(1 << 20);
            c2[i] = rand_i32(1 << 20);
        }
        /* a few explicit small/negative/edge triples */
        c0[0] = 0;         c1[0] = 0;        c2[0] = 0;
        c0[1] = 255;       c1[1] = 128;      c2[1] = 64;
        c0[2] = -100000;   c1[2] = 100000;   c2[2] = -1;
        c0[3] = -1;        c1[3] = -1;       c2[3] = -1;

        memcpy(e0, c0, sizeof(e0));
        memcpy(e1, c1, sizeof(e1));
        memcpy(e2, c2, sizeof(e2));
        opj_mct_encode(e0, e1, e2, (OPJ_SIZE_T)N);

        memcpy(d0, c0, sizeof(d0));
        memcpy(d1, c1, sizeof(d1));
        memcpy(d2, c2, sizeof(d2));
        opj_mct_decode(d0, d1, d2, (OPJ_SIZE_T)N);

        printf("  \"rct\": {\"n\": %d,\n", N);
        printf("    \"c0\": "); print_i32_array(c0, N); printf(",\n");
        printf("    \"c1\": "); print_i32_array(c1, N); printf(",\n");
        printf("    \"c2\": "); print_i32_array(c2, N); printf(",\n");
        printf("    \"enc_c0\": "); print_i32_array(e0, N); printf(",\n");
        printf("    \"enc_c1\": "); print_i32_array(e1, N); printf(",\n");
        printf("    \"enc_c2\": "); print_i32_array(e2, N); printf(",\n");
        printf("    \"dec_c0\": "); print_i32_array(d0, N); printf(",\n");
        printf("    \"dec_c1\": "); print_i32_array(d1, N); printf(",\n");
        printf("    \"dec_c2\": "); print_i32_array(d2, N); printf("\n");
        printf("  },\n");
    }

    /* ---------- ICT (irreversible, float32) ---------- */
    {
        const int N = 64;
        float c0[64], c1[64], c2[64];
        float e0[64], e1[64], e2[64];
        float d0[64], d1[64], d2[64];
        for (int i = 0; i < N; i++) {
            c0[i] = rand_f32(1000.0f);
            c1[i] = rand_f32(1000.0f);
            c2[i] = rand_f32(1000.0f);
        }
        c0[0] = 0.0f;    c1[0] = 0.0f;    c2[0] = 0.0f;
        c0[1] = 255.0f;  c1[1] = 128.0f;  c2[1] = 64.0f;

        memcpy(e0, c0, sizeof(e0));
        memcpy(e1, c1, sizeof(e1));
        memcpy(e2, c2, sizeof(e2));
        opj_mct_encode_real(e0, e1, e2, (OPJ_SIZE_T)N);

        memcpy(d0, c0, sizeof(d0));
        memcpy(d1, c1, sizeof(d1));
        memcpy(d2, c2, sizeof(d2));
        opj_mct_decode_real(d0, d1, d2, (OPJ_SIZE_T)N);

        printf("  \"ict\": {\"n\": %d,\n", N);
        printf("    \"c0\": "); print_fbits_array(c0, N); printf(",\n");
        printf("    \"c1\": "); print_fbits_array(c1, N); printf(",\n");
        printf("    \"c2\": "); print_fbits_array(c2, N); printf(",\n");
        printf("    \"enc_c0\": "); print_fbits_array(e0, N); printf(",\n");
        printf("    \"enc_c1\": "); print_fbits_array(e1, N); printf(",\n");
        printf("    \"enc_c2\": "); print_fbits_array(e2, N); printf(",\n");
        printf("    \"dec_c0\": "); print_fbits_array(d0, N); printf(",\n");
        printf("    \"dec_c1\": "); print_fbits_array(d1, N); printf(",\n");
        printf("    \"dec_c2\": "); print_fbits_array(d2, N); printf("\n");
        printf("  },\n");
    }

    /* ---------- custom MCT (encode int32, decode float32) ---------- */
    {
        int nbcomps[] = {3, 4, 5, 6, 3};
        int ncases = (int)(sizeof(nbcomps) / sizeof(nbcomps[0]));
        const int MAXNB = 6;
        const int N = 16;
        printf("  \"custom\": [\n");
        for (int cse = 0; cse < ncases; cse++) {
            uint32_t nb = (uint32_t)nbcomps[cse];
            int n = N;
            uint32_t ncoeff = nb * nb;

            float matrix[36];
            for (uint32_t i = 0; i < ncoeff; i++) {
                matrix[i] = rand_f32(2.0f);
            }

            /* encode: int32 component data */
            int32_t encData[6][16];
            int32_t encOrig[6][16];
            int32_t *encPtrs[6];
            for (uint32_t j = 0; j < nb; j++) {
                for (int i = 0; i < n; i++) {
                    int32_t v = rand_i32(1000);
                    encData[j][i] = v;
                    encOrig[j][i] = v;
                }
                encPtrs[j] = encData[j];
            }
            opj_mct_encode_custom((OPJ_BYTE *)matrix, (OPJ_SIZE_T)n,
                                  (OPJ_BYTE **)encPtrs, nb, 0);

            /* decode: float32 component data */
            float decData[6][16];
            float decOrig[6][16];
            float *decPtrs[6];
            for (uint32_t j = 0; j < nb; j++) {
                for (int i = 0; i < n; i++) {
                    float v = rand_f32(500.0f);
                    decData[j][i] = v;
                    decOrig[j][i] = v;
                }
                decPtrs[j] = decData[j];
            }
            opj_mct_decode_custom((OPJ_BYTE *)matrix, (OPJ_SIZE_T)n,
                                  (OPJ_BYTE **)decPtrs, nb, 0);

            (void)MAXNB;
            printf("    {\"nbcomp\": %u, \"n\": %d,\n", nb, n);
            printf("      \"matrix\": "); print_fbits_array(matrix, (int)ncoeff); printf(",\n");
            printf("      \"enc_in\": [");
            for (uint32_t j = 0; j < nb; j++) {
                if (j) printf(",");
                print_i32_array(encOrig[j], n);
            }
            printf("],\n");
            printf("      \"enc_out\": [");
            for (uint32_t j = 0; j < nb; j++) {
                if (j) printf(",");
                print_i32_array(encData[j], n);
            }
            printf("],\n");
            printf("      \"dec_in\": [");
            for (uint32_t j = 0; j < nb; j++) {
                if (j) printf(",");
                print_fbits_array(decOrig[j], n);
            }
            printf("],\n");
            printf("      \"dec_out\": [");
            for (uint32_t j = 0; j < nb; j++) {
                if (j) printf(",");
                print_fbits_array(decData[j], n);
            }
            printf("]\n");
            printf("    }%s\n", (cse == ncases - 1) ? "" : ",");
        }
        printf("  ],\n");
    }

    /* ---------- matrix inversion ---------- */
    {
        int nbcomps[] = {3, 4, 5, 6, 3, 4};
        int ncases = (int)(sizeof(nbcomps) / sizeof(nbcomps[0]));
        printf("  \"inversion\": [\n");
        for (int cse = 0; cse < ncases; cse++) {
            uint32_t nb = (uint32_t)nbcomps[cse];
            uint32_t ncoeff = nb * nb;
            float in[36];
            float work[36];
            float out[36];
            for (uint32_t i = 0; i < ncoeff; i++) {
                in[i] = rand_f32(1.0f);
            }
            /* case 4: near-singular (row nearly duplicated) */
            if (cse == 4) {
                for (uint32_t k = 0; k < nb; k++) {
                    in[1 * nb + k] = in[0 * nb + k] * 1.0000001f;
                }
            }
            /* case 5: zero first column -> pivot search fails, inversion returns false */
            if (cse == 5) {
                for (uint32_t r = 0; r < nb; r++) {
                    in[r * nb + 0] = 0.0f;
                }
            }
            memcpy(work, in, ncoeff * sizeof(float));
            OPJ_BOOL ok = opj_matrix_inversion_f(work, out, nb);

            printf("    {\"nbcomp\": %u, \"ok\": %s,\n", nb, ok ? "true" : "false");
            printf("      \"in\": "); print_fbits_array(in, (int)ncoeff); printf(",\n");
            printf("      \"out\": "); print_fbits_array(out, (int)ncoeff); printf("\n");
            printf("    }%s\n", (cse == ncases - 1) ? "" : ",");
        }
        printf("  ]\n");
    }

    printf("}\n");
    return 0;
}
