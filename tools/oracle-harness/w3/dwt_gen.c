/* W3 oracle harness: whole-tile DWT vectors (5/3 + 9/7, forward + inverse).
 *
 * Build (from repo root):
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w3/dwt_gen.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/dwt_gen
 *   /tmp/dwt_gen testdata/vectors/dwt/whole.bin
 *
 * Vector format documented in testdata/vectors/dwt/README.md.
 */
#define OPJ_SKIP_POISON
#include "opj_includes.h"
#include "dwt.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static FILE* g_out;
static uint32_t g_seed = 0xC0FFEEu;
static uint32_t lcg(void)
{
    g_seed = g_seed * 1103515245u + 12345u;
    return g_seed;
}
static void wu32(uint32_t v) { fwrite(&v, 4, 1, g_out); }
static void wi32(int32_t v) { fwrite(&v, 4, 1, g_out); }

static OPJ_UINT32 ceildivpow2(OPJ_UINT32 a, OPJ_UINT32 b)
{
    return (OPJ_UINT32)((a + (((OPJ_UINT64)1U) << b) - 1U) >> b);
}

#define T_ENC53 0
#define T_DEC53 1
#define T_ENC97 2
#define T_DEC97 3

/* Build resolutions for a tilecomp with the standard reduction. */
static void build_res(opj_tcd_resolution_t* res, uint32_t numres,
                      int32_t x0, int32_t y0, int32_t x1, int32_t y1)
{
    for (uint32_t r = 0; r < numres; r++) {
        uint32_t level = numres - 1 - r;
        res[r].x0 = (OPJ_INT32)ceildivpow2((OPJ_UINT32)x0, level);
        res[r].y0 = (OPJ_INT32)ceildivpow2((OPJ_UINT32)y0, level);
        res[r].x1 = (OPJ_INT32)ceildivpow2((OPJ_UINT32)x1, level);
        res[r].y1 = (OPJ_INT32)ceildivpow2((OPJ_UINT32)y1, level);
        res[r].numbands = (r == 0) ? 1 : 3;
    }
}

static opj_thread_pool_t* g_tp;

static void emit_case(int type, uint32_t numres,
                      int32_t x0, int32_t y0, int32_t x1, int32_t y1)
{
    uint32_t w = (uint32_t)(x1 - x0);
    uint32_t h = (uint32_t)(y1 - y0);
    size_t n = (size_t)w * h;

    opj_tcd_resolution_t* res = calloc(numres, sizeof(opj_tcd_resolution_t));
    build_res(res, numres, x0, y0, x1, y1);

    opj_tcd_tilecomp_t tilec;
    memset(&tilec, 0, sizeof(tilec));
    tilec.x0 = x0;
    tilec.y0 = y0;
    tilec.x1 = x1;
    tilec.y1 = y1;
    tilec.numresolutions = numres;
    tilec.minimum_num_resolutions = numres;
    tilec.resolutions = res;
    tilec.data = calloc(n ? n : 1, sizeof(OPJ_INT32));

    /* fill input data deterministically */
    int is97 = (type == T_ENC97 || type == T_DEC97);
    for (size_t i = 0; i < n; i++) {
        if (is97) {
            float f = (float)((int32_t)(lcg() & 0xFFFF) - 32768) / 137.0f;
            memcpy(&tilec.data[i], &f, 4);
        } else {
            tilec.data[i] = (int32_t)(lcg() & 0xFFFF) - 32768;
        }
    }

    /* dump header + input */
    wu32((uint32_t)type);
    wu32(numres);
    wu32(w);
    wu32(h);
    wi32(x0);
    wi32(y0);
    for (uint32_t r = 0; r < numres; r++) {
        wi32(res[r].x0);
        wi32(res[r].y0);
        wi32(res[r].x1);
        wi32(res[r].y1);
    }
    fwrite(tilec.data, sizeof(OPJ_INT32), n, g_out);

    opj_tcd_t tcd;
    memset(&tcd, 0, sizeof(tcd));
    tcd.thread_pool = g_tp;
    tcd.whole_tile_decoding = OPJ_TRUE;

    switch (type) {
    case T_ENC53:
        opj_dwt_encode(&tcd, &tilec);
        break;
    case T_DEC53:
        opj_dwt_decode(&tcd, &tilec, numres);
        break;
    case T_ENC97:
        opj_dwt_encode_real(&tcd, &tilec);
        break;
    case T_DEC97:
        opj_dwt_decode_real(&tcd, &tilec, numres);
        break;
    }

    /* dump output */
    fwrite(tilec.data, sizeof(OPJ_INT32), n, g_out);

    free(tilec.data);
    free(res);
}

/* max numres such that no dimension underflows badly; cap for tiny tiles. */
static uint32_t clamp_numres(uint32_t numres, uint32_t w, uint32_t h)
{
    (void)w; (void)h;
    if (numres < 1) numres = 1;
    if (numres > 6) numres = 6;
    return numres;
}

int main(int argc, char** argv)
{
    if (argc < 2) {
        fprintf(stderr, "usage: %s out.bin\n", argv[0]);
        return 1;
    }
    g_out = fopen(argv[1], "wb");
    if (!g_out) { perror("fopen"); return 1; }
    g_tp = opj_thread_pool_create(0);

    struct { uint32_t w, h; } sizes[] = {
        {1, 1}, {1, 7}, {7, 1}, {5, 3}, {8, 8}, {9, 9},
        {13, 7}, {16, 15}, {23, 21},
    };
    struct { int32_t ox, oy; } origins[] = {
        {0, 0}, {1, 0}, {0, 1}, {1, 1},
    };
    uint32_t nsizes = sizeof(sizes) / sizeof(sizes[0]);
    uint32_t norig = sizeof(origins) / sizeof(origins[0]);

    /* Count cases first for a header. */
    long count_pos = ftell(g_out);
    wu32(0); /* placeholder */
    uint32_t ncases = 0;

    for (uint32_t si = 0; si < nsizes; si++) {
        uint32_t w = sizes[si].w, h = sizes[si].h;
        int big = (w * h) >= 300;
        for (uint32_t oi = 0; oi < norig; oi++) {
            /* Large tiles: only origin (0,0) and (1,1) to limit vector size */
            if (big && oi != 0 && oi != 3) continue;
            int32_t ox = origins[oi].ox, oy = origins[oi].oy;
            int32_t x0 = ox, y0 = oy;
            int32_t x1 = ox + (int32_t)w, y1 = oy + (int32_t)h;
            for (uint32_t nr = 1; nr <= 6; nr++) {
                uint32_t numres = clamp_numres(nr, w, h);
                /* Large tiles: limit decomposition depth spread */
                if (big && nr != 1 && nr != 3 && nr != 5) continue;
                /* A 1x1 tile with >1 resolution level is degenerate: the C
                 * code reads out of bounds (UB) on the single-element buffer.
                 * Skip it. */
                if (w == 1 && h == 1 && numres > 1) continue;
                emit_case(T_ENC53, numres, x0, y0, x1, y1); ncases++;
                emit_case(T_DEC53, numres, x0, y0, x1, y1); ncases++;
                emit_case(T_ENC97, numres, x0, y0, x1, y1); ncases++;
                emit_case(T_DEC97, numres, x0, y0, x1, y1); ncases++;
            }
        }
    }

    long here = ftell(g_out);
    fseek(g_out, count_pos, SEEK_SET);
    wu32(ncases);
    fseek(g_out, here, SEEK_SET);

    fclose(g_out);
    fprintf(stderr, "wrote %s: %u cases\n", argv[1], ncases);
    return 0;
}
