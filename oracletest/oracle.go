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
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gopenjpeg/internal/event"
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
//
// Absence is the ONLY reason a gate may skip. Once Require has passed, a gate
// that actually runs an oracle binary and gets a failure must fail the test, not
// skip it (C23): the case lists are curated pass lists, so an oracle that can no
// longer produce a reference for a listed case is either a broken oracle build
// or a stale list — both are defects, and skipping hid them.
func Require(t *testing.T) {
	t.Helper()
	// Both binaries are required: the decode/CLI gates drive opj_decompress and
	// the encode/CLI-compress gates drive opj_compress. Stat-ing only the former
	// let a half-built oracle turn the encode gates into runtime failures.
	for _, bin := range []string{"opj_decompress", "opj_compress"} {
		if _, err := os.Stat(Bin(bin)); err != nil {
			t.Skipf("oracle not available: %v", err)
		}
	}
	if _, err := os.Stat(DataDir()); err != nil {
		t.Skipf("oracle corpus not available: %v", err)
	}
}

// EventCollector is an event sink for the gates. Every gate that drives the
// pure-Go codec installs one (C61: the gates used to pass a nil *event.Manager,
// which meant the entire error/warning/info emission path — format strings,
// argument counts, handler wiring, concurrent handler access — was never
// executed by the differential harness). Collected messages are available for
// assertions and are dumped on failure.
//
// It is safe for concurrent use: the tier-1 decode workers wrap the manager in
// a locking shim, but the encode side and the container layers do not, so the
// collector locks for itself.
type EventCollector struct {
	mu       sync.Mutex
	errors   []string
	warnings []string
	infos    []string
}

// Manager returns an *event.Manager whose three handlers feed the collector.
func (c *EventCollector) Manager() *event.Manager {
	return &event.Manager{
		ErrorHandler:   func(msg string) { c.add(&c.errors, msg) },
		WarningHandler: func(msg string) { c.add(&c.warnings, msg) },
		InfoHandler:    func(msg string) { c.add(&c.infos, msg) },
	}
}

func (c *EventCollector) add(dst *[]string, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*dst = append(*dst, msg)
}

// Errors, Warnings and Infos return copies of the collected messages.
func (c *EventCollector) Errors() []string   { return c.snapshot(&c.errors) }
func (c *EventCollector) Warnings() []string { return c.snapshot(&c.warnings) }
func (c *EventCollector) Infos() []string    { return c.snapshot(&c.infos) }

func (c *EventCollector) snapshot(src *[]string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), (*src)...)
}

// All returns every collected message, errors first, then warnings, then infos.
func (c *EventCollector) All() []string {
	out := c.Errors()
	out = append(out, c.Warnings()...)
	return append(out, c.Infos()...)
}

// Contains reports whether any collected message contains sub.
func (c *EventCollector) Contains(sub string) bool {
	for _, m := range c.All() {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

// Check asserts the shape of what was emitted: a message must be non-empty and
// must not carry a NUL or an unexpanded fmt verb error ("%!"), which is how a
// mis-ported format string or a wrong argument count shows up. It is cheap
// enough to call from every gate.
func (c *EventCollector) Check(t *testing.T, context string) {
	t.Helper()
	for _, m := range c.All() {
		if m == "" {
			t.Errorf("%s: emitted an empty diagnostic message", context)
			continue
		}
		if strings.ContainsRune(m, 0) {
			t.Errorf("%s: diagnostic contains a NUL byte: %q", context, m)
		}
		if strings.Contains(m, "%!") {
			t.Errorf("%s: diagnostic has a bad format verb / argument count: %q", context, m)
		}
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

// execOracle runs an oracle binary and returns combined output plus error.
func execOracle(name string, args ...string) ([]byte, error) {
	return exec.Command(Bin(name), args...).CombinedOutput()
}
