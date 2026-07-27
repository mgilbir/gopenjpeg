/* W6 tier-2 (t2) oracle harness.
 *
 * Links against libopenjp2.a and drives the non-static t2.c entry points
 * opj_t2_encode_packets / opj_t2_decode_packets over hand-constructed synthetic
 * tiles. For each case it emits a fully self-describing text record: the tile
 * geometry (bands, precincts, code-blocks, passes, per-layer data), then the
 * encoded packet bytes, then the per-code-block decode results (segments,
 * received passes, concatenated chunk data). Truncated variants pin down the
 * warning-vs-error tolerance behaviour.
 *
 * The geometry is deliberately uniform (every band/precinct = 0,0,8,8, one
 * precinct per resolution) so precno is always 0 and the decode window (set to
 * the whole reference grid) makes opj_tcd_is_subband_area_of_interest always
 * true, matching the Go test's whole-tile AOI. This isolates the t2 packet
 * coding logic, which is what we are validating.
 *
 * Build (from repo root):
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w6/t2_harness.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/w6t2
 *   /tmp/w6t2 testdata/vectors/t2/t2_vectors.txt
 */

#include "opj_includes.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define BAND_XY 8      /* uniform band/precinct extent */
#define GRID_BIG 0x40000000u

/* xorshift64* PRNG (mirrored so realized values are emitted, not the seed). */
static uint64_t g_rng;
static void rng_seed(uint64_t s) { g_rng = s ? s : 0x9E3779B97F4A7C15ULL; }
static uint32_t rng_u32(void)
{
    uint64_t x = g_rng;
    x ^= x >> 12; x ^= x << 25; x ^= x >> 27;
    g_rng = x;
    return (uint32_t)((x * 0x2545F4914F6CDD1DULL) >> 32);
}

typedef struct {
    const char *name;
    OPJ_UINT32 numcomps;
    OPJ_UINT32 numres;
    OPJ_UINT32 cw, ch;
    OPJ_UINT32 numlayers;
    OPJ_UINT32 passes_per_layer;
    OPJ_UINT32 csty;
    int prg;
    OPJ_UINT32 maxlayers;   /* layers actually encoded */
    OPJ_UINT32 num_layers_to_decode;
    OPJ_UINT32 cblksty;     /* for decode init_seg */
    OPJ_UINT32 term_each;   /* if 1, term every pass; else term only last of layer */
    OPJ_UINT32 img_w, img_h;
    uint64_t seed;
    /* truncation: if trunc>0, decode only the first `trunc` encoded bytes */
    OPJ_UINT32 trunc;
    OPJ_BOOL strict;
} t2cfg_t;

static OPJ_UINT32 bandno_of(OPJ_UINT32 r, OPJ_UINT32 b)
{
    return (r == 0) ? 0 : (b + 1);
}
static OPJ_UINT32 numbands_of(OPJ_UINT32 r)
{
    return (r == 0) ? 1 : 3;
}

/* ---- image + cp -------------------------------------------------------- */

static opj_image_t *build_image(const t2cfg_t *c)
{
    opj_image_cmptparm_t parms[4];
    memset(parms, 0, sizeof(parms));
    OPJ_UINT32 i;
    for (i = 0; i < c->numcomps; i++) {
        parms[i].dx = 1; parms[i].dy = 1;
        parms[i].w = c->img_w; parms[i].h = c->img_h;
        parms[i].x0 = 0; parms[i].y0 = 0;
        parms[i].prec = 8; parms[i].sgnd = 0;
    }
    opj_image_t *img = opj_image_create(c->numcomps, parms, OPJ_CLRSPC_GRAY);
    img->x0 = 0; img->y0 = 0; img->x1 = c->img_w; img->y1 = c->img_h;
    return img;
}

