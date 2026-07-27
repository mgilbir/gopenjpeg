/* W6 packet-iterator (pi) oracle harness.
 *
 * Links against libopenjp2.a and drives the non-static pi.c entry points
 * (opj_pi_create_decode, opj_pi_initialise_encode, opj_pi_create_encode,
 * opj_pi_next, opj_pi_destroy) over a curated matrix of synthetic coding
 * parameters. For each config it emits a self-describing text record: all
 * parameters needed to rebuild the image / cp / tcp / tccp on the Go side,
 * followed by the full decode and encode iteration sequences.
 *
 * Build (from repo root):
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w6/pi_harness.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/w6pi
 *   /tmp/w6pi testdata/vectors/pi/pi_vectors.txt
 */

#include "opj_includes.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MAXCOMPS 4
#define MAXRES 8
#define MAXPOCS 8
#define SEQ_CAP 5000 /* per-sequence packet cap; noted in output */

typedef struct {
    const char *name;
    OPJ_UINT32 imgx0, imgy0, imgx1, imgy1;
    OPJ_UINT32 numcomps;
    OPJ_UINT32 dx[MAXCOMPS], dy[MAXCOMPS];
    OPJ_UINT32 tw, th;
    OPJ_UINT32 tdx, tdy;
    OPJ_UINT32 tx0, ty0;
    OPJ_UINT32 tileno;
    int prg; /* base progression */
    OPJ_UINT32 numlayers;
    OPJ_UINT32 numres[MAXCOMPS];
    OPJ_UINT32 prcw[MAXCOMPS][MAXRES];
    OPJ_UINT32 prch[MAXCOMPS][MAXRES];
    /* POC */
    OPJ_UINT32 use_poc;
    OPJ_UINT32 numpocs; /* number of poc records - 1 (i.e. tcp->numpocs) */
    struct {
        int prg;
        OPJ_UINT32 resno0, resno1, compno0, compno1, layno1;
    } pocs[MAXPOCS];
} cfg_t;

static opj_image_t *build_image(const cfg_t *c)
{
    opj_image_cmptparm_t parms[MAXCOMPS];
    memset(parms, 0, sizeof(parms));
    OPJ_UINT32 i;
    for (i = 0; i < c->numcomps; i++) {
        parms[i].dx = c->dx[i];
        parms[i].dy = c->dy[i];
        parms[i].w = (c->imgx1 - c->imgx0 + c->dx[i] - 1) / c->dx[i];
        parms[i].h = (c->imgy1 - c->imgy0 + c->dy[i] - 1) / c->dy[i];
        parms[i].x0 = c->imgx0;
        parms[i].y0 = c->imgy0;
        parms[i].prec = 8;
        parms[i].sgnd = 0;
    }
    opj_image_t *img = opj_image_create(c->numcomps, parms, OPJ_CLRSPC_GRAY);
    img->x0 = c->imgx0;
    img->y0 = c->imgy0;
    img->x1 = c->imgx1;
    img->y1 = c->imgy1;
    return img;
}

