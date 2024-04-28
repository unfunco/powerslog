package powerslog

import (
	"time"

	"github.com/unfunco/powerslog/internal/buffer"
)

type handleState struct {
	buf     *buffer.Buffer
	freeBuf bool
	sep     byte
}

func (s *handleState) free() {
	if s.freeBuf {
		s.buf.Free()
	}
}

func (s *handleState) appendTime(t time.Time) {
	_ = s.buf.WriteByte('"')
	// The Powertools documentation refers to format of the timestamp being a
	// simplified extended ISO 8601 timestamp.
	*s.buf = t.AppendFormat(*s.buf, timeFormatISO8601Extended)
	_ = s.buf.WriteByte('"')
}

func (s *handleState) appendKey(key string) {
	_ = s.buf.WriteByte(s.sep)
	s.appendString(key)
	_ = s.buf.WriteByte(':')
	s.sep = ','
}

func (s *handleState) appendString(str string) {
	_ = s.buf.WriteByte('"')
	*s.buf = appendJSONEscapedString(*s.buf, str)
	_ = s.buf.WriteByte('"')
}
