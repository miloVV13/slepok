// Package contextio makes local streaming operations cancellable between I/O calls.
package contextio

import (
	"context"
	"io"
)

func OrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type Reader struct {
	Context context.Context
	Reader  io.Reader
}

func (r Reader) Read(p []byte) (int, error) {
	if err := OrBackground(r.Context).Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

type Writer struct {
	Context context.Context
	Writer  io.Writer
}

func (w Writer) Write(p []byte) (int, error) {
	if err := OrBackground(w.Context).Err(); err != nil {
		return 0, err
	}
	return w.Writer.Write(p)
}