static void build_cp(const t2cfg_t *c, opj_cp_t *cp)
{
    memset(cp, 0, sizeof(*cp));
    cp->rsiz = OPJ_PROFILE_NONE;
    cp->tx0 = 0; cp->ty0 = 0; cp->tdx = c->img_w; cp->tdy = c->img_h;
    cp->tw = 1; cp->th = 1;
    cp->strict = c->strict;
    cp->m_specific_param.m_enc.m_tp_on = 0;
    cp->tcps = (opj_tcp_t *)opj_calloc(1, sizeof(opj_tcp_t));
    opj_tcp_t *tcp = cp->tcps;
    tcp->csty = c->csty;
    tcp->prg = (OPJ_PROG_ORDER)c->prg;
    tcp->numlayers = c->numlayers;
    tcp->num_layers_to_decode = c->num_layers_to_decode;
    tcp->numpocs = 0;
    tcp->POC = 0;
    opj_tccp_t *tccps = (opj_tccp_t *)opj_calloc(c->numcomps, sizeof(opj_tccp_t));
    tcp->tccps = tccps;
    OPJ_UINT32 comp, r;
    for (comp = 0; comp < c->numcomps; comp++) {
        tccps[comp].numresolutions = c->numres;
        tccps[comp].cblksty = c->cblksty;
        tccps[comp].qmfbid = 1;
        for (r = 0; r < OPJ_J2K_MAXRLVLS; r++) {
            tccps[comp].prcw[r] = 15;
            tccps[comp].prch[r] = 15;
        }
    }
}

static void free_cp(opj_cp_t *cp)
{
    opj_free(cp->tcps->tccps);
    opj_free(cp->tcps);
}

/* ---- encode tile ------------------------------------------------------- */

static opj_tcd_tile_t *build_enc_tile(const t2cfg_t *c, opj_event_mgr_t *mgr)
{
    opj_tcd_tile_t *tile = (opj_tcd_tile_t *)opj_calloc(1, sizeof(opj_tcd_tile_t));
    tile->numcomps = c->numcomps;
    tile->comps = (opj_tcd_tilecomp_t *)opj_calloc(c->numcomps, sizeof(opj_tcd_tilecomp_t));
    tile->x0 = 0; tile->y0 = 0; tile->x1 = c->img_w; tile->y1 = c->img_h;

    OPJ_UINT32 comp, r, b, k, ly, pp;
    for (comp = 0; comp < c->numcomps; comp++) {
        opj_tcd_tilecomp_t *tc = &tile->comps[comp];
        tc->compno = comp;
        tc->numresolutions = c->numres;
        tc->minimum_num_resolutions = c->numres;
        tc->x0 = 0; tc->y0 = 0; tc->x1 = (OPJ_INT32)GRID_BIG; tc->y1 = (OPJ_INT32)GRID_BIG;
        tc->resolutions = (opj_tcd_resolution_t *)opj_calloc(c->numres, sizeof(opj_tcd_resolution_t));
        for (r = 0; r < c->numres; r++) {
            opj_tcd_resolution_t *res = &tc->resolutions[r];
            res->pw = 1; res->ph = 1;
            res->numbands = numbands_of(r);
            res->x0 = 0; res->y0 = 0; res->x1 = BAND_XY; res->y1 = BAND_XY;
            for (b = 0; b < res->numbands; b++) {
                opj_tcd_band_t *band = &res->bands[b];
                band->bandno = bandno_of(r, b);
                band->x0 = 0; band->y0 = 0; band->x1 = BAND_XY; band->y1 = BAND_XY;
                band->numbps = 8;
                band->precincts = (opj_tcd_precinct_t *)opj_calloc(1, sizeof(opj_tcd_precinct_t));
                band->precincts_data_size = sizeof(opj_tcd_precinct_t);
                opj_tcd_precinct_t *prc = &band->precincts[0];
                prc->x0 = 0; prc->y0 = 0; prc->x1 = BAND_XY; prc->y1 = BAND_XY;
                prc->cw = c->cw; prc->ch = c->ch;
                prc->incltree = opj_tgt_create(c->cw, c->ch, mgr);
                prc->imsbtree = opj_tgt_create(c->cw, c->ch, mgr);
                OPJ_UINT32 nblk = c->cw * c->ch;
                prc->cblks.enc = (opj_tcd_cblk_enc_t *)opj_calloc(nblk, sizeof(opj_tcd_cblk_enc_t));
                for (k = 0; k < nblk; k++) {
                    opj_tcd_cblk_enc_t *cblk = &prc->cblks.enc[k];
                    cblk->x0 = 0; cblk->y0 = 0; cblk->x1 = BAND_XY; cblk->y1 = BAND_XY;
                    cblk->numbps = 3 + (k % 5); /* <= 8 */
                    OPJ_UINT32 total = c->numlayers * c->passes_per_layer;
                    cblk->totalpasses = total;
                    cblk->passes = (opj_tcd_pass_t *)opj_calloc(total, sizeof(opj_tcd_pass_t));
                    cblk->layers = (opj_tcd_layer_t *)opj_calloc(c->numlayers, sizeof(opj_tcd_layer_t));
                    OPJ_UINT32 passno = 0;
                    for (ly = 0; ly < c->numlayers; ly++) {
                        opj_tcd_layer_t *layer = &cblk->layers[ly];
                        layer->numpasses = c->passes_per_layer;
                        OPJ_UINT32 laylen = 0;
                        for (pp = 0; pp < c->passes_per_layer; pp++) {
                            OPJ_UINT32 plen = 1 + (rng_u32() % 7);
                            cblk->passes[passno].len = plen;
                            cblk->passes[passno].distortiondec = 0;
                            if (c->term_each) {
                                cblk->passes[passno].term = 1;
                            } else {
                                cblk->passes[passno].term = (pp == c->passes_per_layer - 1) ? 1 : 0;
                            }
                            laylen += plen;
                            passno++;
                        }
                        layer->len = laylen;
                        layer->disto = 0;
                        layer->data = (OPJ_BYTE *)opj_malloc(laylen ? laylen : 1);
                        OPJ_UINT32 di;
                        for (di = 0; di < laylen; di++) {
                            layer->data[di] = (OPJ_BYTE)(rng_u32() & 0xff);
                        }
                    }
                }
            }
        }
    }
    return tile;
}