/* Allocates cp->tcps sized to the tile grid; fills the tested tile's tcp. */
static void build_cp(const cfg_t *c, opj_cp_t *cp)
{
    OPJ_UINT32 comp, r, p;
    memset(cp, 0, sizeof(*cp));

    cp->rsiz = OPJ_PROFILE_NONE;
    cp->tx0 = c->tx0;
    cp->ty0 = c->ty0;
    cp->tdx = c->tdx;
    cp->tdy = c->tdy;
    cp->tw = c->tw;
    cp->th = c->th;
    cp->tcps = (opj_tcp_t *)opj_calloc((size_t)c->tw * c->th, sizeof(opj_tcp_t));
    cp->m_specific_param.m_enc.m_tp_on = 0;

    opj_tcp_t *tcp = &cp->tcps[c->tileno];
    opj_tccp_t *tccps = (opj_tccp_t *)opj_calloc(c->numcomps, sizeof(opj_tccp_t));

    tcp->csty = 0;
    tcp->prg = (OPJ_PROG_ORDER)c->prg;
    tcp->numlayers = c->numlayers;
    tcp->num_layers_to_decode = c->numlayers;
    tcp->numpocs = c->use_poc ? c->numpocs : 0;
    tcp->POC = c->use_poc ? 1 : 0;
    tcp->tccps = tccps;

    for (comp = 0; comp < c->numcomps; comp++) {
        tccps[comp].numresolutions = c->numres[comp];
        for (r = 0; r < c->numres[comp]; r++) {
            tccps[comp].prcw[r] = c->prcw[comp][r];
            tccps[comp].prch[r] = c->prch[comp][r];
        }
        /* fill remaining prc entries with 15 (default) to be safe */
        for (p = c->numres[comp]; p < OPJ_J2K_MAXRLVLS; p++) {
            tccps[comp].prcw[p] = 15;
            tccps[comp].prch[p] = 15;
        }
    }

    if (c->use_poc) {
        OPJ_UINT32 i;
        for (i = 0; i <= c->numpocs; i++) {
            tcp->pocs[i].prg = (OPJ_PROG_ORDER)c->pocs[i].prg;
            tcp->pocs[i].prg1 = (OPJ_PROG_ORDER)c->pocs[i].prg;
            tcp->pocs[i].resno0 = c->pocs[i].resno0;
            tcp->pocs[i].resno1 = c->pocs[i].resno1;
            tcp->pocs[i].compno0 = c->pocs[i].compno0;
            tcp->pocs[i].compno1 = c->pocs[i].compno1;
            tcp->pocs[i].layno1 = c->pocs[i].layno1;
            tcp->pocs[i].precno0 = 0;
            tcp->pocs[i].precno1 = 1;
        }
    }
}

static void emit_cfg(FILE *f, const cfg_t *c)
{
    OPJ_UINT32 comp, r, i;
    fprintf(f, "CONFIG %s\n", c->name);
    fprintf(f, "IMG %u %u %u %u\n", c->imgx0, c->imgy0, c->imgx1, c->imgy1);
    fprintf(f, "COMPS %u\n", c->numcomps);
    for (comp = 0; comp < c->numcomps; comp++) {
        fprintf(f, "COMP %u %u %u %u\n", comp, c->dx[comp], c->dy[comp], c->numres[comp]);
        for (r = 0; r < c->numres[comp]; r++) {
            fprintf(f, "RES %u %u %u %u\n", comp, r, c->prcw[comp][r], c->prch[comp][r]);
        }
    }
    fprintf(f, "TILE %u %u %u %u %u %u %u\n", c->tw, c->th, c->tdx, c->tdy, c->tx0, c->ty0, c->tileno);
    fprintf(f, "PRG %d %u\n", c->prg, c->numlayers);
    fprintf(f, "POC %u %u\n", c->use_poc, c->use_poc ? c->numpocs : 0);
    if (c->use_poc) {
        for (i = 0; i <= c->numpocs; i++) {
            fprintf(f, "POCLINE %u %d %u %u %u %u %u\n", i, c->pocs[i].prg,
                    c->pocs[i].resno0, c->pocs[i].resno1, c->pocs[i].compno0,
                    c->pocs[i].compno1, c->pocs[i].layno1);
        }
    }
}

