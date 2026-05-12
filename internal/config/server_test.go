// ==============================================================================
// StormDNS
// Author: nullroute1970
// Github: https://github.com/nullroute1970/StormDNS
// Year: 2026
// ==============================================================================

package config

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	Enums "stormdns-go/internal/enums"
)

func TestLoadServerConfigWithOverridesAppliesFlagPrecedence(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "server_config.toml")

	if err := os.WriteFile(configPath, []byte(`
PROTOCOL_TYPE = "SOCKS5"
UDP_PORT = 53
DOMAIN = ["config.example.com"]
DATA_ENCRYPTION_METHOD = 1
SUPPORTED_UPLOAD_COMPRESSION_TYPES = [0, 3]
SUPPORTED_DOWNLOAD_COMPRESSION_TYPES = [0, 3]
`), 0o644); err != nil {
		t.Fatalf("WriteFile config failed: %v", err)
	}

	cfg, err := LoadServerConfigWithOverrides(configPath, ServerConfigOverrides{
		Values: map[string]any{
			"UDPPort":                           5300,
			"Domain":                            []string{"flag.example.com", "alt.example.com"},
			"DataEncryptionMethod":              2,
			"SupportedUploadCompressionTypes":   []int{0, 1},
			"SupportedDownloadCompressionTypes": []int{0, 1, 3},
		},
	})
	if err != nil {
		t.Fatalf("LoadServerConfigWithOverrides returned error: %v", err)
	}

	if cfg.UDPPort != 5300 {
		t.Fatalf("unexpected udp port override: got=%d want=%d", cfg.UDPPort, 5300)
	}
	if len(cfg.Domain) != 2 || cfg.Domain[0] != "flag.example.com" || cfg.Domain[1] != "alt.example.com" {
		t.Fatalf("unexpected domain override: %+v", cfg.Domain)
	}
	if cfg.DataEncryptionMethod != 2 {
		t.Fatalf("unexpected data encryption override: got=%d want=%d", cfg.DataEncryptionMethod, 2)
	}
	if len(cfg.SupportedUploadCompressionTypes) != 2 || cfg.SupportedUploadCompressionTypes[0] != 0 || cfg.SupportedUploadCompressionTypes[1] != 1 {
		t.Fatalf("unexpected upload compression override: %+v", cfg.SupportedUploadCompressionTypes)
	}
	if len(cfg.SupportedDownloadCompressionTypes) != 3 {
		t.Fatalf("unexpected download compression override: %+v", cfg.SupportedDownloadCompressionTypes)
	}
}

func TestLoadServerConfigParsesTunnelRecordTypes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "server_config.toml")

	if err := os.WriteFile(configPath, []byte(`
PROTOCOL_TYPE = "SOCKS5"
UDP_PORT = 53
DOMAIN = ["config.example.com"]
DATA_ENCRYPTION_METHOD = 1
SUPPORTED_UPLOAD_COMPRESSION_TYPES = [0, 3]
SUPPORTED_DOWNLOAD_COMPRESSION_TYPES = [0, 3]
TUNNEL_DNS_RECORD_TYPES = ["TXT"]
`), 0o644); err != nil {
		t.Fatalf("WriteFile config failed: %v", err)
	}

	cfg, err := LoadServerConfig(configPath)
	if err != nil {
		t.Fatalf("LoadServerConfig returned error: %v", err)
	}
	if len(cfg.TunnelRecordTypes) != 1 || cfg.TunnelRecordTypes[0] != Enums.DNS_RECORD_TYPE_TXT {
		t.Fatalf("unexpected tunnel record types: %+v", cfg.TunnelRecordTypes)
	}
	if _, ok := cfg.TunnelRecordTypeSet[Enums.DNS_RECORD_TYPE_TXT]; !ok {
		t.Fatal("expected TXT in tunnel record type set")
	}
}

