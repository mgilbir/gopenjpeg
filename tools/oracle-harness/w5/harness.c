/* W5 tier-1 oracle harness.
 *
 * Includes t1.c directly so it can drive the static encode/decode entry points
 * opj_t1_encode_cblk / opj_t1_decode_cblk and opj_t1_allocate_buffers, dumping
 * bit-exact input/output vectors for the pure-Go port.
 *
 * Build (see README): the harness provides t1.c's own (non-static) symbols via
 * the #include, so the archive's t1.o is not pulled in and there is no
 * duplicate-symbol clash; every other dependency (mqc, dwt, malloc, ...) is
 * resolved from libopenjp2.a.
 *
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w5/harness.c oracle/openjpeg/build/bin/libopenjp2.a \
 *       -lm -lpthread -o /tmp/w5harness
 *
 * NOTE: t1.c poisons malloc/calloc/free/realloc after its includes; this file
 * therefore only uses opj_* allocators and stack/static buffers, plus fwrite.
 */

#include "t1.c"

#include <stdio.h>
#include <stdint.h>
#include <string.h>

/* ------------------------------------------------------------------ */
/* Deterministic PRNG (xorshift64*).                                    */
static uint64_t g_rng;
static void rng_seed(uint64_t s)
{
    g_rng = s ? s : 0x9E3779B97F4A7C15ULL;
}
static uint32_t rng_u32(void)
{
    uint64_t x = g_rng;
    x ^= x >> 12;
    x ^= x << 25;
    x ^= x >> 27;
    g_rng = x;
    return (uint32_t)((x * 0x2545F4914F6CDD1DULL) >> 32);
}

/* ------------------------------------------------------------------ */
/* Little-endian writers (assume LE host, checked in main).             */
static void wu32(FILE *f, uint32_t v) { fwrite(&v, 4, 1, f); }
static void wi32(FILE *f, int32_t v) { fwrite(&v, 4, 1, f); }
static void wu8(FILE *f, uint8_t v) { fwrite(&v, 1, 1, f); }
static void wf64(FILE *f, double v) { fwrite(&v, 8, 1, f); }

/* ------------------------------------------------------------------ */
/* Parameter matrix.                                                    */
static const uint32_t SIZES[][2] = {
    {4, 4}, {8, 8}, {16, 16}, {32, 32}, {64, 64},
    {5, 7}, {64, 3}, {3, 64}, {1, 1},
};
#define NSIZES (sizeof(SIZES) / sizeof(SIZES[0]))

static const uint32_t STYLES[] = {
    0,
    J2K_CCP_CBLKSTY_LAZY,
    J2K_CCP_CBLKSTY_RESET,
    J2K_CCP_CBLKSTY_TERMALL,
    J2K_CCP_CBLKSTY_VSC,
    J2K_CCP_CBLKSTY_SEGSYM,
    J2K_CCP_CBLKSTY_PTERM,
    J2K_CCP_CBLKSTY_LAZY | J2K_CCP_CBLKSTY_RESET | J2K_CCP_CBLKSTY_TERMALL,
    J2K_CCP_CBLKSTY_LAZY | J2K_CCP_CBLKSTY_RESET | J2K_CCP_CBLKSTY_TERMALL |
    J2K_CCP_CBLKSTY_VSC | J2K_CCP_CBLKSTY_SEGSYM | J2K_CCP_CBLKSTY_PTERM,
};
#define NSTYLES (sizeof(STYLES) / sizeof(STYLES[0]))

/* Fill t1->data (w*h) with DWT-like coefficients: a mixture of zeros/small and
 * larger magnitudes, signed, scaled so that numbps varies with `bits`. */
static void gen_data(OPJ_INT32 *data, uint32_t w, uint32_t h, uint32_t bits)
{
    uint32_t n = w * h, i;
    uint32_t mask = (bits >= 31) ? 0x7fffffffu : ((1u << bits) - 1u);
    for (i = 0; i < n; i++) {
        uint32_t r = rng_u32();
        int32_t v;
        if ((r & 3u) == 0u) {
            v = 0;                       /* ~25% zero */
        } else if ((r & 3u) == 1u) {
            v = (int32_t)(rng_u32() & 0x3fu); /* small */
        } else {
            v = (int32_t)(rng_u32() & mask);
        }
        if (r & 0x80000000u) {
            v = -v;
        }
        data[i] = v;
    }
    /* Ensure at least one sample reaches the target magnitude so numbps is
     * predictable and non-trivial. */
    if (bits < 31) {
        data[(rng_u32() % n)] = (int32_t)(1u << bits) | (int32_t)(rng_u32() & mask);
    }
}

