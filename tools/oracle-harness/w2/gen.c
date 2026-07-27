/*
 * gopenjpeg W2 (internal/mqc) oracle vector generator.
 *
 * Links against the reference libopenjp2.a and drives the C MQ coder to emit
 * a JSON file of encode/decode/bypass/raw test vectors, consumed by the Go
 * tests in internal/mqc. Gitignored + regenerable (see testdata README).
 *
 * Build (from oracle/openjpeg):
 *   gcc -O2 -I src/lib/openjp2 -I build/src/lib/openjp2 \
 *       ../harness/w2/gen.c build/bin/libopenjp2.a -lm -o <out>/genw2
 * Run:
 *   <out>/genw2 > ../../testdata/vectors/mqc/mqc_vectors.json
 */
#include "opj_includes.h"
#include <stdio.h>
#include <string.h>

/* ---- deterministic PRNG (xorshift32) --------------------------------- */
static OPJ_UINT32 rng_state;
static void rng_seed(OPJ_UINT32 s) { rng_state = s ? s : 0x1234567u; }
static OPJ_UINT32 rng_next(void)
{
    OPJ_UINT32 x = rng_state;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    rng_state = x;
    return x;
}

/* ---- T1 standard initial context states ------------------------------ */
static void reset_std(opj_mqc_t *mqc)
{
    opj_mqc_resetstates(mqc);
    opj_mqc_setstate(mqc, T1_CTXNO_UNI, 0, 46);
    opj_mqc_setstate(mqc, T1_CTXNO_AGG, 0, 3);
    opj_mqc_setstate(mqc, T1_CTXNO_ZC, 0, 4);
}

/* ---- tiny JSON helpers ----------------------------------------------- */
static int first_item;
static void arr_u32(const char *name, const OPJ_UINT32 *v, int n)
{
    printf("\"%s\":[", name);
    for (int i = 0; i < n; i++) printf("%s%u", i ? "," : "", v[i]);
    printf("]");
}
static void hexbytes(const char *name, const OPJ_BYTE *v, int n)
{
    printf("\"%s\":\"", name);
    for (int i = 0; i < n; i++) printf("%02x", v[i]);
    printf("\"");
}
static void obj_sep(void) { if (!first_item) printf(","); first_item = 0; }

#define MAXSYM 4096
static OPJ_UINT32 g_ctx[MAXSYM];
static OPJ_UINT32 g_bit[MAXSYM];

/* Encode buffer with 1 scratch byte before start (mirrors tcd alloc). */
static OPJ_BYTE encbuf[MAXSYM * 4 + 64];

/*
 * Generate one MQ-encode case (name/term/profile), print its "enc" object,
 * and return via out params the produced bytes so the caller can also emit a
 * round-trip "dec" object.
 */
static void gen_profile(int profile, OPJ_UINT32 seed, int n)
{
    rng_seed(seed);
    for (int i = 0; i < n; i++) {
        OPJ_UINT32 r = rng_next();
        switch (profile) {
        case 0: /* uniform */
            g_ctx[i] = r % 19;
            g_bit[i] = (r >> 8) & 1;
            break;
        case 1: /* skew 90/10 on a few contexts */
            g_ctx[i] = r % 4;
            g_bit[i] = ((r >> 8) % 10 < 1) ? 1 : 0;
            break;
        case 2: /* context switching, random bits */
            g_ctx[i] = i % 19;
            g_bit[i] = (r >> 8) & 1;
            break;
        case 3: /* long MPS runs on a fixed context */
            g_ctx[i] = 8;
            g_bit[i] = ((r >> 8) % 50 == 0) ? 1 : 0;
            break;
        }
    }
}

