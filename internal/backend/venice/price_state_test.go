package venice

import "testing"

func TestModelPriceFreeRequiresPublishedState(t *testing.T) {
	for _, tc := range []struct {
		price modelPrice
		free  bool
	}{
		{modelPrice{}, false},
		{modelPrice{state: priceUnknown}, false},
		{modelPrice{state: pricePublished}, true},
		{modelPrice{InputUSD: 0.1, state: pricePublished}, false},
		{modelPrice{OutputUSD: 0.1, state: pricePublished}, false},
	} {
		if got := tc.price.free(); got != tc.free {
			t.Errorf("%+v (state %v).free() = %v, want %v", tc.price, tc.price.state, got, tc.free)
		}
	}
}