func TestLoadServerConfigFallsBackToThroughputSafeDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "server_config.toml")

	if err := os.WriteFile(configPath, []byte(`
PROTOCOL_TYPE = "SOCKS5"
UDP_PORT = 53
DOMAIN = ["config.example.com"]
DATA_ENCRYPTION_METHOD = 1
SUPPORTED_UPLOAD_COMPRESSION_TYPES = [0, 3]
SUPPORTED_DOWNLOAD_COMPRESSION_TYPES = [0, 3]
UDP_READERS = 0
DNS_REQUEST_WORKERS = 0
SESSION_TIMEOUT_SECONDS = 0
CLOSED_SESSION_RETENTION_SECONDS = 0
SESSION_INIT_REUSE_TTL_SECONDS = 0
RECENTLY_CLOSED_STREAM_TTL_SECONDS = 0
RECENTLY_CLOSED_STREAM_CAP = 0
TERMINAL_STREAM_RETENTION_SECONDS = 0
DNS_UPSTREAM_TIMEOUT = 0
SOCKS_CONNECT_TIMEOUT = 0
`), 0o644); err != nil {
		t.Fatalf("WriteFile config failed: %v", err)
	}

	cfg, err := LoadServerConfig(configPath)
	if err != nil {
		t.Fatalf("LoadServerConfig returned error: %v", err)
	}

	expectedReaders := min(max(runtime.NumCPU(), 4), 8)
	if cfg.UDPReaders != expectedReaders {
		t.Fatalf("unexpected UDP_READERS fallback: got=%d want=%d", cfg.UDPReaders, expectedReaders)
	}

	expectedWorkers := min(max(runtime.NumCPU()*2, 8), 32)
	if cfg.DNSRequestWorkers != expectedWorkers {
		t.Fatalf("unexpected DNS_REQUEST_WORKERS fallback: got=%d want=%d", cfg.DNSRequestWorkers, expectedWorkers)
	}
	if cfg.UDPReaders < 4 {
		t.Fatalf("UDP_READERS fallback regressed below old fixed default: got=%d", cfg.UDPReaders)
	}
	if cfg.DNSRequestWorkers < 8 {
		t.Fatalf("DNS_REQUEST_WORKERS fallback regressed below old fixed default: got=%d", cfg.DNSRequestWorkers)
	}

	if cfg.SessionTimeoutSecs != 300.0 {
		t.Fatalf("unexpected SESSION_TIMEOUT_SECONDS fallback: got=%v want=300", cfg.SessionTimeoutSecs)
	}
	if cfg.ClosedSessionRetentionSecs != 600.0 {
		t.Fatalf("unexpected CLOSED_SESSION_RETENTION_SECONDS fallback: got=%v want=600", cfg.ClosedSessionRetentionSecs)
	}
	if cfg.SessionInitReuseTTLSeconds != 600.0 {
		t.Fatalf("unexpected SESSION_INIT_REUSE_TTL_SECONDS fallback: got=%v want=600", cfg.SessionInitReuseTTLSeconds)
	}
	if cfg.RecentlyClosedStreamTTLSeconds != 600.0 {
		t.Fatalf("unexpected RECENTLY_CLOSED_STREAM_TTL_SECONDS fallback: got=%v want=600", cfg.RecentlyClosedStreamTTLSeconds)
	}
	if cfg.RecentlyClosedStreamCap != 2000 {
		t.Fatalf("unexpected RECENTLY_CLOSED_STREAM_CAP fallback: got=%d want=2000", cfg.RecentlyClosedStreamCap)
	}
	if cfg.TerminalStreamRetentionSeconds != 45.0 {
		t.Fatalf("unexpected TERMINAL_STREAM_RETENTION_SECONDS fallback: got=%v want=45", cfg.TerminalStreamRetentionSeconds)
	}
	if cfg.DNSUpstreamTimeoutSecs != 4.0 {
		t.Fatalf("unexpected DNS_UPSTREAM_TIMEOUT fallback: got=%v want=4", cfg.DNSUpstreamTimeoutSecs)
	}
	if cfg.SOCKSConnectTimeoutSecs != 8.0 {
		t.Fatalf("unexpected SOCKS_CONNECT_TIMEOUT fallback: got=%v want=8", cfg.SOCKSConnectTimeoutSecs)
	}
	if cfg.ARQDataNackMaxGap != 128 {
		t.Fatalf("unexpected ARQ_DATA_NACK_MAX_GAP default: got=%d want=128", cfg.ARQDataNackMaxGap)
	}
	if cfg.ARQDataNackInitialDelaySeconds != 0.10 {
		t.Fatalf("unexpected ARQ_DATA_NACK_INITIAL_DELAY_SECONDS default: got=%v want=0.1", cfg.ARQDataNackInitialDelaySeconds)
	}
	if cfg.ARQDataNackRepeatSeconds != 0.4 {
		t.Fatalf("unexpected ARQ_DATA_NACK_REPEAT_SECONDS default: got=%v want=0.4", cfg.ARQDataNackRepeatSeconds)
	}
}

