/* W3 oracle harness: region/partial DWT vectors (5/3 + 9/7).
 *
 * Builds an opj_tcd_tilecomp_t by hand with resolutions, bands, one precinct
 * and one whole-band code-block per band (decoded_data filled deterministically)
 * and drives opj_dwt_decode / opj_dwt_decode_real with whole_tile_decoding=FALSE.
 *
 * Build (from repo root):
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w3/partial_gen.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/partial_gen
 *   /tmp/partial_gen testdata/vectors/dwt/partial.bin
 */
#define OPJ_SKIP_POISON
#include "opj_includes.h"
#include "dwt.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static FILE* g_out;
static uint32_t g_seed = 0xBEEF01u;
static uint32_t lcg(void) { g_seed = g_seed * 1103515245u + 12345u; return g_seed; }
static void wu32(uint32_t v) { fwrite(&v, 4, 1, g_out); }
static void wi32(int32_t v) { fwrite(&v, 4, 1, g_out); }

static OPJ_INT32 cdp2(OPJ_INT32 a, OPJ_INT32 b)
{
    return (OPJ_INT32)((a + (((OPJ_INT64)1) << b) - 1) >> b);
}
static OPJ_INT32 cdp2_64(OPJ_INT64 a, OPJ_INT32 b)
{
    return (OPJ_INT32)((a + (((OPJ_INT64)1) << b) - 1) >> b);
}

#define T53 0
#define T97 1

static opj_thread_pool_t* g_tp;

