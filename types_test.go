package mygram

import (
	"testing"
)

// Option types and their wire tokens (types.go).

func TestFilterOpWireTokens(t *testing.T) {
	cases := []struct {
		op   FilterOp
		want string
	}{
		{FilterEqual, "="},
		{FilterNotEqual, "!="},
		{FilterGreaterThan, ">"},
		{FilterGreaterThanOrEqual, ">="},
		{FilterLessThan, "<"},
		{FilterLessThanOrEqual, "<="},
	}
	for _, tc := range cases {
		if got := tc.op.wire(); got != tc.want {
			t.Errorf("FilterOp(%d).wire() = %q, want %q", tc.op, got, tc.want)
		}
	}
}