/* term: 0=flush, 1=erterm, 2=segmark(+flush) */
static void emit_enc_and_dec(const char *name, int profile, OPJ_UINT32 seed,
                             int n, int term)
{
    opj_mqc_t mqc;
    memset(&mqc, 0, sizeof(mqc));
    gen_profile(profile, seed, n);

    /* --- encode --- */
    memset(encbuf, 0, sizeof(encbuf));
    opj_mqc_init_enc(&mqc, encbuf + 1);
    reset_std(&mqc);
    for (int i = 0; i < n; i++) {
        opj_mqc_setcurctx(&mqc, g_ctx[i]);
        OPJ_UINT32 d = g_bit[i];
        opj_mqc_encode_macro((&mqc), mqc.curctx, mqc.a, mqc.c, mqc.ct, d);
    }
    const char *termstr = "flush";
    if (term == 1) {
        opj_mqc_erterm_enc(&mqc);
        termstr = "erterm";
    } else if (term == 2) {
        opj_mqc_segmark_enc(&mqc);
        opj_mqc_flush(&mqc);
        termstr = "segmark";
    } else {
        opj_mqc_flush(&mqc);
    }
    OPJ_UINT32 nb = opj_mqc_numbytes(&mqc);

    obj_sep();
    printf("{\"name\":\"%s\",\"term\":\"%s\",", name, termstr);
    arr_u32("ctxs", g_ctx, n);
    printf(",");
    arr_u32("bits", g_bit, n);
    printf(",");
    hexbytes("out", encbuf + 1, nb);
    printf(",\"numbytes\":%u}", nb);

    /* --- round-trip decode of the produced bytes --- */
    /* Build decode context list (append segmark's 4 ctx=18 symbols). */
    static OPJ_UINT32 dctx[MAXSYM + 8];
    static OPJ_UINT32 dbit[MAXSYM + 8];
    int dn = n;
    for (int i = 0; i < n; i++) dctx[i] = g_ctx[i];
    if (term == 2) {
        for (int k = 0; k < 4; k++) dctx[dn++] = 18;
    }

    static OPJ_BYTE decbuf[MAXSYM * 4 + 64];
    memset(decbuf, 0, sizeof(decbuf));
    memcpy(decbuf, encbuf + 1, nb);
    opj_mqc_t dmqc;
    memset(&dmqc, 0, sizeof(dmqc));
    reset_std(&dmqc);
    opj_mqc_init_dec(&dmqc, decbuf, nb, OPJ_COMMON_CBLK_DATA_EXTRA);
    for (int i = 0; i < dn; i++) {
        opj_mqc_setcurctx(&dmqc, dctx[i]);
        OPJ_UINT32 v;
        opj_mqc_decode(v, (&dmqc));
        dbit[i] = v;
    }
    OPJ_UINT32 eobsc = dmqc.end_of_byte_stream_counter;
    opq_mqc_finish_dec(&dmqc);

    /* stash for the "dec" section: reuse global buffers via static store */
    /* (printed directly here into a companion structure is simpler: we
       emit into the dec array in a second pass, so save to files.) */
    /* Instead, emit dec object immediately into a separate global buffer. */
    /* To keep single-pass JSON valid we print dec entries in their own
       array; so accumulate here in memory. */
    extern void dec_stash(const char *name, const OPJ_BYTE *in, int len,
                          const OPJ_UINT32 *ctxs, const OPJ_UINT32 *bits,
                          int n, OPJ_UINT32 eobsc);
    dec_stash(name, decbuf, nb, dctx, dbit, dn, eobsc);
}

/* ---- deferred "dec" section accumulation ----------------------------- */
struct dec_entry {
    char name[64];
    OPJ_BYTE in[MAXSYM * 4 + 64];
    int len;
    OPJ_UINT32 ctxs[MAXSYM + 8];
    OPJ_UINT32 bits[MAXSYM + 8];
    int n;
    OPJ_UINT32 eobsc;
};
static struct dec_entry dec_entries[64];
static int dec_count;

void dec_stash(const char *name, const OPJ_BYTE *in, int len,
               const OPJ_UINT32 *ctxs, const OPJ_UINT32 *bits, int n,
               OPJ_UINT32 eobsc)
{
    struct dec_entry *e = &dec_entries[dec_count++];
    strncpy(e->name, name, sizeof(e->name) - 1);
    memcpy(e->in, in, (size_t)len);
    e->len = len;
    for (int i = 0; i < n; i++) { e->ctxs[i] = ctxs[i]; e->bits[i] = bits[i]; }
    e->n = n;
    e->eobsc = eobsc;
}

/* Adversarial MQ decode: run C decoder on a raw buffer with a cycling ctx
   sequence and stash the result. */