static void free_enc_tile(opj_tcd_tile_t *tile, const t2cfg_t *c)
{
    OPJ_UINT32 comp, r, b, k;
    for (comp = 0; comp < c->numcomps; comp++) {
        opj_tcd_tilecomp_t *tc = &tile->comps[comp];
        for (r = 0; r < c->numres; r++) {
            opj_tcd_resolution_t *res = &tc->resolutions[r];
            for (b = 0; b < res->numbands; b++) {
                opj_tcd_band_t *band = &res->bands[b];
                opj_tcd_precinct_t *prc = &band->precincts[0];
                OPJ_UINT32 nblk = c->cw * c->ch;
                for (k = 0; k < nblk; k++) {
                    opj_tcd_cblk_enc_t *cblk = &prc->cblks.enc[k];
                    OPJ_UINT32 ly;
                    for (ly = 0; ly < c->numlayers; ly++) {
                        opj_free(cblk->layers[ly].data);
                    }
                    opj_free(cblk->layers);
                    opj_free(cblk->passes);
                }
                opj_free(prc->cblks.enc);
                opj_tgt_destroy(prc->incltree);
                opj_tgt_destroy(prc->imsbtree);
                opj_free(band->precincts);
            }
        }
        opj_free(tc->resolutions);
    }
    opj_free(tile->comps);
    opj_free(tile);
}

/* ---- decode tile (mirror, empty) -------------------------------------- */

static opj_tcd_tile_t *build_dec_tile(const t2cfg_t *c, opj_event_mgr_t *mgr)
{
    opj_tcd_tile_t *tile = (opj_tcd_tile_t *)opj_calloc(1, sizeof(opj_tcd_tile_t));
    tile->numcomps = c->numcomps;
    tile->comps = (opj_tcd_tilecomp_t *)opj_calloc(c->numcomps, sizeof(opj_tcd_tilecomp_t));
    tile->x0 = 0; tile->y0 = 0; tile->x1 = c->img_w; tile->y1 = c->img_h;

    OPJ_UINT32 comp, r, b, k;
    for (comp = 0; comp < c->numcomps; comp++) {
        opj_tcd_tilecomp_t *tc = &tile->comps[comp];
        tc->compno = comp;
        tc->numresolutions = c->numres;
        tc->minimum_num_resolutions = c->numres;
        tc->x0 = 0; tc->y0 = 0; tc->x1 = (OPJ_INT32)GRID_BIG; tc->y1 = (OPJ_INT32)GRID_BIG;
        tc->resolutions = (opj_tcd_resolution_t *)opj_calloc(c->numres, sizeof(opj_tcd_resolution_t));
        for (r = 0; r < c->numres; r++) {
            opj_tcd_resolution_t *res = &tc->resolutions[r];
            res->pw = 1; res->ph = 1;
            res->numbands = numbands_of(r);
            res->x0 = 0; res->y0 = 0; res->x1 = BAND_XY; res->y1 = BAND_XY;
            for (b = 0; b < res->numbands; b++) {
                opj_tcd_band_t *band = &res->bands[b];
                band->bandno = bandno_of(r, b);
                band->x0 = 0; band->y0 = 0; band->x1 = BAND_XY; band->y1 = BAND_XY;
                band->numbps = 8;
                band->precincts = (opj_tcd_precinct_t *)opj_calloc(1, sizeof(opj_tcd_precinct_t));
                band->precincts_data_size = sizeof(opj_tcd_precinct_t);
                opj_tcd_precinct_t *prc = &band->precincts[0];
                prc->x0 = 0; prc->y0 = 0; prc->x1 = BAND_XY; prc->y1 = BAND_XY;
                prc->cw = c->cw; prc->ch = c->ch;
                prc->incltree = opj_tgt_create(c->cw, c->ch, mgr);
                prc->imsbtree = opj_tgt_create(c->cw, c->ch, mgr);
                OPJ_UINT32 nblk = c->cw * c->ch;
                prc->cblks.dec = (opj_tcd_cblk_dec_t *)opj_calloc(nblk, sizeof(opj_tcd_cblk_dec_t));
                for (k = 0; k < nblk; k++) {
                    opj_tcd_cblk_dec_t *cblk = &prc->cblks.dec[k];
                    cblk->x0 = 0; cblk->y0 = 0; cblk->x1 = BAND_XY; cblk->y1 = BAND_XY;
                }
            }
        }
    }
    return tile;
}

