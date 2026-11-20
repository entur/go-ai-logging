// Package golog writes JSON log events in the shape of
// github.com/entur/go-logging (zerolog), by hand, so that the fixtures need
// no dependencies: severity, fields, error, stack, timestamp, caller, message.
package golog

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"time"
)

// Event is one log event being built.
type Event struct {
	severity string
	buf      bytes.Buffer
}

// Warn starts a warning.
func Warn() *Event { return start("warning") }

// Error starts an error.
func Error() *Event { return start("error") }

// Fatal starts a fatal event; Msg exits the process with status 1.
func Fatal() *Event { return start("fatal") }

func start(severity string) *Event {
	e := &Event{severity: severity}
	e.buf.WriteString(`{"severity":`)
	e.value(severity)
	return e
}

// Str adds a string field.
func (e *Event) Str(key, value string) *Event { return e.field(key, value) }

// Int adds an integer field.
func (e *Event) Int(key string, value int) *Event { return e.field(key, value) }

// Err adds "error" and a "stack" of [][]{file,function,line}, with string
// line numbers as go-logging writes them.
func (e *Event) Err(err error) *Event {
	type frame struct {
		File     string `json:"file"`
		Function string `json:"function"`
		Line     string `json:"line"`
	}
	pcs := make([]uintptr, 3)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	var group []frame
	for {
		f, more := frames.Next()
		group = append(group, frame{File: f.File, Function: f.Function, Line: strconv.Itoa(f.Line)})
		if !more {
			break
		}
	}
	return e.field("error", err.Error()).field("stack", [][]frame{group})
}

// Msg writes the event to stdout.
func (e *Event) Msg(msg string) {
	_, file, line, _ := runtime.Caller(1)
	e.field("timestamp", time.Now().Format(time.RFC3339Nano))
	e.field("caller", file+":"+strconv.Itoa(line))
	e.field("message", msg)
	e.buf.WriteString("}\n")
	os.Stdout.Write(e.buf.Bytes())
	if e.severity == "fatal" {
		os.Exit(1)
	}
}

func (e *Event) field(key string, value any) *Event {
	e.buf.WriteByte(',')
	e.value(key)
	e.buf.WriteByte(':')
	e.value(value)
	return e
}

func (e *Event) value(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	e.buf.Write(b)
}
