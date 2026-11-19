package queries

import "testing"

func TestServiceBusSettledHorizon(t *testing.T) {
	cases := []struct {
		name      string
		after     int64
		tail      []serviceBusTailRow
		truncated bool
		want      int64
	}{
		{"no young rows", 10, nil, false, serviceBusUnbounded},
		{"contiguous young tail", 10, []serviceBusTailRow{{11, 10}, {12, 11}}, false, serviceBusUnbounded},
		// 11 committed before 10: deliver nothing past the cursor until 10 settles.
		{"gap right above cursor", 9, []serviceBusTailRow{{11, 9}}, false, 9},
		{"gap inside tail", 10, []serviceBusTailRow{{11, 10}, {13, 11}, {14, 13}}, false, 11},
		// prev below the cursor (rows deleted or on the cursor itself) counts from the cursor.
		{"prev below cursor", 20, []serviceBusTailRow{{21, 5}}, false, serviceBusUnbounded},
		{"gap above old predecessor", 0, []serviceBusTailRow{{7, 5}}, false, 5},
		{"truncated scan caps at last scanned", 10, []serviceBusTailRow{{11, 10}, {12, 11}}, true, 12},
	}
	for _, tc := range cases {
		if got := serviceBusSettledHorizon(tc.after, tc.tail, tc.truncated); got != tc.want {
			t.Errorf("%s: horizon = %d, want %d", tc.name, got, tc.want)
		}
	}
}
