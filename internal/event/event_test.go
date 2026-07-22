package event

import "testing"

func TestNilManagerNoOp(t *testing.T) {
	var m *Manager
	if m.Errorf("boom") {
		t.Error("nil manager Errorf returned true")
	}
	if m.Warnf("warn") {
		t.Error("nil manager Warnf returned true")
	}
	if m.Infof("info") {
		t.Error("nil manager Infof returned true")
	}
}

func TestNilHandlersNoOp(t *testing.T) {
	m := &Manager{}
	if m.Errorf("x") || m.Warnf("x") || m.Infof("x") {
		t.Error("manager with nil handlers should return false")
	}
}

func TestHandlersInvoked(t *testing.T) {
	var got [3]string
	m := &Manager{
		ErrorHandler:   func(s string) { got[0] = s },
		WarningHandler: func(s string) { got[1] = s },
		InfoHandler:    func(s string) { got[2] = s },
	}
	if !m.Errorf("err %d", 1) {
		t.Error("Errorf returned false")
	}
	if !m.Warnf("warn %s", "z") {
		t.Error("Warnf returned false")
	}
	if !m.Infof("plain message") {
		t.Error("Infof returned false")
	}
	if got[0] != "err 1" {
		t.Errorf("error msg = %q", got[0])
	}
	if got[1] != "warn z" {
		t.Errorf("warn msg = %q", got[1])
	}
	if got[2] != "plain message" {
		t.Errorf("info msg = %q", got[2])
	}
}

// TestLiteralPercentPassthrough checks that a bare format string with stray
// percent signs (no args) is not run through Sprintf.
func TestLiteralPercentPassthrough(t *testing.T) {
	var got string
	m := &Manager{ErrorHandler: func(s string) { got = s }}
	m.Errorf("100% done")
	if got != "100% done" {
		t.Errorf("got %q", got)
	}
}