/* ------------------------------------------------------------------ */
/* Scratch buffers big enough for the largest block (64x64).            */
static OPJ_INT32 g_input[64 * 64];
static OPJ_BYTE g_cblkdata[64 * 64 * 8 + 64];
static opj_tcd_pass_t g_passes[512];
static OPJ_BYTE g_stream[64 * 64 * 8 + 64];
static OPJ_INT32 g_decoded[64 * 64];

int main(void)
{
    /* Endianness sanity. */
    uint32_t one = 1;
    if (*(uint8_t *)&one != 1) {
        fprintf(stderr, "harness requires little-endian host\n");
        return 1;
    }

    FILE *fe = fopen("testdata/vectors/t1/t1_encode.bin", "wb");
    FILE *fd = fopen("testdata/vectors/t1/t1_decode.bin", "wb");
    if (!fe || !fd) {
        fprintf(stderr, "cannot open output files (run from repo root)\n");
        return 1;
    }
    fwrite("T1EN0001", 1, 8, fe);
    fwrite("T1DE0001", 1, 8, fd);

    /* Placeholder counts, patched at the end. */
    uint32_t enc_count = 0, dec_count = 0;
    long enc_count_pos = ftell(fe), dec_count_pos = ftell(fd);
    wu32(fe, 0);
    wu32(fd, 0);

    rng_seed(0xC0FFEE123456789ULL);

    opj_t1_t *t1e = opj_t1_create(OPJ_TRUE);
    opj_t1_t *t1d = opj_t1_create(OPJ_FALSE);

    uint32_t si, sty, orient, qi;
    uint32_t case_index = 0;

    for (si = 0; si < NSIZES; si++) {
        uint32_t w = SIZES[si][0];
        uint32_t h = SIZES[si][1];
        int big = (w * h) >= (32 * 32);

        for (sty = 0; sty < NSTYLES; sty++) {
            uint32_t cblksty = STYLES[sty];

            for (orient = 0; orient < 4; orient++) {
                /* For large blocks, prune the cross product to keep vectors
                 * small: only orient 0 and 3. */
                if (big && orient != 0 && orient != 3) {
                    continue;
                }
                for (qi = 0; qi < 2; qi++) {
                    uint32_t qmfbid = qi;
                    /* Vary the magnitude (=> numbps) across cases 1..12. */
                    uint32_t bits = 6 + (case_index % 12); /* 6..17 -> numbps 1..12 */
                    uint32_t compno = 0, level = case_index % 6, numcomps = 1;
                    double stepsize = (qmfbid == 1) ? 1.0
                                      : (0.0009 + 0.00007 * (double)(case_index % 17));
                    case_index++;

                    /* --- Encode --- */
                    if (!opj_t1_allocate_buffers(t1e, w, h)) {
                        fprintf(stderr, "alloc enc failed\n");
                        return 1;
                    }
                    gen_data(t1e->data, w, h, bits);
                    memcpy(g_input, t1e->data, sizeof(OPJ_INT32) * w * h);

                    opj_tcd_cblk_enc_t cblk;
                    memset(&cblk, 0, sizeof(cblk));
                    cblk.x0 = 0;
                    cblk.y0 = 0;
                    cblk.x1 = (OPJ_INT32)w;
                    cblk.y1 = (OPJ_INT32)h;
                    cblk.data = g_cblkdata;
                    cblk.passes = g_passes;
                    memset(g_passes, 0, sizeof(g_passes));

                    (void)opj_t1_encode_cblk(t1e, &cblk, orient, compno, level,
                                             qmfbid, stepsize, cblksty, numcomps,
                                             NULL, 0);

                    uint32_t numbps = cblk.numbps;
                    uint32_t totalpasses = cblk.totalpasses;

                    /* Write encode record. */
                    enc_count++;
                    wu32(fe, w);
                    wu32(fe, h);
                    wu32(fe, orient);
                    wu32(fe, compno);
                    wu32(fe, level);
                    wu32(fe, qmfbid);
                    wu32(fe, cblksty);
                    wu32(fe, numcomps);
                    wf64(fe, stepsize);
                    { uint32_t i; for (i = 0; i < w * h; i++) wi32(fe, g_input[i]); }
                    wu32(fe, numbps);
                    wu32(fe, totalpasses);
                    { uint32_t p;
                        for (p = 0; p < totalpasses; p++) {
                            wu32(fe, cblk.passes[p].rate);
                            wu32(fe, (uint32_t)cblk.passes[p].term);
                            wf64(fe, cblk.passes[p].distortiondec);
                        }
                    }
                    uint32_t fullLen = totalpasses ? cblk.passes[totalpasses - 1].rate : 0;
                    wu32(fe, fullLen);
                    fwrite(cblk.data, 1, fullLen, fe);

                    if (totalpasses == 0) {
                        continue; /* nothing to decode */
                    }

                    /* Prepare the full stream buffer (+2 scratch) for decode. */
                    memcpy(g_stream, cblk.data, fullLen);
                    g_stream[fullLen] = 0;
                    g_stream[fullLen + 1] = 0;

                    /* Choose truncation pass counts: 1, mid, full, plus every
                     * terminated-pass (segment) boundary. */
                    uint32_t truncs[512];
                    uint32_t ntr = 0;
                    truncs[ntr++] = 1;
                    if (totalpasses >= 2) truncs[ntr++] = (totalpasses + 1) / 2;
                    truncs[ntr++] = totalpasses;
                    /* Add up to 2 terminated-pass (segment) boundaries so the
                     * multi-segment / bypass paths are exercised without an
                     * explosion for TERMALL. */
                    { uint32_t p, added = 0;
                        for (p = 0; p + 1 < totalpasses && added < 2; p++) {
                            if (cblk.passes[p].term) {
                                truncs[ntr++] = p + 1;
                                added++;
                            }
                        }
                    }

                    uint32_t roivals[2] = {0, 3};
                    uint32_t ri;
                    for (ri = 0; ri < 2; ri++) {
                        uint32_t roishift = roivals[ri];
                        /* roishift only meaningful for a subset to bound size. */
                        if (roishift != 0 && (case_index % 5) != 0) {
                            continue;
                        }
                        uint32_t ti;
                        for (ti = 0; ti < ntr; ti++) {
                            uint32_t P = truncs[ti];
                            uint32_t prev = 0; /* dedup */
                            uint32_t skip = 0, tj;
                            for (tj = 0; tj < ti; tj++) if (truncs[tj] == P) skip = 1;
                            (void)prev;
                            if (skip) continue;
                            if (P == 0 || P > totalpasses) continue;

                            /* Build segments for the first P passes. */
                            opj_tcd_seg_t segs[512];
                            uint32_t nsegs = 0, cum = 0, passStart = 0, i;
                            for (i = 0; i < P; i++) {
                                if (cblk.passes[i].term || i == P - 1) {
                                    uint32_t endRate = cblk.passes[i].rate;
                                    if (endRate > fullLen) endRate = fullLen;
                                    memset(&segs[nsegs], 0, sizeof(segs[nsegs]));
                                    segs[nsegs].len = endRate - cum;
                                    segs[nsegs].real_num_passes = i - passStart + 1;
                                    cum = endRate;
                                    passStart = i + 1;
                                    nsegs++;
                                }
                            }

                            opj_tcd_seg_data_chunk_t chunk;
                            chunk.data = g_stream;
                            chunk.len = cum;

                            opj_tcd_cblk_dec_t dc;
                            memset(&dc, 0, sizeof(dc));
                            dc.x0 = 0;
                            dc.y0 = 0;
                            dc.x1 = (OPJ_INT32)w;
                            dc.y1 = (OPJ_INT32)h;
                            dc.numbps = numbps;
                            dc.segs = segs;
                            dc.real_num_segs = nsegs;
                            dc.numsegs = nsegs;
                            dc.chunks = &chunk;
                            dc.numchunks = 1;
                            dc.decoded_data = NULL;
                            dc.corrupted = OPJ_FALSE;

                            OPJ_BOOL ok = opj_t1_decode_cblk(
                                              t1d, &dc, orient, roishift, cblksty,
                                              NULL, NULL, OPJ_FALSE);
                            if (!ok) {
                                /* bpno_plus_one >= 31: skip (mirrors Go error). */
                                continue;
                            }
                            memcpy(g_decoded, t1d->data, sizeof(OPJ_INT32) * w * h);

                            /* Write decode record. */
                            dec_count++;
                            wu32(fd, w);
                            wu32(fd, h);
                            wu32(fd, orient);
                            wu32(fd, roishift);
                            wu32(fd, cblksty);
                            wu32(fd, numbps);
                            wu32(fd, nsegs);
                            { uint32_t s;
                                for (s = 0; s < nsegs; s++) {
                                    wu32(fd, segs[s].len);
                                    wu32(fd, segs[s].real_num_passes);
                                }
                            }
                            wu32(fd, cum); /* chunk length */
                            fwrite(g_stream, 1, cum, fd);
                            { uint32_t j;
                                for (j = 0; j < w * h; j++) wi32(fd, g_decoded[j]);
                            }
                        }
                    }
                }
            }
        }
    }

    /* Patch counts. */
    fseek(fe, enc_count_pos, SEEK_SET);
    wu32(fe, enc_count);
    fseek(fd, dec_count_pos, SEEK_SET);
    wu32(fd, dec_count);

    fclose(fe);
    fclose(fd);
    opj_t1_destroy(t1e);
    opj_t1_destroy(t1d);

    fprintf(stderr, "wrote %u encode, %u decode vectors\n", enc_count, dec_count);
    return 0;
}