static void adversarial_dec(const char *name, const OPJ_BYTE *data, int len,
                            int nsym)
{
    static OPJ_BYTE db[MAXSYM * 4 + 64];
    static OPJ_UINT32 ctx[MAXSYM + 8], bit[MAXSYM + 8];
    memset(db, 0, sizeof(db));
    if (len) memcpy(db, data, (size_t)len);
    opj_mqc_t m;
    memset(&m, 0, sizeof(m));
    reset_std(&m);
    opj_mqc_init_dec(&m, db, len, OPJ_COMMON_CBLK_DATA_EXTRA);
    for (int i = 0; i < nsym; i++) {
        ctx[i] = i % 19;
        opj_mqc_setcurctx(&m, ctx[i]);
        OPJ_UINT32 v;
        opj_mqc_decode(v, (&m));
        bit[i] = v;
    }
    OPJ_UINT32 eobsc = m.end_of_byte_stream_counter;
    opq_mqc_finish_dec(&m);
    /* Note: input to stash is the ORIGINAL bytes (finish restored db). */
    static OPJ_BYTE orig[MAXSYM * 4 + 64];
    memset(orig, 0, sizeof(orig));
    if (len) memcpy(orig, data, (size_t)len);
    dec_stash(name, orig, len, ctx, bit, nsym, eobsc);
}

/* ---- bypass (RAW encode) cases --------------------------------------- */
static void emit_bypass(const char *name, OPJ_UINT32 seed, int nprefix,
                        int nbits, int erterm, int allones)
{
    static OPJ_UINT32 pctx[64], pbit[64], bpbit[MAXSYM];
    rng_seed(seed);
    for (int i = 0; i < nprefix; i++) {
        OPJ_UINT32 r = rng_next();
        pctx[i] = r % 19;
        pbit[i] = (r >> 8) & 1;
    }
    for (int i = 0; i < nbits; i++) {
        bpbit[i] = allones ? 1 : (rng_next() >> 7) & 1;
    }

    opj_mqc_t mqc;
    memset(&mqc, 0, sizeof(mqc));
    memset(encbuf, 0, sizeof(encbuf));
    opj_mqc_init_enc(&mqc, encbuf + 1);
    reset_std(&mqc);
    for (int i = 0; i < nprefix; i++) {
        opj_mqc_setcurctx(&mqc, pctx[i]);
        OPJ_UINT32 d = pbit[i];
        opj_mqc_encode_macro((&mqc), mqc.curctx, mqc.a, mqc.c, mqc.ct, d);
    }
    opj_mqc_flush(&mqc);
    opj_mqc_bypass_init_enc(&mqc);
    for (int i = 0; i < nbits; i++) {
        OPJ_UINT32 d = bpbit[i];
        opj_mqc_bypass_enc_macro((&mqc), mqc.c, mqc.ct, d);
    }
    opj_mqc_bypass_flush_enc(&mqc, erterm);
    OPJ_UINT32 nb = opj_mqc_numbytes(&mqc);

    obj_sep();
    printf("{\"name\":\"%s\",", name);
    arr_u32("ctxs", pctx, nprefix);
    printf(",");
    arr_u32("bits", pbit, nprefix);
    printf(",");
    arr_u32("bpbits", bpbit, nbits);
    printf(",\"erterm\":%d,", erterm ? 1 : 0);
    hexbytes("out", encbuf + 1, nb);
    printf(",\"numbytes\":%u}", nb);
}

/* ---- raw decode cases ------------------------------------------------ */
static void emit_raw(const char *name, const OPJ_BYTE *data, int len, int count)
{
    static OPJ_BYTE db[MAXSYM * 4 + 64];
    static OPJ_UINT32 bit[MAXSYM + 8];
    memset(db, 0, sizeof(db));
    if (len) memcpy(db, data, (size_t)len);
    opj_mqc_t m;
    memset(&m, 0, sizeof(m));
    opj_mqc_raw_init_dec(&m, db, len, OPJ_COMMON_CBLK_DATA_EXTRA);
    for (int i = 0; i < count; i++) bit[i] = opj_mqc_raw_decode(&m);
    opq_mqc_finish_dec(&m);

    obj_sep();
    printf("{\"name\":\"%s\",", name);
    hexbytes("in", data, len);
    printf(",\"len\":%d,\"count\":%d,", len, count);
    arr_u32("bits", bit, count);
    printf("}");
}

