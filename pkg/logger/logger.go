package logger

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Nigel2392/errors"
)

type Log interface {
	Println(context.Context, Loglevel, ...any) error
	Printf(context.Context, Loglevel, string, ...any) error
}

type Null struct{}

func (l Null) Println(_ context.Context, _ Loglevel, _ ...any) error          { return nil }
func (l Null) Printf(_ context.Context, _ Loglevel, _ string, _ ...any) error { return nil }

type WriterLog struct {
	Level         Loglevel
	UseTimestamps bool      // if true, prefix with time
	PkgName       string    // if not empty, will be prefixed with "[<<PkgName>>]:"
	Out           io.Writer // write to this output
}

func (l WriterLog) Println(ctx context.Context, level Loglevel, args ...any) error {
	if SkipLog(ctx, l.Level, level) {
		return nil
	}

	levelStr, err := loglevelString(ctx, level)
	if err != nil {
		return errors.Wrap(err, "WriterLog.Println")
	}

	out := strings.Builder{}
	if l.UseTimestamps {
		out.WriteString(time.Now().Format(time.DateTime))
		out.WriteRune(' ')
	}

	out.WriteRune('[')

	if l.PkgName != "" {
		out.Grow(3 + len(l.PkgName) + len(levelStr))
		out.WriteString(l.PkgName)
		out.WriteString(" / ")
		out.WriteString(levelStr)
	} else {
		out.WriteString(levelStr)
	}

	out.WriteString("]: ")

	_, err = fmt.Fprintln(l.Out, append([]any{out.String()}, args...)...)
	return err
}

func (l WriterLog) Printf(ctx context.Context, level Loglevel, format string, args ...any) error {
	if SkipLog(ctx, l.Level, level) {
		return nil
	}

	levelStr, err := loglevelString(ctx, level)
	if err != nil {
		return errors.Wrap(err, "WriterLog.Println")
	}

	out := strings.Builder{}
	if l.UseTimestamps {
		out.WriteString(time.Now().Format(time.DateTime))
		out.WriteRune(' ')
	}

	out.WriteRune('[')

	if l.PkgName != "" {
		out.Grow(3 + len(l.PkgName) + len(levelStr))
		out.WriteString(l.PkgName)
		out.WriteString(" / ")
		out.WriteString(levelStr)
	} else {
		out.WriteString(levelStr)
	}

	out.WriteString("]: ")

	out.WriteString(format)

	out.WriteRune('\n')

	_, err = fmt.Fprintf(l.Out, out.String(), args...)
	return err
}