static void free_dec_tile(opj_tcd_tile_t *tile, const t2cfg_t *c)
{
    OPJ_UINT32 comp, r, b, k;
    for (comp = 0; comp < c->numcomps; comp++) {
        opj_tcd_tilecomp_t *tc = &tile->comps[comp];
        for (r = 0; r < c->numres; r++) {
            opj_tcd_resolution_t *res = &tc->resolutions[r];
            for (b = 0; b < res->numbands; b++) {
                opj_tcd_band_t *band = &res->bands[b];
                opj_tcd_precinct_t *prc = &band->precincts[0];
                OPJ_UINT32 nblk = c->cw * c->ch;
                for (k = 0; k < nblk; k++) {
                    opj_tcd_cblk_dec_t *cblk = &prc->cblks.dec[k];
                    opj_free(cblk->segs);
                    opj_free(cblk->chunks);
                }
                opj_free(prc->cblks.dec);
                opj_tgt_destroy(prc->incltree);
                opj_tgt_destroy(prc->imsbtree);
                opj_free(band->precincts);
            }
        }
        opj_free(tc->resolutions);
    }
    opj_free(tile->comps);
    opj_free(tile);
}

/* ---- emit helpers ------------------------------------------------------ */

/* wr_hex_raw writes n bytes as hex with no empty-marker. */
static void wr_hex_raw(FILE *f, const OPJ_BYTE *data, OPJ_UINT32 n)
{
    OPJ_UINT32 i;
    for (i = 0; i < n; i++) {
        fprintf(f, "%02x", data[i]);
    }
}

/* wr_hex writes n bytes as hex, or "-" when n == 0 (so a field is never empty). */
static void wr_hex(FILE *f, const OPJ_BYTE *data, OPJ_UINT32 n)
{
    if (n == 0) {
        fprintf(f, "-");
        return;
    }
    wr_hex_raw(f, data, n);
}

