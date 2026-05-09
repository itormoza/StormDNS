package client

import (
	"strings"
	"testing"
	"time"

	"stormdns-go/internal/config"
	Enums "stormdns-go/internal/enums"
)

func TestMakeConnectionKeyDiffersByQType(t *testing.T) {
	txtKey := makeConnectionKey("8.8.8.8", 53, "v.example.com", Enums.DNS_RECORD_TYPE_TXT)
	aaaaKey := makeConnectionKey("8.8.8.8", 53, "v.example.com", Enums.DNS_RECORD_TYPE_AAAA)

	if txtKey == aaaaKey {
		t.Fatalf("expected qtype-specific keys to differ: %q", txtKey)
	}
	if !strings.HasSuffix(txtKey, "|16") {
		t.Fatalf("expected TXT key to include qtype 16, got %q", txtKey)
	}
}

func TestBuildConnectionMapFixedModeUsesOneCarrierPerDomainResolver(t *testing.T) {
	c := New(config.ClientConfig{
		Domains: []string{"a.example.com", "b.example.com"},
		Resolvers: []config.ResolverAddress{
			{IP: "8.8.8.8", Port: 53},
			{IP: "1.1.1.1", Port: 53},
		},
		TunnelRecordType: Enums.DNS_RECORD_TYPE_TXT,
	}, nil, nil)

	if err := c.BuildConnectionMap(); err != nil {
		t.Fatalf("BuildConnectionMap returned error: %v", err)
	}
	if len(c.connections) != 4 {
		t.Fatalf("unexpected connection count: got=%d want=4", len(c.connections))
	}
	for _, conn := range c.connections {
		if conn.TunnelRecordType != Enums.DNS_RECORD_TYPE_TXT || conn.TunnelRecordName != "TXT" {
			t.Fatalf("unexpected connection carrier: type=%d name=%q", conn.TunnelRecordType, conn.TunnelRecordName)
		}
	}
}

func TestBuildConnectionMapAutoUsesImplementedCarrierMatrix(t *testing.T) {
	c := New(config.ClientConfig{
		Domains: []string{"a.example.com"},
		Resolvers: []config.ResolverAddress{
			{IP: "8.8.8.8", Port: 53},
			{IP: "1.1.1.1", Port: 53},
		},
		TunnelDNSRecordAuto:   true,
		TunnelAutoRecordTypes: []uint16{Enums.DNS_RECORD_TYPE_TXT},
	}, nil, nil)

	if err := c.BuildConnectionMap(); err != nil {
		t.Fatalf("BuildConnectionMap returned error: %v", err)
	}
	if len(c.connections) != 2 {
		t.Fatalf("unexpected AUTO connection count: got=%d want=2", len(c.connections))
	}
}

func TestSelectBestAutoCarrierConnectionsKeepsOnePerDomainResolver(t *testing.T) {
	c := New(config.ClientConfig{
		TunnelDNSRecordAuto: true,
		TunnelAutoRecordTypes: []uint16{
			Enums.DNS_RECORD_TYPE_TXT,
			Enums.DNS_RECORD_TYPE_AAAA,
		},
	}, nil, nil)
	c.connections = []Connection{
		{
			Domain:           "v.example.com",
			Resolver:         "8.8.8.8",
			ResolverPort:     53,
			TunnelRecordType: Enums.DNS_RECORD_TYPE_TXT,
			IsValid:          true,
			UploadMTUBytes:   100,
			DownloadMTUBytes: 200,
			MTUResolveTime:   20 * time.Millisecond,
		},
		{
			Domain:           "v.example.com",
			Resolver:         "8.8.8.8",
			ResolverPort:     53,
			TunnelRecordType: Enums.DNS_RECORD_TYPE_AAAA,
			IsValid:          true,
			UploadMTUBytes:   100,
			DownloadMTUBytes: 300,
			MTUResolveTime:   30 * time.Millisecond,
		},
	}

	c.selectBestAutoCarrierConnections()

	if c.connections[0].IsValid {
		t.Fatal("expected lower-download TXT candidate to be disabled")
	}
	if !c.connections[1].IsValid {
		t.Fatal("expected higher-download AAAA candidate to remain valid")
	}
}

func TestParseResolverCacheLineDefaultsMissingTypeToTXT(t *testing.T) {
	entry, ok := parseResolverCacheLine("2026-04-20T15:04:05Z 8.8.8.8:53 v.example.com UP=64 DOWN=120")
	if !ok {
		t.Fatal("expected old cache log line to parse")
	}
	if entry.TunnelRecordType != Enums.DNS_RECORD_TYPE_TXT || entry.TunnelRecordName != "TXT" {
		t.Fatalf("unexpected default carrier: type=%d name=%q", entry.TunnelRecordType, entry.TunnelRecordName)
	}
}

func TestParseResolverCacheLinePreservesExplicitType(t *testing.T) {
	entry, ok := parseResolverCacheLine("2026-04-20T15:04:05Z 8.8.8.8:53 v.example.com TYPE=TXT UP=64 DOWN=120")
	if !ok {
		t.Fatal("expected typed cache log line to parse")
	}
	if entry.TunnelRecordType != Enums.DNS_RECORD_TYPE_TXT || entry.TunnelRecordName != "TXT" {
		t.Fatalf("unexpected explicit carrier: type=%d name=%q", entry.TunnelRecordType, entry.TunnelRecordName)
	}
}
