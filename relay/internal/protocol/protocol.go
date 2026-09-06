// Package protocol exposes the shared tunnel codec to relay components.
package protocol

import "mobile-egress/internal/tunnelwire"

const (
	Version1                   = tunnelwire.Version1
	MaxDecodedPayloadBytes     = tunnelwire.MaxDecodedPayloadBytes
	MaxDecodedDataPayloadBytes = tunnelwire.MaxDecodedDataPayloadBytes
	TypeOpen                   = tunnelwire.TypeOpen
	TypeOpened                 = tunnelwire.TypeOpened
	TypeRejected               = tunnelwire.TypeRejected
	TypeData                   = tunnelwire.TypeData
	TypeClose                  = tunnelwire.TypeClose
	TypePing                   = tunnelwire.TypePing
	TypePong                   = tunnelwire.TypePong
)

var ErrInvalidEnvelope = tunnelwire.ErrInvalidEnvelope

type MessageType = tunnelwire.MessageType
type Envelope = tunnelwire.Envelope

func ParseEnvelope(raw []byte) (Envelope, error) {
	return tunnelwire.ParseEnvelope(raw)
}