static void emit_partial(int type, uint32_t numres,
                         int32_t x0, int32_t y0, int32_t x1, int32_t y1,
                         uint32_t wx0, uint32_t wy0, uint32_t wx1, uint32_t wy1)
{
    int is97 = (type == T97);
    opj_tcd_resolution_t* res = calloc(numres, sizeof(opj_tcd_resolution_t));

    for (uint32_t r = 0; r < numres; r++) {
        uint32_t level = numres - 1 - r;
        opj_tcd_resolution_t* R = &res[r];
        R->x0 = cdp2(x0, (OPJ_INT32)level);
        R->y0 = cdp2(y0, (OPJ_INT32)level);
        R->x1 = cdp2(x1, (OPJ_INT32)level);
        R->y1 = cdp2(y1, (OPJ_INT32)level);
        R->pw = 1;
        R->ph = 1;
        R->numbands = (r == 0) ? 1 : 3;
        for (uint32_t b = 0; b < R->numbands; b++) {
            opj_tcd_band_t* band = &R->bands[b];
            if (r == 0) {
                band->bandno = 0;
                band->x0 = cdp2(x0, (OPJ_INT32)level);
                band->y0 = cdp2(y0, (OPJ_INT32)level);
                band->x1 = cdp2(x1, (OPJ_INT32)level);
                band->y1 = cdp2(y1, (OPJ_INT32)level);
            } else {
                band->bandno = b + 1;
                OPJ_INT32 x0b = (OPJ_INT32)(band->bandno & 1);
                OPJ_INT32 y0b = (OPJ_INT32)(band->bandno >> 1);
                band->x0 = cdp2_64((OPJ_INT64)x0 - ((OPJ_INT64)x0b << level), (OPJ_INT32)(level + 1));
                band->y0 = cdp2_64((OPJ_INT64)y0 - ((OPJ_INT64)y0b << level), (OPJ_INT32)(level + 1));
                band->x1 = cdp2_64((OPJ_INT64)x1 - ((OPJ_INT64)x0b << level), (OPJ_INT32)(level + 1));
                band->y1 = cdp2_64((OPJ_INT64)y1 - ((OPJ_INT64)y0b << level), (OPJ_INT32)(level + 1));
            }
            uint32_t bw = (band->x1 > band->x0) ? (uint32_t)(band->x1 - band->x0) : 0;
            uint32_t bh = (band->y1 > band->y0) ? (uint32_t)(band->y1 - band->y0) : 0;
            opj_tcd_precinct_t* prc = calloc(1, sizeof(opj_tcd_precinct_t));
            band->precincts = prc;
            if (bw > 0 && bh > 0) {
                prc->cw = 1;
                prc->ch = 1;
                opj_tcd_cblk_dec_t* cblk = calloc(1, sizeof(opj_tcd_cblk_dec_t));
                prc->cblks.dec = cblk;
                cblk->x0 = band->x0;
                cblk->y0 = band->y0;
                cblk->x1 = band->x1;
                cblk->y1 = band->y1;
                cblk->decoded_data = calloc((size_t)bw * bh, sizeof(OPJ_INT32));
                for (size_t i = 0; i < (size_t)bw * bh; i++) {
                    if (is97) {
                        float f = (float)((int32_t)(lcg() & 0xFFFF) - 32768) / 211.0f;
                        memcpy(&cblk->decoded_data[i], &f, 4);
                    } else {
                        cblk->decoded_data[i] = (int32_t)(lcg() & 0xFFFF) - 32768;
                    }
                }
            } else {
                prc->cw = 0;
                prc->ch = 0;
            }
        }
    }

    opj_tcd_resolution_t* trmax = &res[numres - 1];
    trmax->win_x0 = wx0;
    trmax->win_y0 = wy0;
    trmax->win_x1 = wx1;
    trmax->win_y1 = wy1;

    uint32_t winW = wx1 - wx0, winH = wy1 - wy0;

    opj_tcd_tilecomp_t tilec;
    memset(&tilec, 0, sizeof(tilec));
    tilec.x0 = x0; tilec.y0 = y0; tilec.x1 = x1; tilec.y1 = y1;
    tilec.numresolutions = numres;
    tilec.minimum_num_resolutions = numres;
    tilec.resolutions = res;
    tilec.win_x0 = wx0; tilec.win_y0 = wy0; tilec.win_x1 = wx1; tilec.win_y1 = wy1;
    tilec.data_win = calloc((size_t)winW * winH, sizeof(OPJ_INT32));

    /* dump header + geometry + band data */
    wu32((uint32_t)type);
    wu32(numres);
    wi32(x0); wi32(y0); wi32(x1); wi32(y1);
    wu32(wx0); wu32(wy0); wu32(wx1); wu32(wy1);
    for (uint32_t r = 0; r < numres; r++) {
        opj_tcd_resolution_t* R = &res[r];
        wi32(R->x0); wi32(R->y0); wi32(R->x1); wi32(R->y1);
        wu32(R->numbands);
        for (uint32_t b = 0; b < R->numbands; b++) {
            opj_tcd_band_t* band = &R->bands[b];
            wu32(band->bandno);
            wi32(band->x0); wi32(band->y0); wi32(band->x1); wi32(band->y1);
            uint32_t bw = (band->x1 > band->x0) ? (uint32_t)(band->x1 - band->x0) : 0;
            uint32_t bh = (band->y1 > band->y0) ? (uint32_t)(band->y1 - band->y0) : 0;
            if (band->precincts->cw > 0) {
                uint32_t dl = bw * bh;
                wu32(dl);
                fwrite(band->precincts->cblks.dec->decoded_data,
                       sizeof(OPJ_INT32), dl, g_out);
            } else {
                wu32(0);
            }
        }
    }

    opj_tcd_t tcd;
    memset(&tcd, 0, sizeof(tcd));
    tcd.thread_pool = g_tp;
    tcd.whole_tile_decoding = OPJ_FALSE;

    if (is97) {
        opj_dwt_decode_real(&tcd, &tilec, numres);
    } else {
        opj_dwt_decode(&tcd, &tilec, numres);
    }

    fwrite(tilec.data_win, sizeof(OPJ_INT32), (size_t)winW * winH, g_out);

    /* cleanup */
    for (uint32_t r = 0; r < numres; r++) {
        opj_tcd_resolution_t* R = &res[r];
        for (uint32_t b = 0; b < R->numbands; b++) {
            opj_tcd_band_t* band = &R->bands[b];
            if (band->precincts->cw > 0) {
                free(band->precincts->cblks.dec->decoded_data);
                free(band->precincts->cblks.dec);
            }
            free(band->precincts);
        }
    }
    free(tilec.data_win);
    free(res);
}

