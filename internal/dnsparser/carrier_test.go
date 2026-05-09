package dnsparser

import (
	"testing"

	Enums "stormdns-go/internal/enums"
)

func TestNormalizeTunnelCarrierNameMapsPrivateToConfiguredType(t *testing.T) {
	qType, err := NormalizeTunnelCarrierNameWithPrivate("private", 65281)
	if err != nil {
		t.Fatalf("NormalizeTunnelCarrierNameWithPrivate returned error: %v", err)
	}
	if qType != 65281 {
		t.Fatalf("unexpected private qtype: got=%d want=65281", qType)
	}
	if got := TunnelCarrierName(qType, 65281); got != "PRIVATE" {
		t.Fatalf("unexpected carrier name: got=%q want=PRIVATE", got)
	}
}

func TestSupportedTunnelCarrierSetIncludesOnlyImplementedCarriers(t *testing.T) {
	set := SupportedTunnelCarrierSet([]uint16{
		Enums.DNS_RECORD_TYPE_TXT,
		Enums.DNS_RECORD_TYPE_AAAA,
	})
	if _, ok := set[Enums.DNS_RECORD_TYPE_TXT]; !ok {
		t.Fatal("expected TXT to be supported")
	}
	if _, ok := set[Enums.DNS_RECORD_TYPE_AAAA]; ok {
		t.Fatal("did not expect unimplemented AAAA to be supported")
	}
}