func TestLoadServerConfigAutoAcceptsImplementedTunnelCarriers(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "server_config.toml")

	if err := os.WriteFile(configPath, []byte(`
PROTOCOL_TYPE = "SOCKS5"
UDP_PORT = 53
DOMAIN = ["config.example.com"]
DATA_ENCRYPTION_METHOD = 1
SUPPORTED_UPLOAD_COMPRESSION_TYPES = [0, 3]
SUPPORTED_DOWNLOAD_COMPRESSION_TYPES = [0, 3]
TUNNEL_DNS_RECORD_TYPES = ["AUTO"]
`), 0o644); err != nil {
		t.Fatalf("WriteFile config failed: %v", err)
	}

	cfg, err := LoadServerConfig(configPath)
	if err != nil {
		t.Fatalf("LoadServerConfig returned error: %v", err)
	}
	if len(cfg.TunnelRecordTypes) != 1 || cfg.TunnelRecordTypes[0] != Enums.DNS_RECORD_TYPE_TXT {
		t.Fatalf("unexpected AUTO tunnel record types: %+v", cfg.TunnelRecordTypes)
	}
}

func TestServerConfigFlagBinderBuildsOverridesForSetFlagsOnly(t *testing.T) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	binder, err := NewServerConfigFlagBinder(fs)
	if err != nil {
		t.Fatalf("NewServerConfigFlagBinder returned error: %v", err)
	}

	if err := fs.Parse([]string{
		"-udp-port=5300",
		"-domain=a.example.com,b.example.com",
		"-use-external-socks5",
		"-supported-upload-compression-types=0,1",
		"-data-encryption-method=2",
	}); err != nil {
		t.Fatalf("flag parse failed: %v", err)
	}

	overrides := binder.Overrides()
	if got, ok := overrides.Values["UDPPort"].(int); !ok || got != 5300 {
		t.Fatalf("unexpected udp port override: %#v", overrides.Values["UDPPort"])
	}
	if got, ok := overrides.Values["UseExternalSOCKS5"].(bool); !ok || !got {
		t.Fatalf("unexpected socks5 override: %#v", overrides.Values["UseExternalSOCKS5"])
	}
	if got, ok := overrides.Values["DataEncryptionMethod"].(int); !ok || got != 2 {
		t.Fatalf("unexpected encryption method override: %#v", overrides.Values["DataEncryptionMethod"])
	}
	gotDomains, ok := overrides.Values["Domain"].([]string)
	if !ok || len(gotDomains) != 2 || gotDomains[0] != "a.example.com" || gotDomains[1] != "b.example.com" {
		t.Fatalf("unexpected domains override: %#v", overrides.Values["Domain"])
	}
	gotCompressions, ok := overrides.Values["SupportedUploadCompressionTypes"].([]int)
	if !ok || len(gotCompressions) != 2 || gotCompressions[0] != 0 || gotCompressions[1] != 1 {
		t.Fatalf("unexpected compression override: %#v", overrides.Values["SupportedUploadCompressionTypes"])
	}
	if _, exists := overrides.Values["UDPHost"]; exists {
		t.Fatalf("did not expect unset flag to appear in overrides: %#v", overrides.Values["UDPHost"])
	}
}
