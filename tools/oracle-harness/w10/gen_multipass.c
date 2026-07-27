/*
 * W10 harness: synthesize multi-pass HT codeblocks and run them through the
 * instrumented opj_t1_ht_decode_cblk (which dumps input/output vectors to the
 * file named by OPJ_W10_DUMP on success).
 *
 * Why synthesis: the only HT streams in the conformance corpus are
 * cleanup-pass-only with numbps==1 (zero_bplanes==Mb), and neither OpenJPEG
 * nor OpenJPH can encode SigProp/MagRef passes. The HT cleanup segment of a
 * real codeblock remains a valid cleanup segment for any (Mb', numbps') with
 * the same zero_bplanes = Mb'+1-numbps', because the cleanup decoder only uses
 * p = numbps (a shift) and zero_bplanes+1 (a bound). Raising numbps' >= 2
 * legitimately enables SPP/MRP, whose input segment is an arbitrary bit
 * string read on demand (any byte pattern is a decodable refinement segment;
 * exhaustion is well-defined: SPP fills 0s, MRP fills 0s). The C decoder is
 * the oracle: whatever it outputs for these inputs is the ground truth the Go
 * port must reproduce bit-exactly.
 *
 * Input: the raw single-pass vector dump (574 records) produced by running
 * the instrumented opj_decompress over the htj2k corpus.
 * Output: appended records in the same format via the OPJ_W10_DUMP hook.
 *
 * Build/run: see testdata/vectors/ht/README.md.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "opj_includes.h"

OPJ_BOOL opj_t1_ht_decode_cblk(opj_t1_t *t1,
                               opj_tcd_cblk_dec_t* cblk,
                               OPJ_UINT32 orient,
                               OPJ_UINT32 roishift,
                               OPJ_UINT32 cblksty,
                               opj_event_mgr_t *p_manager,
                               opj_mutex_t* p_manager_mutex,
                               OPJ_BOOL check_pterm);

static OPJ_UINT32 xs_state;
static OPJ_UINT32 xs_next(void)
{
    OPJ_UINT32 x = xs_state;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    xs_state = x;
    return x;
}

static OPJ_UINT32 rd_u32(FILE* f)
{
    unsigned char b[4];
    if (fread(b, 1, 4, f) != 4) {
        return 0xFFFFFFFFu;
    }
    return (OPJ_UINT32)b[0] | ((OPJ_UINT32)b[1] << 8) |
           ((OPJ_UINT32)b[2] << 16) | ((OPJ_UINT32)b[3] << 24);
}

int main(int argc, char** argv)
{
    FILE* f;
    opj_t1_t* t1;
    opj_event_mgr_t mgr;
    int rec = 0, produced = 0;

    if (argc < 2) {
        fprintf(stderr, "usage: %s base_vectors.bin (set OPJ_W10_DUMP)\n",
                argv[0]);
        return 1;
    }
    f = fopen(argv[1], "rb");
    if (!f) {
        perror("open");
        return 1;
    }

    memset(&mgr, 0, sizeof(mgr)); /* null handlers: silent */
    t1 = opj_t1_create(OPJ_FALSE);
    if (!t1) {
        return 1;
    }

    for (;;) {
        OPJ_UINT32 w, h, orient, roishift, cblksty, Mb, numbps, numsegs;
        OPJ_UINT32 s0p, s0l, s1p, s1l, total, oc;
        OPJ_UINT8* coded;
        OPJ_UINT32 zbp;
        int v;

        w = rd_u32(f);
        if (w == 0xFFFFFFFFu) {
            break;    /* EOF */
        }
        h = rd_u32(f);
        orient = rd_u32(f);
        roishift = rd_u32(f);
        cblksty = rd_u32(f);
        Mb = rd_u32(f);
        numbps = rd_u32(f);
        numsegs = rd_u32(f);
        s0p = rd_u32(f);
        s0l = rd_u32(f);
        s1p = rd_u32(f);
        s1l = rd_u32(f);
        total = rd_u32(f);
        coded = (OPJ_UINT8*)opj_malloc(total ? total : 1);
        if (fread(coded, 1, total, f) != total) {
            break;
        }
        oc = rd_u32(f);
        fseek(f, (long)oc * 4, SEEK_CUR); /* skip expected output */

        (void)numsegs;
        (void)s1p;
        (void)s1l;
        zbp = Mb + 1 - numbps; /* preserved across variants */

        /* three variants per record:
           v=0: CUP+SPP        numbps'=2, small random seg2
           v=1: CUP+SPP+MRP    numbps'=3, medium random seg2, VSC flipped
           v=2: CUP+SPP+MRP    numbps'=8, larger seg2 (exhaustion mix)     */
        for (v = 0; v < 3; ++v) {
            opj_tcd_cblk_dec_t cblk;
            opj_tcd_seg_t segs[2];
            opj_tcd_seg_data_chunk_t chunk;
            OPJ_UINT32 nb2 = (v == 0) ? 2u : (v == 1) ? 3u : 8u;
            OPJ_UINT32 mb2 = zbp + nb2 - 1;
            OPJ_UINT32 sty = cblksty;
            OPJ_UINT32 l2;
            OPJ_UINT8* buf;
            OPJ_UINT32 i;

            if (mb2 > 30) {
                continue;
            }
            if (v == 1) {
                sty ^= J2K_CCP_CBLKSTY_VSC;
            }

            xs_state = 0x9E3779B9u ^ (OPJ_UINT32)(rec * 2654435761u + v * 97u);
            l2 = (v == 0) ? (xs_next() % 24u)
                 : (v == 1) ? (8u + xs_next() % 120u)
                 : (2u + xs_next() % 40u);
            /* every 29th variant: zero-length seg2 (warning path) */
            if ((rec * 3 + v) % 29 == 0) {
                l2 = 0;
            }

            buf = (OPJ_UINT8*)opj_malloc(s0l + l2 + 8);
            memcpy(buf, coded, s0l);
            for (i = 0; i < l2; ++i) {
                buf[s0l + i] = (OPJ_UINT8)(xs_next() & 0xFF);
            }
            memset(buf + s0l + l2, 0, 8);

            memset(&cblk, 0, sizeof(cblk));
            memset(segs, 0, sizeof(segs));
            cblk.x0 = 0;
            cblk.y0 = 0;
            cblk.x1 = (OPJ_INT32)w;
            cblk.y1 = (OPJ_INT32)h;
            cblk.Mb = mb2;
            cblk.numbps = nb2;
            cblk.numsegs = 2;
            cblk.real_num_segs = 2;
            cblk.numchunks = 1;
            chunk.data = buf;
            chunk.len = s0l + l2;
            cblk.chunks = &chunk;
            segs[0].len = s0l;
            segs[0].real_num_passes = s0p; /* 1 */
            segs[1].len = l2;
            segs[1].real_num_passes = (v == 0) ? 1u : 2u;
            cblk.segs = segs;

            if (opj_t1_ht_decode_cblk(t1, &cblk, orient, roishift, sty,
                                      &mgr, NULL, OPJ_FALSE)) {
                produced++;
            }
            opj_free(buf);
        }

        opj_free(coded);
        rec++;
    }

    fprintf(stderr, "records read: %d, decodes succeeded: %d\n", rec, produced);
    opj_t1_destroy(t1);
    fclose(f);
    return 0;
}
