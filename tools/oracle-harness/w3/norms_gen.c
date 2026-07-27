/* W3 oracle harness: DWT norm and explicit-stepsize vectors.
 *
 * Build (from repo root):
 *   gcc -O2 -I oracle/openjpeg/src/lib/openjp2 \
 *       -I oracle/openjpeg/build/src/lib/openjp2 \
 *       tools/oracle-harness/w3/norms_gen.c \
 *       oracle/openjpeg/build/bin/libopenjp2.a -lm -lpthread -o /tmp/norms_gen
 *   /tmp/norms_gen testdata/vectors/dwt/norms.bin
 */
#define OPJ_SKIP_POISON
#include "opj_includes.h"
#include "dwt.h"

#include <stdint.h>
#include <stdio.h>
#include <string.h>

static FILE* g_out;
static void wu32(uint32_t v) { fwrite(&v, 4, 1, g_out); }
static void wi32(int32_t v) { fwrite(&v, 4, 1, g_out); }
static void wf64(double v) { fwrite(&v, 8, 1, g_out); }

int main(int argc, char** argv)
{
    if (argc < 2) { fprintf(stderr, "usage: %s out.bin\n", argv[0]); return 1; }
    g_out = fopen(argv[1], "wb");
    if (!g_out) { perror("fopen"); return 1; }

    /* Section 1: getnorm / getnorm_real for level 0..11, orient 0..3 */
    wu32(12); /* levels */
    wu32(4);  /* orients */
    for (uint32_t level = 0; level < 12; level++) {
        for (uint32_t orient = 0; orient < 4; orient++) {
            wf64(opj_dwt_getnorm(level, orient));
            wf64(opj_dwt_getnorm_real(level, orient));
        }
    }

    /* Section 2: calc_explicit_stepsizes for a matrix of params */
    uint32_t numres_list[] = { 1, 2, 3, 5, 6 };
    uint32_t qmfbid_list[] = { 0, 1 };
    uint32_t qntsty_list[] = { 0, 1, 2 }; /* NOQNT, SIQNT, others */
    uint32_t prec_list[] = { 8, 12, 16 };

    uint32_t ncfg = 0;
    long cfgpos = ftell(g_out);
    wu32(0); /* placeholder */

    for (uint32_t a = 0; a < 5; a++)
        for (uint32_t b = 0; b < 2; b++)
            for (uint32_t c = 0; c < 3; c++)
                for (uint32_t d = 0; d < 3; d++) {
                    uint32_t numres = numres_list[a];
                    opj_tccp_t tccp;
                    memset(&tccp, 0, sizeof(tccp));
                    tccp.numresolutions = numres;
                    tccp.qmfbid = qmfbid_list[b];
                    tccp.qntsty = qntsty_list[c];
                    uint32_t prec = prec_list[d];
                    opj_dwt_calc_explicit_stepsizes(&tccp, prec);
                    uint32_t numbands = 3 * numres - 2;
                    wu32(numres);
                    wu32(qmfbid_list[b]);
                    wu32(qntsty_list[c]);
                    wu32(prec);
                    wu32(numbands);
                    for (uint32_t bn = 0; bn < numbands; bn++) {
                        wi32(tccp.stepsizes[bn].expn);
                        wi32(tccp.stepsizes[bn].mant);
                    }
                    ncfg++;
                }

    long here = ftell(g_out);
    fseek(g_out, cfgpos, SEEK_SET);
    wu32(ncfg);
    fseek(g_out, here, SEEK_SET);
    fclose(g_out);
    fprintf(stderr, "wrote %s: %u stepsize cfgs\n", argv[1], ncfg);
    return 0;
}
