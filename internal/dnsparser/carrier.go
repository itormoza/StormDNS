// ==============================================================================
// StormDNS
// Author: nullroute1970
// Github: https://github.com/nullroute1970/StormDNS
// Year: 2026
// ==============================================================================

package dnsparser

import Carrier "stormdns-go/internal/tunnelcarrier"

const (
	DefaultTunnelPrivateRecordType = Carrier.DefaultTunnelPrivateRecordType
	MinPrivateUseRRType            = Carrier.MinPrivateUseRRType
	MaxPrivateUseRRType            = Carrier.MaxPrivateUseRRType
)

var (
	ErrUnsupportedCarrier       = Carrier.ErrUnsupportedCarrier
	ErrCarrierPayloadTooLarge   = Carrier.ErrCarrierPayloadTooLarge
	ErrCarrierAnswerMalformed   = Carrier.ErrCarrierAnswerMalformed
	ErrInvalidPrivateRecordType = Carrier.ErrInvalidPrivateRecordType
)

type TunnelCarrier = Carrier.TunnelCarrier

func NormalizeTunnelCarrierName(value string) (uint16, error) {
	return Carrier.NormalizeTunnelCarrierName(value)
}

func NormalizeTunnelCarrierNameWithPrivate(value string, privateType uint16) (uint16, error) {
	return Carrier.NormalizeTunnelCarrierNameWithPrivate(value, privateType)
}

func TunnelCarrierName(qType uint16, privateType uint16) string {
	return Carrier.TunnelCarrierName(qType, privateType)
}

func IsPrivateUseRRType(qType uint16) bool {
	return Carrier.IsPrivateUseRRType(qType)
}

func IsTunnelCarrierImplemented(qType uint16) bool {
	return Carrier.IsTunnelCarrierImplemented(qType)
}

func ImplementedTunnelCarrierTypes() []uint16 {
	return Carrier.ImplementedTunnelCarrierTypes()
}

func ImplementedTunnelCarriers() []TunnelCarrier {
	return Carrier.ImplementedTunnelCarriers()
}

func SupportedTunnelCarrierSet(types []uint16) map[uint16]struct{} {
	return Carrier.SupportedTunnelCarrierSet(types)
}
