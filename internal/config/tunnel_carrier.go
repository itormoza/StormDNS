// ==============================================================================
// StormDNS
// Author: nullroute1970
// Github: https://github.com/nullroute1970/StormDNS
// Year: 2026
// ==============================================================================

package config

import (
	"fmt"
	"strings"

	Carrier "stormdns-go/internal/tunnelcarrier"
)

const (
	defaultTunnelPrivateRecordType = int(Carrier.DefaultTunnelPrivateRecordType)
	minPrivateUseRRType            = int(Carrier.MinPrivateUseRRType)
	maxPrivateUseRRType            = int(Carrier.MaxPrivateUseRRType)
)

var defaultTunnelDNSAutoRecordTypes = []string{
	"TXT",
	"CNAME",
	"AAAA",
	"A",
	"MX",
	"SRV",
	"NULL",
	"PRIVATE",
	"CAA",
}

func normalizeClientTunnelCarrierConfig(cfg *ClientConfig) error {
	privateType, err := normalizeTunnelPrivateRecordType(cfg.TunnelPrivateRecordType)
	if err != nil {
		return err
	}
	cfg.TunnelPrivateRecordType = int(privateType)

	mode := strings.ToUpper(strings.TrimSpace(cfg.TunnelDNSRecordType))
	if mode == "" {
		mode = "TXT"
	}

	if mode == "AUTO" {
		types, names, err := resolveImplementedTunnelCarrierList(cfg.TunnelDNSAutoRecordTypes, privateType, true)
		if err != nil {
			return err
		}
		if len(types) == 0 {
			return fmt.Errorf("TUNNEL_DNS_AUTO_RECORD_TYPES has no implemented tunnel carriers")
		}
		cfg.TunnelDNSRecordType = "AUTO"
		cfg.TunnelDNSRecordAuto = true
		cfg.TunnelRecordType = 0
		cfg.TunnelRecordName = "AUTO"
		cfg.TunnelAutoRecordTypes = types
		cfg.TunnelAutoRecordNames = names
		return nil
	}

	qType, err := Carrier.NormalizeTunnelCarrierNameWithPrivate(mode, privateType)
	if err != nil {
		return fmt.Errorf("invalid TUNNEL_DNS_RECORD_TYPE: %w", err)
	}
	if !Carrier.IsTunnelCarrierImplemented(qType) {
		return fmt.Errorf("TUNNEL_DNS_RECORD_TYPE %q is not implemented", mode)
	}

	name := Carrier.TunnelCarrierName(qType, privateType)
	cfg.TunnelDNSRecordType = name
	cfg.TunnelDNSRecordAuto = false
	cfg.TunnelRecordType = qType
	cfg.TunnelRecordName = name
	cfg.TunnelAutoRecordTypes = []uint16{qType}
	cfg.TunnelAutoRecordNames = []string{name}
	return nil
}

func normalizeServerTunnelCarrierConfig(cfg *ServerConfig) error {
	privateType, err := normalizeTunnelPrivateRecordType(cfg.TunnelPrivateRecordType)
	if err != nil {
		return err
	}
	cfg.TunnelPrivateRecordType = int(privateType)

	values := cfg.TunnelDNSRecordTypes
	if len(values) == 0 {
		values = []string{"TXT"}
	}

	types, names, err := resolveImplementedTunnelCarrierList(values, privateType, true)
	if err != nil {
		return err
	}
	if len(types) == 0 {
		return fmt.Errorf("TUNNEL_DNS_RECORD_TYPES has no implemented tunnel carriers")
	}

	cfg.TunnelDNSRecordTypes = names
	cfg.TunnelRecordTypes = types
	cfg.TunnelRecordTypeSet = Carrier.SupportedTunnelCarrierSet(types)
	cfg.TunnelRecordNames = names
	return nil
}

func normalizeTunnelPrivateRecordType(value int) (uint16, error) {
	if value == 0 {
		return uint16(defaultTunnelPrivateRecordType), nil
	}
	if value < minPrivateUseRRType || value > maxPrivateUseRRType {
		return 0, fmt.Errorf("TUNNEL_PRIVATE_RECORD_TYPE must be in %d..%d", minPrivateUseRRType, maxPrivateUseRRType)
	}
	return uint16(value), nil
}

func resolveImplementedTunnelCarrierList(values []string, privateType uint16, allowAuto bool) ([]uint16, []string, error) {
	if len(values) == 0 {
		values = implementedTunnelCarrierNames(privateType)
	}

	types := make([]uint16, 0, len(values))
	names := make([]string, 0, len(values))
	seen := make(map[uint16]struct{}, len(values))

	for _, raw := range values {
		value := strings.ToUpper(strings.TrimSpace(raw))
		if value == "" {
			continue
		}

		if value == "AUTO" {
			if !allowAuto {
				return nil, nil, fmt.Errorf("AUTO is not valid in this tunnel carrier list")
			}
			for _, qType := range Carrier.ImplementedTunnelCarrierTypes() {
				if _, ok := seen[qType]; ok {
					continue
				}
				seen[qType] = struct{}{}
				types = append(types, qType)
				names = append(names, Carrier.TunnelCarrierName(qType, privateType))
			}
			continue
		}

		qType, err := Carrier.NormalizeTunnelCarrierNameWithPrivate(value, privateType)
		if err != nil {
			return nil, nil, err
		}
		if !Carrier.IsTunnelCarrierImplemented(qType) {
			continue
		}
		if _, ok := seen[qType]; ok {
			continue
		}
		seen[qType] = struct{}{}
		types = append(types, qType)
		names = append(names, Carrier.TunnelCarrierName(qType, privateType))
	}

	return types, names, nil
}

func implementedTunnelCarrierNames(privateType uint16) []string {
	types := Carrier.ImplementedTunnelCarrierTypes()
	names := make([]string, 0, len(types))
	for _, qType := range types {
		names = append(names, Carrier.TunnelCarrierName(qType, privateType))
	}
	return names
}
