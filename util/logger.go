package util

import (
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"k8s.io/klog/klogr"
)

func NewLogger() logr.Logger {
	return logger{klogr.New()}
}

type logger struct {
	logr.Logger
}

type stackTracer interface {
	StackTrace() errors.StackTrace
}

func (l logger) Error(err error, msg string, kvList ...interface{}) {
	if s, ok := err.(stackTracer); ok {
		l.Logger.WithValues("stack", s.StackTrace()).Error(err, msg, kvList...)
	} else {
		l.Logger.Error(err, msg, kvList...)
	}
}

func (l logger) WithName(name string) logr.Logger {
	new := l.Logger.WithName(name)
	return logger{new}
}

func (l logger) WithValues(kvList ...interface{}) logr.Logger {
	new := l.Logger.WithValues(kvList...)
	return logger{new}
}