static void run_decode(FILE *f, const cfg_t *c, opj_image_t *img, opj_cp_t *cp, opj_event_mgr_t *mgr)
{
    opj_pi_iterator_t *pi = opj_pi_create_decode(img, cp, c->tileno, mgr);
    if (!pi) {
        fprintf(f, "DECODE NULL\n");
        return;
    }
    OPJ_UINT32 nb_pocs = cp->tcps[c->tileno].numpocs + 1;
    /* Buffer the sequence so we can print the count first. */
    static OPJ_UINT32 buf[SEQ_CAP][4];
    OPJ_UINT32 n = 0, pino;
    int capped = 0;
    for (pino = 0; pino < nb_pocs; pino++) {
        opj_pi_iterator_t *cur = &pi[pino];
        while (opj_pi_next(cur)) {
            if (n < SEQ_CAP) {
                buf[n][0] = cur->compno;
                buf[n][1] = cur->resno;
                buf[n][2] = cur->precno;
                buf[n][3] = cur->layno;
                n++;
            } else {
                capped = 1;
            }
        }
    }
    fprintf(f, "DECODE %u %d\n", n, capped);
    OPJ_UINT32 k;
    for (k = 0; k < n; k++) {
        fprintf(f, "%u %u %u %u\n", buf[k][0], buf[k][1], buf[k][2], buf[k][3]);
    }
    opj_pi_destroy(pi, nb_pocs);
}

static void run_encode(FILE *f, const cfg_t *c, opj_image_t *img, opj_cp_t *cp, opj_event_mgr_t *mgr)
{
    opj_pi_iterator_t *pi = opj_pi_initialise_encode(img, cp, c->tileno, FINAL_PASS, mgr);
    if (!pi) {
        fprintf(f, "ENCODE NULL\n");
        return;
    }
    OPJ_UINT32 nb_pocs = cp->tcps[c->tileno].numpocs + 1;
    OPJ_INT32 tppos = cp->m_specific_param.m_enc.m_tp_pos;
    static OPJ_UINT32 buf[SEQ_CAP][4];
    OPJ_UINT32 n = 0, pino;
    int capped = 0;
    for (pino = 0; pino < nb_pocs; pino++) {
        opj_pi_create_encode(pi, cp, c->tileno, pino, 0, tppos, FINAL_PASS);
        opj_pi_iterator_t *cur = &pi[pino];
        while (opj_pi_next(cur)) {
            if (n < SEQ_CAP) {
                buf[n][0] = cur->compno;
                buf[n][1] = cur->resno;
                buf[n][2] = cur->precno;
                buf[n][3] = cur->layno;
                n++;
            } else {
                capped = 1;
            }
        }
    }
    fprintf(f, "ENCODE %u %d\n", n, capped);
    OPJ_UINT32 k;
    for (k = 0; k < n; k++) {
        fprintf(f, "%u %u %u %u\n", buf[k][0], buf[k][1], buf[k][2], buf[k][3]);
    }
    opj_pi_destroy(pi, nb_pocs);
}

/* ---- config matrix -------------------------------------------------- */

#define NCFG (sizeof(g_cfgs) / sizeof(g_cfgs[0]))
static cfg_t g_cfgs[200];
static int g_ncfg;

static void add(cfg_t c)
{
    g_cfgs[g_ncfg++] = c;
}

/* helper: base config with uniform precincts (default 15) */
static cfg_t base_cfg(const char *name, OPJ_UINT32 w, OPJ_UINT32 h, OPJ_UINT32 nc,
                      int prg, OPJ_UINT32 layers, OPJ_UINT32 res)
{
    cfg_t c;
    memset(&c, 0, sizeof(c));
    c.name = name;
    c.imgx0 = 0;
    c.imgy0 = 0;
    c.imgx1 = w;
    c.imgy1 = h;
    c.numcomps = nc;
    OPJ_UINT32 i, r;
    for (i = 0; i < nc; i++) {
        c.dx[i] = 1;
        c.dy[i] = 1;
        c.numres[i] = res;
        for (r = 0; r < MAXRES; r++) {
            c.prcw[i][r] = 15;
            c.prch[i][r] = 15;
        }
    }
    c.tw = 1;
    c.th = 1;
    c.tdx = w;
    c.tdy = h;
    c.tx0 = 0;
    c.ty0 = 0;
    c.tileno = 0;
    c.prg = prg;
    c.numlayers = layers;
    return c;
}

