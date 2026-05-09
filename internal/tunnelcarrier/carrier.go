// ==============================================================================
// StormDNS
// Author: nullroute1970
// Github: https://github.com/nullroute1970/StormDNS
// Year: 2026
// ==============================================================================

package tunnelcarrier

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	Enums "stormdns-go/internal/enums"
)

const (
	DefaultTunnelPrivateRecordType uint16 = 65280
	MinPrivateUseRRType            uint16 = 65280
	MaxPrivateUseRRType            uint16 = 65534
)

var (
	ErrUnsupportedCarrier       = errors.New("unsupported dns tunnel carrier")
	ErrCarrierPayloadTooLarge   = errors.New("dns tunnel carrier payload too large")
	ErrCarrierAnswerMalformed   = errors.New("dns tunnel carrier answer malformed")
	ErrInvalidPrivateRecordType = errors.New("invalid private-use dns record type")
)

type TunnelCarrier struct {
	Type       uint16
	Name       string
	Binary     bool
	MaxPayload int
}

var implementedTunnelCarriers = []TunnelCarrier{
	{
		Type:       Enums.DNS_RECORD_TYPE_TXT,
		Name:       "TXT",
		Binary:     false,
		MaxPayload: 255,
	},
}

func NormalizeTunnelCarrierName(value string) (uint16, error) {
	return NormalizeTunnelCarrierNameWithPrivate(value, DefaultTunnelPrivateRecordType)
}

func NormalizeTunnelCarrierNameWithPrivate(value string, privateType uint16) (uint16, error) {
	name := strings.ToUpper(strings.TrimSpace(value))
	name = strings.TrimPrefix(name, "DNS_RECORD_TYPE_")
	if name == "" {
		return 0, fmt.Errorf("%w: empty carrier name", ErrUnsupportedCarrier)
	}

	switch name {
	case "A":
		return Enums.DNS_RECORD_TYPE_A, nil
	case "AAAA":
		return Enums.DNS_RECORD_TYPE_AAAA, nil
	case "CAA":
		return Enums.DNS_RECORD_TYPE_CAA, nil
	case "CNAME":
		return Enums.DNS_RECORD_TYPE_CNAME, nil
	case "MX":
		return Enums.DNS_RECORD_TYPE_MX, nil
	case "NULL":
		return Enums.DNS_RECORD_TYPE_NULL, nil
	case "PRIVATE":
		if !IsPrivateUseRRType(privateType) {
			return 0, ErrInvalidPrivateRecordType
		}
		return privateType, nil
	case "SRV":
		return Enums.DNS_RECORD_TYPE_SRV, nil
	case "TXT":
		return Enums.DNS_RECORD_TYPE_TXT, nil
	}

	if strings.HasPrefix(name, "TYPE") {
		qType, err := strconv.ParseUint(strings.TrimPrefix(name, "TYPE"), 10, 16)
		if err != nil {
			return 0, fmt.Errorf("%w: %s", ErrUnsupportedCarrier, value)
		}
		return uint16(qType), nil
	}

	qType, err := strconv.ParseUint(name, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrUnsupportedCarrier, value)
	}
	return uint16(qType), nil
}

func TunnelCarrierName(qType uint16, privateType uint16) string {
	if IsPrivateUseRRType(qType) && qType == privateType {
		return "PRIVATE"
	}

	switch qType {
	case Enums.DNS_RECORD_TYPE_NULL:
		return "NULL"
	default:
		return Enums.DNSRecordTypeName(qType)
	}
}

func IsPrivateUseRRType(qType uint16) bool {
	return qType >= MinPrivateUseRRType && qType <= MaxPrivateUseRRType
}

func IsTunnelCarrierImplemented(qType uint16) bool {
	for _, carrier := range implementedTunnelCarriers {
		if carrier.Type == qType {
			return true
		}
	}
	return false
}

func ImplementedTunnelCarrierTypes() []uint16 {
	types := make([]uint16, 0, len(implementedTunnelCarriers))
	for _, carrier := range implementedTunnelCarriers {
		types = append(types, carrier.Type)
	}
	return types
}

func ImplementedTunnelCarriers() []TunnelCarrier {
	out := make([]TunnelCarrier, len(implementedTunnelCarriers))
	copy(out, implementedTunnelCarriers)
	return out
}

func SupportedTunnelCarrierSet(types []uint16) map[uint16]struct{} {
	if len(types) == 0 {
		types = ImplementedTunnelCarrierTypes()
	}

	out := make(map[uint16]struct{}, len(types))
	for _, qType := range types {
		if IsTunnelCarrierImplemented(qType) {
			out[qType] = struct{}{}
		}
	}
	return out
}
