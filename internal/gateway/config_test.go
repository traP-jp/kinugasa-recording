package gateway

import "testing"

func TestRISTURLUsesLibRISTListenerSyntax(t *testing.T) {
	config := Config{RISTAddress: "0.0.0.0:9000"}
	if got, want := config.ristURL(), "rist://@0.0.0.0:9000"; got != want {
		t.Fatalf("ristURL() = %q, want %q", got, want)
	}
}