int main(void)
{
    printf("{\n");

    /* ---- encode section (also fills dec_entries via dec_stash) ---- */
    printf("\"enc\":[");
    first_item = 1;
    emit_enc_and_dec("uniform-flush", 0, 0xA11CE, 400, 0);
    emit_enc_and_dec("skew90-flush", 1, 0xBEEF01, 400, 0);
    emit_enc_and_dec("ctxswitch-flush", 2, 0xC0FFEE, 380, 0);
    emit_enc_and_dec("longmps-flush", 3, 0xD00D, 500, 0);
    emit_enc_and_dec("uniform-erterm", 0, 0x1357, 300, 1);
    emit_enc_and_dec("skew90-segmark", 1, 0x2468, 300, 2);
    emit_enc_and_dec("longmps-erterm", 3, 0x99AA, 400, 1);
    emit_enc_and_dec("uniform-segmark", 0, 0x5EED, 256, 2);
    printf("],\n");

    /* ---- bypass section ---- */
    printf("\"bypass\":[");
    first_item = 1;
    emit_bypass("bypass-rand-noerterm", 0x30303, 6, 200, 0, 0);
    emit_bypass("bypass-rand-erterm", 0x40404, 6, 200, 1, 0);
    emit_bypass("bypass-allones-noerterm", 0x50505, 4, 160, 0, 1);
    emit_bypass("bypass-allones-erterm", 0x60606, 4, 160, 1, 1);
    emit_bypass("bypass-short-noerterm", 0x70707, 8, 3, 0, 0);
    emit_bypass("bypass-empty-noerterm", 0x80808, 10, 0, 0, 0);
    printf("],\n");

    /* ---- raw decode section ---- */
    printf("\"raw\":[");
    first_item = 1;
    {
        OPJ_BYTE r[64];
        rng_seed(0xAB01);
        for (int i = 0; i < 40; i++) r[i] = (OPJ_BYTE)(rng_next() >> 3);
        emit_raw("raw-random", r, 40, 40 * 8);
    }
    {
        OPJ_BYTE r[16];
        memset(r, 0xff, sizeof(r));
        emit_raw("raw-all-ff", r, 16, 200);
    }
    {
        OPJ_BYTE r[8] = {0xff, 0x90, 0xff, 0x2a, 0xff, 0xff, 0x00, 0x00};
        emit_raw("raw-ff-markers", r, 8, 90);
    }
    {
        OPJ_BYTE r[4] = {0x12, 0x34, 0x56, 0x78};
        emit_raw("raw-truncated", r, 4, 200);
    }
    {
        emit_raw("raw-empty", NULL, 0, 64);
    }
    {
        OPJ_BYTE r[8] = {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00};
        emit_raw("raw-all-zero", r, 8, 100);
    }
    printf("],\n");

    /* ---- adversarial MQ decode (append to dec section) ---- */
    {
        OPJ_BYTE r[16];
        memset(r, 0xff, sizeof(r));
        adversarial_dec("mq-all-ff", r, 16, 80);
    }
    {
        OPJ_BYTE r[4] = {0xff, 0x90, 0x00, 0x00};
        adversarial_dec("mq-ff-90", r, 4, 64);
    }
    {
        OPJ_BYTE r[1] = {0xff};
        adversarial_dec("mq-single-ff", r, 1, 40);
    }
    {
        OPJ_BYTE r[3] = {0x80, 0x80, 0x80};
        adversarial_dec("mq-truncated", r, 3, 80);
    }
    adversarial_dec("mq-empty", NULL, 0, 48);

    /* ---- dec section (round-trip + adversarial) ---- */
    printf("\"dec\":[");
    for (int i = 0; i < dec_count; i++) {
        struct dec_entry *e = &dec_entries[i];
        if (i) printf(",");
        printf("{\"name\":\"%s\",", e->name);
        hexbytes("in", e->in, e->len);
        printf(",\"len\":%d,", e->len);
        arr_u32("ctxs", e->ctxs, e->n);
        printf(",");
        arr_u32("bits", e->bits, e->n);
        printf(",\"eobsc\":%u}", e->eobsc);
    }
    printf("]\n");

    printf("}\n");
    return 0;
}
