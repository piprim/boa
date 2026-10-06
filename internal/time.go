package internal

import (
	"fmt"
	"time"
)

const (
	dateFormat         = "2006-01-02"
	timeFormat         = "15:04:05.999999999"
	timetzFormat1      = "15:04:05.999999999-07:00:00"
	timetzFormat2      = "15:04:05.999999999-07:00"
	timetzFormat3      = "15:04:05.999999999-07"
	timestampFormat    = "2006-01-02 15:04:05.999999999"
	timestamptzFormat1 = "2006-01-02 15:04:05.999999999-07:00:00"
	timestamptzFormat2 = "2006-01-02 15:04:05.999999999-07:00"
	timestamptzFormat3 = "2006-01-02 15:04:05.999999999-07"
)

// PosInfinity and NegInfinity stand for PostgreSQL's 'infinity' and
// '-infinity', which a time.Time cannot hold. PostgreSQL keeps microseconds,
// so their nanoseconds cannot come from a stored value, and both years stay in
// the range encoding/json accepts.
var (
	PosInfinity = time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC)
	NegInfinity = time.Date(0, time.January, 1, 0, 0, 0, 1, time.UTC)
)

const (
	posInfinityText = "infinity"
	negInfinityText = "-infinity"
)

// InfinityText returns the PostgreSQL spelling of tm when it is one of the
// infinity sentinels.
func InfinityText(tm time.Time) (string, bool) {
	switch {
	case tm.Equal(PosInfinity):
		return posInfinityText, true
	case tm.Equal(NegInfinity):
		return negInfinityText, true
	}
	return "", false
}

func ParseTime(s string) (time.Time, error) {
	switch s {
	case posInfinityText:
		return PosInfinity, nil
	case negInfinityText:
		return NegInfinity, nil
	}

	l := len(s)

	if l >= len("2006-01-02 15:04:05") {
		switch s[10] {
		case ' ':
			if c := s[l-6]; c == '+' || c == '-' {
				return time.Parse(timestamptzFormat2, s)
			}
			if c := s[l-3]; c == '+' || c == '-' {
				return time.Parse(timestamptzFormat3, s)
			}
			if c := s[l-9]; c == '+' || c == '-' {
				return time.Parse(timestamptzFormat1, s)
			}
			return time.ParseInLocation(timestampFormat, s, time.UTC)
		case 'T':
			return time.Parse(time.RFC3339Nano, s)
		}
	}

	if l >= len("15:04:05-07") {
		if c := s[l-6]; c == '+' || c == '-' {
			return time.Parse(timetzFormat2, s)
		}
		if c := s[l-3]; c == '+' || c == '-' {
			return time.Parse(timetzFormat3, s)
		}
		if c := s[l-9]; c == '+' || c == '-' {
			return time.Parse(timetzFormat1, s)
		}
	}

	if l < len("15:04:05") {
		return time.Time{}, fmt.Errorf("boa: can't parse time=%q", s)
	}

	if s[2] == ':' {
		return time.ParseInLocation(timeFormat, s, time.UTC)
	}
	return time.ParseInLocation(dateFormat, s, time.UTC)
}
