package gateway

import "testing"

func TestRISTURLUsesLibRISTListenerSyntax(t *testing.T) {
	config := Config{RISTAddress: "0.0.0.0:9000"}
	if got, want := config.ristURL(), "rist://@0.0.0.0:9000"; got != want {
		t.Fatalf("ristURL() = %q, want %q", got, want)
	}
}

func TestConfigDefaultsToRISTReceiverUDPOutput(t *testing.T) {
	config := (Config{}).withDefaults()
	if got, want := config.RISTReceiverPath, "ristreceiver"; got != want {
		t.Fatalf("RISTReceiverPath = %q, want %q", got, want)
	}
	if got, want := config.RISTOutputURL, "udp://127.0.0.1:10000"; got != want {
		t.Fatalf("RISTOutputURL = %q, want %q", got, want)
	}
}
