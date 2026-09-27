// Package powerslog provides a [slog.Handler] that writes structured log
// records enriched with key fields captured from an AWS Lambda context,
// to produce logs equivalent to those produced by the AWS Lambda Powertools
// packages for Python, TypeScript, and .NET.
package powerslog

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"unicode/utf8"

	"github.com/unfunco/powerslog/internal/buffer"
)

const (
	// LevelKey is the key used by the handler for the level of the log call.
	// The associated value is a [slog.Level].
	LevelKey = "level"
	// MessageKey is the key used by handler for the message of the log call.
	// The associated value is a string.
	MessageKey = "message"
	// TimestampKey is the key used by the handlers for the timestamp when the
	// log method is called. The associated Value is a [time.Time].
	TimestampKey = "timestamp"

	// timeFormatISO8601Extended is the format used by the handler for the
	// timestamp when the log method is called. The format is a simplified
	// extended ISO 8601 timestamp and is the same format used by the AWS
	// Lambda Powertools packages.
	timeFormatISO8601Extended = "2006-01-02T15:04:05.999Z07:00"
)

// HandlerOptions are options for a [Handler].
// A zero HandlerOptions consists entirely of default values.
type HandlerOptions struct {
	// Level reports the minimum record level that will be logged.
	// The handler discards records with lower levels.
	// If Level is nil, the handler assumes slog.LevelInfo.
	// The handler calls Level.Level for each record processed;
	// to adjust the minimum level dynamically, use a slog.LevelVar.
	Level slog.Leveler
}

// Handler is a [slog.Handler] that writes Records enriched with key fields from
// an AWS Lambda context to an [io.Writer].
type Handler struct {
	mu   *sync.Mutex
	opts HandlerOptions
	w    io.Writer
}

// NewHandler creates a [Handler] that writes to w, using the given options.
// It adds the service name and function name to the attributes of the
// log record during handler construction since these values are not going to
// change during the lifetime of the handler.
func NewHandler(w io.Writer, opts *HandlerOptions) *Handler {
	if opts == nil {
		opts = &HandlerOptions{}
	}
	return &Handler{mu: &sync.Mutex{}, opts: *opts, w: w}
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower. When the
// Advanced Logging Controls feature is enabled.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle implements the slog.Handler interface and handles the Record.
// It will only be called when Enabled returns true.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	state := h.newHandleState(buffer.New(), true)
	defer state.free()

	_ = state.buf.WriteByte('{')

	state.appendKey(LevelKey)
	state.appendString(record.Level.String())

	if !record.Time.IsZero() {
		state.appendKey(TimestampKey)
		state.appendTime(record.Time.Round(0))
	}

	state.appendKey(MessageKey)
	state.appendString(record.Message)

	_ = state.buf.WriteByte('}')
	_ = state.buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(*state.buf)
	return err
}

// WithAttrs returns a new Handler whose attributes consist of both the
// receiver's attributes and the arguments.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h2 := h.clone()
	state := h2.newHandleState(buffer.New(), false)
	defer state.free()
	//
	return h2
}

// WithGroup returns a new Handler with the given group appended to
// the receiver's existing groups.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{}
}

// Copied from encoding/json/encode.go:encodeState.string,
// with escapeHTML set to false.
func appendJSONEscapedString(buf []byte, s string) []byte {
	char := func(b byte) { buf = append(buf, b) }
	str := func(s string) { buf = append(buf, s...) }

	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if safeSet[b] {
				i++
				continue
			}
			if start < i {
				str(s[start:i])
			}
			char('\\')
			switch b {
			case '\\', '"':
				char(b)
			case '\n':
				char('n')
			case '\r':
				char('r')
			case '\t':
				char('t')
			default:
				// This encodes bytes < 0x20 except for \t, \n and \r.
				str(`u00`)
				char(hex[b>>4])
				char(hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			if start < i {
				str(s[start:i])
			}
			str(`\ufffd`)
			i += size
			start = i
			continue
		}
		// U+2028 is LINE SEPARATOR.
		// U+2029 is PARAGRAPH SEPARATOR.
		// They are both technically valid characters in JSON strings,
		// but don't work in JSONP, which has to be evaluated as JavaScript,
		// and can lead to security holes there. It is valid JSON to
		// escape them, so we do so unconditionally.
		// See http://timelessrepo.com/json-isnt-a-javascript-subset for discussion.
		if c == '\u2028' || c == '\u2029' {
			if start < i {
				str(s[start:i])
			}
			str(`\u202`)
			char(hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		str(s[start:])
	}
	return buf
}

const hex = "0123456789abcdef"

var safeSet = [utf8.RuneSelf]bool{
	' ':      true,
	'!':      true,
	'"':      false,
	'#':      true,
	'$':      true,
	'%':      true,
	'&':      true,
	'\'':     true,
	'(':      true,
	')':      true,
	'*':      true,
	'+':      true,
	',':      true,
	'-':      true,
	'.':      true,
	'/':      true,
	'0':      true,
	'1':      true,
	'2':      true,
	'3':      true,
	'4':      true,
	'5':      true,
	'6':      true,
	'7':      true,
	'8':      true,
	'9':      true,
	':':      true,
	';':      true,
	'<':      true,
	'=':      true,
	'>':      true,
	'?':      true,
	'@':      true,
	'A':      true,
	'B':      true,
	'C':      true,
	'D':      true,
	'E':      true,
	'F':      true,
	'G':      true,
	'H':      true,
	'I':      true,
	'J':      true,
	'K':      true,
	'L':      true,
	'M':      true,
	'N':      true,
	'O':      true,
	'P':      true,
	'Q':      true,
	'R':      true,
	'S':      true,
	'T':      true,
	'U':      true,
	'V':      true,
	'W':      true,
	'X':      true,
	'Y':      true,
	'Z':      true,
	'[':      true,
	'\\':     false,
	']':      true,
	'^':      true,
	'_':      true,
	'`':      true,
	'a':      true,
	'b':      true,
	'c':      true,
	'd':      true,
	'e':      true,
	'f':      true,
	'g':      true,
	'h':      true,
	'i':      true,
	'j':      true,
	'k':      true,
	'l':      true,
	'm':      true,
	'n':      true,
	'o':      true,
	'p':      true,
	'q':      true,
	'r':      true,
	's':      true,
	't':      true,
	'u':      true,
	'v':      true,
	'w':      true,
	'x':      true,
	'y':      true,
	'z':      true,
	'{':      true,
	'|':      true,
	'}':      true,
	'~':      true,
	'\u007f': true,
}

func (h *Handler) newHandleState(buf *buffer.Buffer, freeBuf bool) handleState {
	return handleState{
		buf:     buf,
		freeBuf: freeBuf,
	}
}

func (h *Handler) clone() *Handler {
	return &Handler{mu: h.mu, opts: h.opts, w: h.w}
}