static void emit_tile_desc(FILE *f, const t2cfg_t *c, opj_tcd_tile_t *tile)
{
    OPJ_UINT32 comp, r, b, k, ly, pp;
    for (comp = 0; comp < c->numcomps; comp++) {
        opj_tcd_tilecomp_t *tc = &tile->comps[comp];
        fprintf(f, "COMP %u numres %u\n", comp, c->numres);
        for (r = 0; r < c->numres; r++) {
            opj_tcd_resolution_t *res = &tc->resolutions[r];
            for (b = 0; b < res->numbands; b++) {
                opj_tcd_band_t *band = &res->bands[b];
                opj_tcd_precinct_t *prc = &band->precincts[0];
                fprintf(f, "BAND %u %u %u numbps %d cw %u ch %u\n", comp, r, band->bandno,
                        band->numbps, prc->cw, prc->ch);
                OPJ_UINT32 nblk = c->cw * c->ch;
                for (k = 0; k < nblk; k++) {
                    opj_tcd_cblk_enc_t *cblk = &prc->cblks.enc[k];
                    fprintf(f, "CBLK %u %u %u %u numbps %u numlayers %u totalpasses %u\n",
                            comp, r, band->bandno, k, cblk->numbps, c->numlayers, cblk->totalpasses);
                    OPJ_UINT32 passno = 0;
                    for (ly = 0; ly < c->numlayers; ly++) {
                        opj_tcd_layer_t *layer = &cblk->layers[ly];
                        fprintf(f, "LAYER %u %u %u %u %u np %u len %u data ",
                                comp, r, band->bandno, k, ly, layer->numpasses, layer->len);
                        wr_hex(f, layer->data, layer->len);
                        fprintf(f, "\n");
                        for (pp = 0; pp < layer->numpasses; pp++) {
                            opj_tcd_pass_t *pass = &cblk->passes[passno];
                            fprintf(f, "PASS %u %u %u %u %u len %u term %d\n",
                                    comp, r, band->bandno, k, passno, pass->len, (int)pass->term);
                            passno++;
                        }
                    }
                }
            }
        }
    }
}

static void emit_decode(FILE *f, const t2cfg_t *c, opj_tcd_tile_t *tile)
{
    OPJ_UINT32 comp, r, b, k, s, ci;
    for (comp = 0; comp < c->numcomps; comp++) {
        opj_tcd_tilecomp_t *tc = &tile->comps[comp];
        for (r = 0; r < c->numres; r++) {
            opj_tcd_resolution_t *res = &tc->resolutions[r];
            for (b = 0; b < res->numbands; b++) {
                opj_tcd_band_t *band = &res->bands[b];
                opj_tcd_precinct_t *prc = &band->precincts[0];
                OPJ_UINT32 nblk = c->cw * c->ch;
                for (k = 0; k < nblk; k++) {
                    opj_tcd_cblk_dec_t *cblk = &prc->cblks.dec[k];
                    fprintf(f, "DCBLK %u %u %u %u numsegs %u realnumsegs %u corrupted %d numchunks %u\n",
                            comp, r, band->bandno, k, cblk->numsegs, cblk->real_num_segs,
                            (int)cblk->corrupted, cblk->numchunks);
                    for (s = 0; s < cblk->numsegs; s++) {
                        opj_tcd_seg_t *seg = &cblk->segs[s];
                        fprintf(f, "DSEG %u %u %u %u %u len %u numpasses %u realnumpasses %u\n",
                                comp, r, band->bandno, k, s, seg->len, seg->numpasses,
                                seg->real_num_passes);
                    }
                    /* concatenated chunk data */
                    fprintf(f, "DDATA %u %u %u %u ", comp, r, band->bandno, k);
                    OPJ_UINT32 total = 0;
                    for (ci = 0; ci < cblk->numchunks; ci++) {
                        total += cblk->chunks[ci].len;
                    }
                    fprintf(f, "%u ", total);
                    if (total == 0) {
                        fprintf(f, "-");
                    } else {
                        for (ci = 0; ci < cblk->numchunks; ci++) {
                            wr_hex_raw(f, cblk->chunks[ci].data, cblk->chunks[ci].len);
                        }
                    }
                    fprintf(f, "\n");
                }
            }
        }
    }
}

/* ---- run one case ------------------------------------------------------ */

static OPJ_BYTE g_dest[1 << 18];

