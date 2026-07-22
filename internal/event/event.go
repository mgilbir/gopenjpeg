// Package event provides a minimal logging/message callback system, porting
// the parts of event.c/event.h used across the OpenJPEG codebase.
//
// The C code passes an opj_event_mgr_t* to almost every function and calls
// opj_event_msg(mgr, EVT_ERROR|EVT_WARNING|EVT_INFO, fmt, ...). This package
// replaces that with a small Manager type carrying three callbacks. All
// methods are nil-safe: a nil *Manager, or a Manager with nil handlers, is a
// silent no-op, mirroring the C behaviour where a NULL manager or NULL
// handler causes opj_event_msg to return OPJ_FALSE without doing anything.
package event

import "fmt"

// MsgCallback is a message sink. It ports opj_msg_callback: a function that
// receives a formatted, NUL-free message string. The C callback also receives
// a void* client_data; callers that need context should use a closure.
type MsgCallback func(msg string)

// Manager ports opj_event_mgr_t. A nil *Manager is valid and behaves as a
// no-op logger.
type Manager struct {
	// ErrorHandler ports opj_event_mgr_t.error_handler (EVT_ERROR).
	ErrorHandler MsgCallback
	// WarningHandler ports opj_event_mgr_t.warning_handler (EVT_WARNING).
	WarningHandler MsgCallback
	// InfoHandler ports opj_event_mgr_t.info_handler (EVT_INFO).
	InfoHandler MsgCallback
}

// Errorf ports opj_event_msg with event_type == EVT_ERROR. It returns true if
// a handler was present and invoked (matching opj_event_msg's OPJ_TRUE), false
// otherwise.
func (m *Manager) Errorf(format string, args ...any) bool {
	if m == nil || m.ErrorHandler == nil {
		return false
	}
	m.ErrorHandler(format2(format, args))
	return true
}

// Warnf ports opj_event_msg with event_type == EVT_WARNING.
func (m *Manager) Warnf(format string, args ...any) bool {
	if m == nil || m.WarningHandler == nil {
		return false
	}
	m.WarningHandler(format2(format, args))
	return true
}

// Infof ports opj_event_msg with event_type == EVT_INFO.
func (m *Manager) Infof(format string, args ...any) bool {
	if m == nil || m.InfoHandler == nil {
		return false
	}
	m.InfoHandler(format2(format, args))
	return true
}

// format2 applies fmt.Sprintf only when there are arguments, so that a bare
// format string containing stray percent signs is passed through verbatim
// (the common OpenJPEG call site passes a plain literal message).
func format2(format string, args []any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
