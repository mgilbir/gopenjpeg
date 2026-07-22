// Package oracletest provides helpers for differential testing against the
// OpenJPEG reference implementation ("the oracle").
//
// The oracle is expected under <repo>/oracle: the built C binaries in
// oracle/openjpeg/build/bin and the openjpeg-data conformance corpus in
// oracle/data. Tests that need the oracle must call Require(t) and are
// skipped when it is absent (e.g. in CI without the clone), so plain
// `go test ./...` always works.
package oracletest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Root returns the oracle directory, honoring GOPENJPEG_ORACLE, defaulting
// to <repo>/oracle.
func Root() string {
	if v := os.Getenv("GOPENJPEG_ORACLE"); v != "" {
		return v
	}
	_, self, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(self), "..", "oracle")
}

// Bin returns the path to an oracle binary (opj_decompress, opj_compress,
// opj_dump).
func Bin(name string) string {
	return filepath.Join(Root(), "openjpeg", "build", "bin", name)
}

// DataDir returns a path inside the openjpeg-data conformance corpus.
func DataDir(parts ...string) string {
	return filepath.Join(append([]string{Root(), "data"}, parts...)...)
}

// Require skips the test if the oracle binaries or corpus are not present.
func Require(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(Bin("opj_decompress")); err != nil {
		t.Skipf("oracle not available: %v", err)
	}
	if _, err := os.Stat(DataDir()); err != nil {
		t.Skipf("oracle corpus not available: %v", err)
	}
}

// RunOracle executes an oracle binary with args and returns combined output.
func RunOracle(t *testing.T, bin string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(Bin(bin), args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", bin, args, err, out)
	}
	return out
}