static void run_case(FILE *f, const t2cfg_t *c, opj_event_mgr_t *mgr)
{
    rng_seed(c->seed);

    opj_image_t *img = build_image(c);
    opj_cp_t cp_enc;
    build_cp(c, &cp_enc);

    opj_tcd_tile_t *etile = build_enc_tile(c, mgr);

    fprintf(f, "CASE %s csty %u prg %d maxlayers %u numcomps %u numres %u cw %u ch %u "
               "numlayers %u ppl %u cblksty %u term_each %u imgw %u imgh %u ltd %u trunc %u strict %d\n",
            c->name, c->csty, c->prg, c->maxlayers, c->numcomps, c->numres, c->cw, c->ch,
            c->numlayers, c->passes_per_layer, c->cblksty, c->term_each, c->img_w, c->img_h,
            c->num_layers_to_decode, c->trunc, (int)c->strict);
    emit_tile_desc(f, c, etile);

    opj_t2_t *t2e = opj_t2_create(img, &cp_enc);
    OPJ_UINT32 written = 0;
    memset(g_dest, 0, sizeof(g_dest));
    OPJ_BOOL enc_ok = opj_t2_encode_packets(t2e, 0, etile, c->maxlayers, g_dest, &written,
                                            sizeof(g_dest), NULL, NULL, 0, 0, 0, FINAL_PASS, mgr);
    fprintf(f, "ENC %d %u ", (int)enc_ok, written);
    wr_hex(f, g_dest, written);
    fprintf(f, "\n");
    opj_t2_destroy(t2e);

    /* decode over (possibly truncated) encoded bytes on a mirror tile */
    OPJ_UINT32 declen = (c->trunc > 0 && c->trunc < written) ? c->trunc : written;

    opj_cp_t cp_dec;
    build_cp(c, &cp_dec);
    cp_dec.strict = c->strict;
    opj_tcd_tile_t *dtile = build_dec_tile(c, mgr);

    /* Minimal opj_tcd_t for opj_tcd_is_subband_area_of_interest (whole grid). */
    opj_tcd_t tcd;
    memset(&tcd, 0, sizeof(tcd));
    opj_tcd_image_t tcd_image;
    memset(&tcd_image, 0, sizeof(tcd_image));
    tcd_image.tiles = dtile;
    tcd.tcd_image = &tcd_image;
    tcd.image = img;
    tcd.cp = &cp_dec;
    tcd.tcp = cp_dec.tcps;
    tcd.win_x0 = 0; tcd.win_y0 = 0; tcd.win_x1 = GRID_BIG; tcd.win_y1 = GRID_BIG;
    tcd.whole_tile_decoding = 1;

    opj_t2_t *t2d = opj_t2_create(img, &cp_dec);
    OPJ_UINT32 read = 0;
    OPJ_BOOL dec_ok = opj_t2_decode_packets(&tcd, t2d, 0, dtile, g_dest, &read, declen, NULL, mgr);
    fprintf(f, "DEC %d %u declen %u\n", (int)dec_ok, read, declen);
    emit_decode(f, c, dtile);
    opj_t2_destroy(t2d);

    free_enc_tile(etile, c);
    free_dec_tile(dtile, c);
    free_cp(&cp_enc);
    free_cp(&cp_dec);
    opj_image_destroy(img);
}

/* ---- case matrix ------------------------------------------------------- */

static t2cfg_t g_cases[400];
static int g_ncase;

static t2cfg_t base_case(const char *name, OPJ_UINT32 nc, OPJ_UINT32 nr,
                         OPJ_UINT32 cw, OPJ_UINT32 ch, OPJ_UINT32 nl, OPJ_UINT32 ppl)
{
    t2cfg_t c;
    memset(&c, 0, sizeof(c));
    c.name = name;
    c.numcomps = nc; c.numres = nr; c.cw = cw; c.ch = ch;
    c.numlayers = nl; c.passes_per_layer = ppl;
    c.csty = 0; c.prg = OPJ_LRCP;
    c.maxlayers = nl; c.num_layers_to_decode = nl;
    c.cblksty = 0; c.term_each = 0;
    c.img_w = 64; c.img_h = 64;
    c.seed = 0x1234567 + g_ncase * 0x9e37;
    c.trunc = 0; c.strict = 0;
    return c;
}

static void add(t2cfg_t c) { g_cases[g_ncase++] = c; }

