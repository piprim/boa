package internal

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseTime_Infinity(t *testing.T) {
	t.Run("infinity is the positive sentinel", func(t *testing.T) {
		got, err := ParseTime("infinity")
		if err != nil {
			t.Fatalf("ParseTime() error = %v", err)
		}
		if !got.Equal(PosInfinity) {
			t.Errorf("ParseTime() = %v, want %v", got, PosInfinity)
		}
	})

	t.Run("-infinity is the negative sentinel", func(t *testing.T) {
		got, err := ParseTime("-infinity")
		if err != nil {
			t.Fatalf("ParseTime() error = %v", err)
		}
		if !got.Equal(NegInfinity) {
			t.Errorf("ParseTime() = %v, want %v", got, NegInfinity)
		}
	})

	t.Run("a finite timestamp is still parsed", func(t *testing.T) {
		got, err := ParseTime("2026-04-02 10:30:00")
		if err != nil {
			t.Fatalf("ParseTime() error = %v", err)
		}
		if want := time.Date(2026, time.April, 2, 10, 30, 0, 0, time.UTC); !got.Equal(want) {
			t.Errorf("ParseTime() = %v, want %v", got, want)
		}
	})
}

func TestInfinitySentinels(t *testing.T) {
	t.Run("the positive sentinel is after any date PostgreSQL stores", func(t *testing.T) {
		if last := time.Date(9999, time.December, 31, 23, 59, 59, 999999000, time.UTC); !PosInfinity.After(last) {
			t.Errorf("PosInfinity = %v, want it after %v", PosInfinity, last)
		}
	})

	t.Run("the negative sentinel is not the zero time", func(t *testing.T) {
		if NegInfinity.IsZero() || !NegInfinity.Before(time.Time{}) {
			t.Errorf("NegInfinity = %v, want it before the zero time", NegInfinity)
		}
	})

	t.Run("both sentinels survive encoding/json", func(t *testing.T) {
		for _, tm := range []time.Time{PosInfinity, NegInfinity} {
			if _, err := json.Marshal(tm); err != nil {
				t.Errorf("json.Marshal(%v) error = %v", tm, err)
			}
		}
	})
}

func TestInfinityText(t *testing.T) {
	t.Run("the positive sentinel is written infinity, in any location", func(t *testing.T) {
		got, ok := InfinityText(PosInfinity.In(time.FixedZone("CET", 3600)))
		if !ok || got != "infinity" {
			t.Errorf("InfinityText() = %q, %v, want infinity, true", got, ok)
		}
	})

	t.Run("the negative sentinel is written -infinity", func(t *testing.T) {
		got, ok := InfinityText(NegInfinity)
		if !ok || got != "-infinity" {
			t.Errorf("InfinityText() = %q, %v, want -infinity, true", got, ok)
		}
	})

	t.Run("a finite time is not infinite", func(t *testing.T) {
		if got, ok := InfinityText(time.Date(2026, time.April, 2, 0, 0, 0, 0, time.UTC)); ok {
			t.Errorf("InfinityText() = %q, true, want false", got)
		}
	})
}