static void build_matrix(void)
{
    int prgs[5] = {OPJ_LRCP, OPJ_RLCP, OPJ_RPCL, OPJ_PCRL, OPJ_CPRL};
    const char *pn[5] = {"lrcp", "rlcp", "rpcl", "pcrl", "cprl"};
    char namebuf[4096];
    static char names[4000][40];
    int ni = 0;
    int p;

    /* 1) all progressions, single comp, small images, various res/layers */
    for (p = 0; p < 5; p++) {
        OPJ_UINT32 sizes[][2] = {{16, 16}, {33, 17}, {64, 64}, {7, 5}};
        OPJ_UINT32 s;
        for (s = 0; s < 4; s++) {
            OPJ_UINT32 res[] = {1, 3, 5};
            OPJ_UINT32 ri;
            for (ri = 0; ri < 3; ri++) {
                OPJ_UINT32 lay[] = {1, 3};
                OPJ_UINT32 li;
                for (li = 0; li < 2; li++) {
                    snprintf(names[ni], 40, "%s_%ux%u_r%u_l%u", pn[p], sizes[s][0], sizes[s][1], res[ri], lay[li]);
                    cfg_t c = base_cfg(names[ni], sizes[s][0], sizes[s][1], 1, prgs[p], lay[li], res[ri]);
                    add(c);
                    ni++;
                }
            }
        }
    }

    /* 2) multi-component, uniform dx/dy */
    for (p = 0; p < 5; p++) {
        OPJ_UINT32 ncs[] = {2, 3, 4};
        OPJ_UINT32 ci;
        for (ci = 0; ci < 3; ci++) {
            snprintf(names[ni], 40, "%s_mc%u_r4_l2", pn[p], ncs[ci]);
            cfg_t c = base_cfg(names[ni], 64, 48, ncs[ci], prgs[p], 2, 4);
            add(c);
            ni++;
        }
    }

    /* 3) subsampled components (dx/dy = 2 on chroma) */
    for (p = 0; p < 5; p++) {
        snprintf(names[ni], 40, "%s_sub_r4_l2", pn[p]);
        cfg_t c = base_cfg(names[ni], 64, 64, 3, prgs[p], 2, 4);
        c.dx[1] = 2; c.dy[1] = 2;
        c.dx[2] = 2; c.dy[2] = 2;
        add(c);
        ni++;
    }

    /* 4) custom precinct sizes per resolution */
    for (p = 0; p < 5; p++) {
        snprintf(names[ni], 40, "%s_prc_r5_l2", pn[p]);
        cfg_t c = base_cfg(names[ni], 128, 128, 2, prgs[p], 2, 5);
        OPJ_UINT32 i;
        OPJ_UINT32 pw[5] = {2, 3, 4, 5, 6};
        for (i = 0; i < 2; i++) {
            OPJ_UINT32 r;
            for (r = 0; r < 5; r++) { c.prcw[i][r] = pw[r]; c.prch[i][r] = pw[r]; }
        }
        add(c);
        ni++;
    }

    /* 5) multi-tile, dump an interior tile */
    for (p = 0; p < 5; p++) {
        snprintf(names[ni], 40, "%s_tiles_t3", pn[p]);
        cfg_t c = base_cfg(names[ni], 100, 80, 2, prgs[p], 2, 4);
        c.tw = 2; c.th = 2;
        c.tdx = 50; c.tdy = 40;
        c.tileno = 3; /* bottom-right */
        add(c);
        ni++;
    }
    for (p = 0; p < 5; p++) {
        snprintf(names[ni], 40, "%s_tiles_t1", pn[p]);
        cfg_t c = base_cfg(names[ni], 100, 80, 1, prgs[p], 3, 5);
        c.tw = 3; c.th = 1;
        c.tdx = 34; c.tdy = 80;
        c.tileno = 1;
        add(c);
        ni++;
    }

    /* 6) image origin offset (nonzero x0/y0) */
    for (p = 0; p < 5; p++) {
        snprintf(names[ni], 40, "%s_off_r4", pn[p]);
        cfg_t c = base_cfg(names[ni], 70, 70, 2, prgs[p], 2, 4);
        c.imgx0 = 5; c.imgy0 = 3;
        c.imgx1 = 70; c.imgy1 = 70;
        c.tdx = 65; c.tdy = 67;
        add(c);
        ni++;
    }

    /* 7) larger layer counts */
    for (p = 0; p < 5; p++) {
        snprintf(names[ni], 40, "%s_l5_r3", pn[p]);
        cfg_t c = base_cfg(names[ni], 32, 32, 2, prgs[p], 5, 3);
        add(c);
        ni++;
    }

    /* 8) POC single-record (POC flag set, one poc) */
    {
        snprintf(names[ni], 40, "poc1_lrcp");
        cfg_t c = base_cfg(names[ni], 64, 64, 3, OPJ_LRCP, 3, 4);
        c.use_poc = 1; c.numpocs = 0;
        c.pocs[0].prg = OPJ_LRCP;
        c.pocs[0].resno0 = 0; c.pocs[0].resno1 = 4;
        c.pocs[0].compno0 = 0; c.pocs[0].compno1 = 3;
        c.pocs[0].layno1 = 3;
        add(c); ni++;
    }

    /* 9) POC multi-record: RLCP then CPRL */
    {
        snprintf(names[ni], 40, "poc2_rlcp_cprl");
        cfg_t c = base_cfg(names[ni], 64, 64, 3, OPJ_RLCP, 3, 4);
        c.use_poc = 1; c.numpocs = 1;
        c.pocs[0].prg = OPJ_RLCP;
        c.pocs[0].resno0 = 0; c.pocs[0].resno1 = 2;
        c.pocs[0].compno0 = 0; c.pocs[0].compno1 = 3;
        c.pocs[0].layno1 = 1;
        c.pocs[1].prg = OPJ_CPRL;
        c.pocs[1].resno0 = 0; c.pocs[1].resno1 = 4;
        c.pocs[1].compno0 = 0; c.pocs[1].compno1 = 3;
        c.pocs[1].layno1 = 3;
        add(c); ni++;
    }

    /* 10) POC multi-record: three records with staged resolutions */
    {
        snprintf(names[ni], 40, "poc3_staged");
        cfg_t c = base_cfg(names[ni], 96, 96, 2, OPJ_RPCL, 4, 5);
        c.use_poc = 1; c.numpocs = 2;
        c.pocs[0].prg = OPJ_RLCP; c.pocs[0].resno0 = 0; c.pocs[0].resno1 = 1;
        c.pocs[0].compno0 = 0; c.pocs[0].compno1 = 2; c.pocs[0].layno1 = 2;
        c.pocs[1].prg = OPJ_RPCL; c.pocs[1].resno0 = 1; c.pocs[1].resno1 = 3;
        c.pocs[1].compno0 = 0; c.pocs[1].compno1 = 2; c.pocs[1].layno1 = 4;
        c.pocs[2].prg = OPJ_CPRL; c.pocs[2].resno0 = 3; c.pocs[2].resno1 = 5;
        c.pocs[2].compno0 = 0; c.pocs[2].compno1 = 2; c.pocs[2].layno1 = 4;
        add(c); ni++;
    }

    (void)namebuf;
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

    build_matrix();

    fprintf(f, "PIVEC 1 %d\n", g_ncfg);
    int i;
    for (i = 0; i < g_ncfg; i++) {
        cfg_t *c = &g_cfgs[i];
        opj_image_t *img = build_image(c);
        opj_cp_t cp;
        build_cp(c, &cp);

        emit_cfg(f, c);
        run_decode(f, c, img, &cp, &mgr);
        run_encode(f, c, img, &cp, &mgr);

        opj_free(cp.tcps[c->tileno].tccps);
        opj_free(cp.tcps);
        opj_image_destroy(img);
    }

    fclose(f);
    fprintf(stderr, "wrote %d configs to %s\n", g_ncfg, argv[1]);
    return 0;
}
