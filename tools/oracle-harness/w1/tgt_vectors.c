/* Oracle harness (W1): dumps tgt.c tag-tree encode/decode vectors.
 *
 * Compile:
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w1/tgt_vectors.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/tgt_vectors
 *   /tmp/tgt_vectors > testdata/vectors/tgt/tgt.txt
 *
 * Each case is four lines:
 *   TREE <H> <V> <threshold>
 *   VALS <v0> <v1> ...            (leaf values in leafno order)
 *   ENC  <hexbytes>               (encode every leaf at threshold, then flush)
 *   DEC  <r0>:<val0> <r1>:<val1>  (fresh-tree decode of every leaf: return:node-value)
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

#define MAXLEAFS 64
static void emit_case(uint32_t H, uint32_t V, int32_t threshold, uint32_t seed)
{
    uint32_t nleafs = H * V;
    int32_t vals[MAXLEAFS];
    OPJ_BYTE buf[MAXLEAFS * 4 + 16];
    opj_tgt_tree_t *tree;
    opj_bio_t *bio;
    uint32_t i;
    ptrdiff_t nbytes;

    st = seed;
    for (i = 0; i < nleafs; i++) {
        vals[i] = (int32_t)(nextrand() % 20u);
    }

    /* Encode */
    tree = opj_tgt_create(H, V, NULL);
    for (i = 0; i < nleafs; i++) {
        opj_tgt_setvalue(tree, i, vals[i]);
    }
    bio = opj_bio_create();
    opj_bio_init_enc(bio, buf, nleafs * 4u + 16u);
    for (i = 0; i < nleafs; i++) {
        opj_tgt_encode(bio, tree, i, threshold);
    }
    opj_bio_flush(bio);
    nbytes = opj_bio_numbytes(bio);
    opj_bio_destroy(bio);
    opj_tgt_destroy(tree);

    printf("TREE %u %u %d\n", H, V, threshold);
    printf("VALS");
    for (i = 0; i < nleafs; i++) {
        printf(" %d", vals[i]);
    }
    printf("\n");
    printf("ENC ");
    for (i = 0; i < (uint32_t)nbytes; i++) {
        printf("%02x", buf[i]);
    }
    printf("\n");

    /* Decode with a fresh tree */
    tree = opj_tgt_create(H, V, NULL);
    bio = opj_bio_create();
    opj_bio_init_dec(bio, buf, (OPJ_UINT32)nbytes);
    printf("DEC");
    for (i = 0; i < nleafs; i++) {
        OPJ_UINT32 r = opj_tgt_decode(bio, tree, i, threshold);
        printf(" %u:%d", r, tree->nodes[i].value);
    }
    printf("\n");
    opj_bio_destroy(bio);
    opj_tgt_destroy(tree);
}

int main(void)
{
    struct {
        uint32_t H, V;
    } dims[] = {
        {1, 1}, {1, 3}, {3, 1}, {2, 2}, {3, 3}, {4, 2},
        {5, 5}, {8, 1}, {1, 8}, {4, 4}, {6, 3}, {7, 7}
    };
    int nd = (int)(sizeof(dims) / sizeof(dims[0]));
    int32_t thresholds[] = { 1, 3, 5, 10, 20, 25 };
    int nt = (int)(sizeof(thresholds) / sizeof(thresholds[0]));
    int d, t;
    uint32_t seed = 0xC0FFEEu;

    for (d = 0; d < nd; d++) {
        for (t = 0; t < nt; t++) {
            emit_case(dims[d].H, dims[d].V, thresholds[t], seed);
            seed = seed * 2654435761u + 12345u;
        }
    }

    return 0;
}
