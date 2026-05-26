package web

import (
	"testing"
	"time"
)

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{59 * time.Second, "59s"},
		{1 * time.Minute, "1m 0s"},
		{5*time.Minute + 30*time.Second, "5m 30s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{1 * time.Hour, "1h 0m"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
		{23*time.Hour + 59*time.Minute, "23h 59m"},
		{24 * time.Hour, "1d 0h"},
		{3*24*time.Hour + 4*time.Hour, "3d 4h"},
		{30 * 24 * time.Hour, "30d 0h"},
	}
	for _, c := range cases {
		got := formatUptime(c.d)
		if got != c.want {
			t.Errorf("formatUptime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
