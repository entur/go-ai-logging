// Package compact renders logical output lines as compact.log: structured
// JSON log events (go-logging/zerolog and log/slog shapes) become one
// key=value line, other text is quoted, and adjacent repeats are collapsed.
package compact

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/entur/go-ai-logging/internal/escape"
)

const (
	stackFrames = 2
	stackGroups = 2
)

// Event is a logical line recognised as a structured log event.
type Event struct {
	fields         map[string]json.RawMessage
	sevKey, msgKey string
	label          string
	key            [sha256.Size]byte
}

// Parse recognises a JSON object with a "severity" or "level" string (the
// first present key must hold a JSON string, not null) and a "message" or
// "msg" of any type; it returns nil for anything else.
func Parse(text []byte) *Event {
	trimmed := bytes.TrimSpace(text)
	if !bytes.HasPrefix(trimmed, []byte("{")) {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(trimmed, &fields) != nil {
		return nil
	}
	ev := &Event{fields: fields, sevKey: firstKey(fields, "severity", "level"), msgKey: firstKey(fields, "message", "msg")}
	var sev string
	if ev.sevKey == "" || ev.msgKey == "" || !isString(fields[ev.sevKey]) || json.Unmarshal(fields[ev.sevKey], &sev) != nil {
		return nil
	}
	ev.label = severityLabel(sev)

	var id []byte
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if !isTimeKey(k) {
			id = strconv.AppendQuote(id, k)
			id = append(id, ':')
			id = append(id, fields[k]...)
			id = append(id, ',')
		}
	}
	ev.key = sha256.Sum256(id)
	return ev
}

// Key is the event's identity: a hash of every field except the timestamp.
func (e *Event) Key() [sha256.Size]byte {
	return e.key
}

// Label is the three-letter severity, or the unknown value uppercased.
func (e *Event) Label() string {
	return e.label
}

// Severe reports a warning or worse.
func (e *Event) Severe() bool {
	switch e.label {
	case "WRN", "ERR", "FTL", "PNC":
		return true
	default:
		return false
	}
}

// severityLabel maps go-logging/zerolog and slog levels, in any case.
func severityLabel(sev string) string {
	switch strings.ToLower(sev) {
	case "trace":
		return "TRC"
	case "debug":
		return "DBG"
	case "info":
		return "INF"
	case "warning", "warn":
		return "WRN"
	case "error":
		return "ERR"
	case "fatal":
		return "FTL"
	case "panic":
		return "PNC"
	default:
		return escape.Text(strings.ToUpper(sev))
	}
}

func isTimeKey(k string) bool {
	return k == "timestamp" || k == "time"
}

// Render returns the compact form without the leading "#N ".
func (e *Event) Render(pkg, hint, cwd string) string {
	var b strings.Builder
	b.WriteString(e.label + " " + pkg + " ?" + hint)
	at, atKey := e.location(cwd)
	if atKey != "" {
		b.WriteString(" at=" + at)
	}
	b.WriteString(" msg=" + jsonValue(e.fields[e.msgKey]))
	if raw, ok := e.fields["error"]; ok {
		b.WriteString(" error=" + jsonValue(raw))
	}
	if raw, ok := e.fields["stack"]; ok {
		if stack, ok := renderStack(raw, cwd); ok {
			b.WriteString(" stack=" + strconv.Quote(stack))
		} else {
			b.WriteString(" stack=" + jsonValue(raw))
		}
	}

	for _, k := range slices.Sorted(maps.Keys(e.fields)) {
		if !e.shown(k, atKey) {
			b.WriteString(" " + bare(k) + "=" + jsonValue(e.fields[k]))
		}
	}
	return b.String()
}

// shown reports fields rendered before the key-sorted rest, or dropped.
func (e *Event) shown(k, atKey string) bool {
	return k == e.sevKey || k == e.msgKey || atKey != "" && k == atKey || isTimeKey(k) || k == "error" || k == "stack"
}

// location renders go-logging's "caller" or slog's "source" object as the
// at= value, and returns the field it used ("" for none).
func (e *Event) location(cwd string) (at, key string) {
	if raw, ok := e.fields["caller"]; ok {
		var caller string
		if json.Unmarshal(raw, &caller) == nil {
			return bare(trimCwd(caller, cwd)), "caller"
		}
		return jsonValue(raw), "caller"
	}
	var src struct {
		File *string         `json:"file"`
		Line json.RawMessage `json:"line"`
	}
	if raw, ok := e.fields["source"]; ok && json.Unmarshal(raw, &src) == nil && src.File != nil && len(src.Line) > 0 {
		return bare(trimCwd(*src.File, cwd) + ":" + strings.Trim(string(src.Line), `"`)), "source"
	}
	return "", ""
}

type frame struct {
	File     string          `json:"file"`
	Function string          `json:"function"`
	Line     json.RawMessage `json:"line"`
}

// renderStack renders go-logging's [][]{file,function,line} stack shape.
func renderStack(raw json.RawMessage, cwd string) (string, bool) {
	var groups [][]frame
	if json.Unmarshal(raw, &groups) != nil || len(groups) == 0 {
		return "", false
	}
	parts := make([]string, 0, stackGroups)
	for _, group := range groups[:min(stackGroups, len(groups))] {
		frames := make([]string, 0, stackFrames)
		for _, f := range group[:min(stackFrames, len(group))] {
			if f.File == "" && f.Function == "" {
				return "", false
			}
			line := strings.Trim(string(f.Line), `"`)
			frames = append(frames, f.Function+" "+trimCwd(f.File, cwd)+":"+line)
		}
		part := strings.Join(frames, " > ")
		if len(group) > stackFrames {
			part += fmt.Sprintf(" [+%d frames]", len(group)-stackFrames)
		}
		parts = append(parts, part)
	}
	out := strings.Join(parts, " | ")
	if len(groups) > stackGroups {
		out += fmt.Sprintf(" [+%d groups]", len(groups)-stackGroups)
	}
	return out, true
}

func firstKey(fields map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if _, ok := fields[k]; ok {
			return k
		}
	}
	return ""
}

// isString reports a JSON string token; null and other types are not.
func isString(raw json.RawMessage) bool {
	return bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`))
}

// jsonValue renders strings quoted and everything else as minified JSON, keeping numbers exact.
func jsonValue(raw json.RawMessage) string {
	var s string
	if isString(raw) && json.Unmarshal(raw, &s) == nil {
		return strconv.Quote(s)
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return strconv.Quote(string(raw))
	}
	return escape.Text(buf.String())
}

// trimCwd strips the working directory from a path, at a separator boundary only.
func trimCwd(path, cwd string) string {
	if cwd != "" && len(path) > len(cwd) && strings.HasPrefix(path, cwd) {
		return path[len(cwd):]
	}
	return path
}

// bare returns s unquoted when that is unambiguous in a k=v line.
func bare(s string) string {
	if s == "" || !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool {
		return r == ' ' || r == '"' || r == '=' || !unicode.IsPrint(r)
	}) {
		return strconv.Quote(s)
	}
	return s
}
