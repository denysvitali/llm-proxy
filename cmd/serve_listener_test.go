package cmd

import "testing"

func TestLoopbackListener(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    bool
	}{
		{"127.0.0.1:8090", true}, {"127.2.3.4:8090", true}, {"[::1]:8090", true},
		{"localhost:8090", true}, {":8090", false}, {"0.0.0.0:8090", false},
		{"[::]:8090", false}, {"192.0.2.1:8090", false}, {"proxy.example:8090", false}, {"invalid", false},
	} {
		t.Run(tc.address, func(t *testing.T) {
			if got := loopbackListener(tc.address); got != tc.want {
				t.Fatalf("loopbackListener(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
}
