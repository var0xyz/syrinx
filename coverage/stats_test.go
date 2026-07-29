package coverage

import "testing"

func TestPercent(t *testing.T) {
	tests := []struct {
		holders, active int
		want            int
	}{
		{0, 0, 0},
		{12, 100, 12},
		{1, 3, 33},
		{100, 100, 100},
		{150, 100, 100},
	}
	for _, tc := range tests {
		if got := Percent(tc.holders, tc.active); got != tc.want {
			t.Fatalf("Percent(%d, %d) = %d, want %d", tc.holders, tc.active, got, tc.want)
		}
	}
}
