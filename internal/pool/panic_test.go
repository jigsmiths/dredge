package pool

import (
	"context"
	"strings"
	"testing"
)

func TestAPanicFailsOnlyItsOwnItem(t *testing.T) {
	in := []int{1, 2, 3, 4}
	got := Map(context.Background(), 2, in, func(_ context.Context, n int) (int, error) {
		if n == 2 {
			panic("boom")
		}
		return n * 10, nil
	})
	for i, r := range got {
		if in[i] == 2 {
			if r.Err == nil || !strings.Contains(r.Err.Error(), "panic") {
				t.Errorf("item 2 = %+v, want an error naming the panic", r)
			}
			continue
		}
		if r.Err != nil || r.Value != in[i]*10 {
			t.Errorf("item %d = %+v, want value %d", in[i], r, in[i]*10)
		}
	}
}
