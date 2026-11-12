// Package msg builds the messages users see that carry values (a quota, a
// name, a limit) so the dashboard can translate them: a fixed English
// template with {name} placeholders, and the values that fill it. The API
// sends both, with the filled English text, and the dashboard translates
// the template (web/locales/*/server.json) before filling it.
package msg

import (
	"errors"
	"fmt"
	"strings"
)

// Error is a message with values; it is also an error.
type Error struct {
	Template string
	Params   map[string]string
}

// New makes a message from a template and name/value pairs:
// msg.New("website quota of {limit} reached", "limit", 5).
func New(template string, kv ...any) *Error {
	e := &Error{Template: template, Params: map[string]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Params[fmt.Sprint(kv[i])] = fmt.Sprint(kv[i+1])
	}
	return e
}

// Error is the English text, with the values filled in.
func (e *Error) Error() string {
	if len(e.Params) == 0 {
		return e.Template
	}
	pairs := make([]string, 0, 2*len(e.Params))
	for k, v := range e.Params {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(e.Template)
}

// From returns the message err is, when it is one (not wrapped in more text).
func From(err error) (*Error, bool) {
	var m *Error
	if errors.As(err, &m) && err.Error() == m.Error() {
		return m, true
	}
	return nil, false
}
