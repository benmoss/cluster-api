package logger

import (
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
)

type Logger struct {
	logr.Logger
}

func (l fooLogger) Error(err error, msg string, kvList ...interface{}) {
	if s, ok := err.(stackTracer); ok {
		l.Logger.WithValues("stack", s.StackTrace()).Error(err, msg, kvList...)
	} else {
		l.Logger.Error(err, msg, kvList...)
	}
}

func (l fooLogger) WithName(name string) logr.Logger {
	new := l.Logger.WithName(name)
	return fooLogger{new}
}

func (l fooLogger) WithValues(kvList ...interface{}) logr.Logger {
	new := l.Logger.WithValues(kvList...)
	return fooLogger{new}
}

type stackTracer interface {
	StackTrace() errors.StackTrace
}