/* skip if a processed resolution collapses (matches Go test guard) */
static int collapses(uint32_t numres, int32_t x0, int32_t y0, int32_t x1, int32_t y1)
{
    for (uint32_t r = 1; r < numres; r++) {
        uint32_t level = numres - 1 - r;
        if (cdp2(x1, (OPJ_INT32)level) == cdp2(x0, (OPJ_INT32)level)) return 1;
        if (cdp2(y1, (OPJ_INT32)level) == cdp2(y0, (OPJ_INT32)level)) return 1;
    }
    return 0;
}

int main(int argc, char** argv)
{
    if (argc < 2) { fprintf(stderr, "usage: %s out.bin\n", argv[0]); return 1; }
    g_out = fopen(argv[1], "wb");
    if (!g_out) { perror("fopen"); return 1; }
    g_tp = opj_thread_pool_create(0);

    struct { uint32_t w, h; } sizes[] = {
        {8, 8}, {13, 11}, {16, 16}, {23, 19},
    };
    struct { int32_t ox, oy; } origins[] = { {0, 0}, {1, 1}, {2, 3} };
    uint32_t nsizes = sizeof(sizes) / sizeof(sizes[0]);
    uint32_t norig = sizeof(origins) / sizeof(origins[0]);

    long count_pos = ftell(g_out);
    wu32(0);
    uint32_t ncases = 0;

    for (uint32_t si = 0; si < nsizes; si++) {
        uint32_t w = sizes[si].w, h = sizes[si].h;
        for (uint32_t oi = 0; oi < norig; oi++) {
            int32_t ox = origins[oi].ox, oy = origins[oi].oy;
            int32_t x0 = ox, y0 = oy, x1 = ox + (int32_t)w, y1 = oy + (int32_t)h;
            for (uint32_t nr = 2; nr <= 4; nr++) {
                if (collapses(nr, x0, y0, x1, y1)) continue;
                /* Several windows in tile-comp coordinates. */
                uint32_t cx = (uint32_t)x0 + w / 2, cy = (uint32_t)y0 + h / 2;
                struct { uint32_t a, b, c, d; } wins[] = {
                    { (uint32_t)x0, (uint32_t)y0, (uint32_t)x1, (uint32_t)y1 },        /* full */
                    { cx, cy, cx + 1, cy + 1 },                                       /* 1 pixel */
                    { (uint32_t)x0, (uint32_t)y0, cx + 1, cy + 1 },                    /* top-left */
                    { cx, cy, (uint32_t)x1, (uint32_t)y1 },                            /* bottom-right */
                    { (uint32_t)x1 - 1, (uint32_t)y0, (uint32_t)x1, (uint32_t)y1 },    /* right edge col */
                    { (uint32_t)x0, (uint32_t)y1 - 1, (uint32_t)x1, (uint32_t)y1 },    /* bottom edge row */
                };
                uint32_t nwin = sizeof(wins) / sizeof(wins[0]);
                for (uint32_t wi = 0; wi < nwin; wi++) {
                    uint32_t a = wins[wi].a, b = wins[wi].b, c = wins[wi].c, d = wins[wi].d;
                    if (!(c > a && d > b)) continue;
                    /* Only a subset of windows for larger tiles to save space */
                    if ((w * h) > 300 && wi != 0 && wi != 1 && wi != 3) continue;
                    emit_partial(T53, nr, x0, y0, x1, y1, a, b, c, d); ncases++;
                    emit_partial(T97, nr, x0, y0, x1, y1, a, b, c, d); ncases++;
                }
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