static void build_cases(void)
{
    static char names[400][48];
    int prgs[5] = {OPJ_LRCP, OPJ_RLCP, OPJ_RPCL, OPJ_PCRL, OPJ_CPRL};
    const char *pn[5] = {"lrcp", "rlcp", "rpcl", "pcrl", "cprl"};
    OPJ_UINT32 cstys[4] = {0, J2K_CP_CSTY_SOP, J2K_CP_CSTY_EPH, J2K_CP_CSTY_SOP | J2K_CP_CSTY_EPH};
    const char *cn[4] = {"none", "sop", "eph", "sopeph"};

    /* geometry variants */
    struct { OPJ_UINT32 nc, nr, cw, ch, nl, ppl; } geoms[] = {
        {1, 1, 1, 1, 1, 1},
        {1, 2, 1, 1, 2, 2},
        {1, 3, 2, 2, 2, 1},
        {2, 3, 2, 1, 3, 2},
        {2, 2, 1, 2, 1, 3},
    };
    int ngeom = (int)(sizeof(geoms) / sizeof(geoms[0]));

    int gi, pi, si;
    /* (A) all progressions x csty=0 across geometries */
    for (gi = 0; gi < ngeom; gi++) {
        for (pi = 0; pi < 5; pi++) {
            snprintf(names[g_ncase], 48, "g%d_%s_none", gi, pn[pi]);
            t2cfg_t c = base_case(names[g_ncase], geoms[gi].nc, geoms[gi].nr, geoms[gi].cw,
                                  geoms[gi].ch, geoms[gi].nl, geoms[gi].ppl);
            c.prg = prgs[pi];
            add(c);
        }
    }
    /* (B) all csty x prg=LRCP across geometries */
    for (gi = 0; gi < ngeom; gi++) {
        for (si = 0; si < 4; si++) {
            snprintf(names[g_ncase], 48, "g%d_lrcp_%s", gi, cn[si]);
            t2cfg_t c = base_case(names[g_ncase], geoms[gi].nc, geoms[gi].nr, geoms[gi].cw,
                                  geoms[gi].ch, geoms[gi].nl, geoms[gi].ppl);
            c.csty = cstys[si];
            add(c);
        }
    }
    /* (C) cblksty variants (TERMALL, LAZY) with term_each */
    {
        snprintf(names[g_ncase], 48, "termall_r3");
        t2cfg_t c = base_case(names[g_ncase], 1, 3, 2, 1, 3, 3);
        c.cblksty = J2K_CCP_CBLKSTY_TERMALL; c.term_each = 1;
        add(c);
    }
    {
        snprintf(names[g_ncase], 48, "lazy_r2");
        t2cfg_t c = base_case(names[g_ncase], 1, 2, 1, 1, 3, 4);
        c.cblksty = J2K_CCP_CBLKSTY_LAZY;
        add(c);
    }
    /* (D) partial-layer decode (num_layers_to_decode < numlayers) */
    {
        snprintf(names[g_ncase], 48, "laydecode_2of4");
        t2cfg_t c = base_case(names[g_ncase], 1, 2, 2, 1, 4, 1);
        c.num_layers_to_decode = 2;
        add(c);
    }
    /* (E) maxlayers < numlayers on encode */
    {
        snprintf(names[g_ncase], 48, "maxlay_2of3");
        t2cfg_t c = base_case(names[g_ncase], 2, 2, 1, 1, 3, 2);
        c.maxlayers = 2;
        add(c);
    }
    /* (F) truncated decode, non-strict (tolerant) */
    {
        snprintf(names[g_ncase], 48, "trunc_nonstrict_sopeph");
        t2cfg_t c = base_case(names[g_ncase], 2, 3, 2, 2, 2, 2);
        c.csty = J2K_CP_CSTY_SOP | J2K_CP_CSTY_EPH;
        c.trunc = 12; c.strict = 0;
        add(c);
    }
    {
        snprintf(names[g_ncase], 48, "trunc_nonstrict_r3");
        t2cfg_t c = base_case(names[g_ncase], 1, 3, 2, 2, 3, 2);
        c.trunc = 20; c.strict = 0;
        add(c);
    }
    /* (G) truncated decode, strict (hard error) */
    {
        snprintf(names[g_ncase], 48, "trunc_strict_r3");
        t2cfg_t c = base_case(names[g_ncase], 1, 3, 2, 2, 3, 2);
        c.trunc = 20; c.strict = 1;
        add(c);
    }
}

static void quiet_cb(const char *msg, void *d) { (void)msg; (void)d; }

int main(int argc, char **argv)
{
    if (argc < 2) {
        fprintf(stderr, "usage: %s out.txt\n", argv[0]);
        return 1;
    }
    FILE *f = fopen(argv[1], "wb");
    if (!f) { perror("fopen"); return 1; }

    opj_event_mgr_t mgr;
    memset(&mgr, 0, sizeof(mgr));
    mgr.error_handler = quiet_cb;
    mgr.warning_handler = quiet_cb;
    mgr.info_handler = quiet_cb;

    build_cases();

    fprintf(f, "T2VEC 1 %d\n", g_ncase);
    int i;
    for (i = 0; i < g_ncase; i++) {
        run_case(f, &g_cases[i], &mgr);
    }
    fclose(f);
    fprintf(stderr, "wrote %d t2 cases to %s\n", g_ncase, argv[1]);
    return 0;
}
