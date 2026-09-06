// Package tunnelwire defines the JSON and negotiated binary framing shared by Go peers.
package tunnelwire

import (
	"encoding/binary"
	"errors"
)

const Capability = "mobile-egress.transport.v2"
const MaxDataBytes = 32 << 10

func IsData(raw []byte) bool { return len(raw) > 0 && raw[0] == 2 }

func ValidStreamID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ParseData returns a payload view; callers retain the message until delivery.
func ParseData(raw []byte) (string, []byte, error) {
	if len(raw) < 4 || raw[0] != 2 || raw[1] != 4 {
		return "", nil, errors.New("invalid binary data header")
	}
	n := int(binary.BigEndian.Uint16(raw[2:4]))
	if n < 1 || n > 128 || n > len(raw)-4 || len(raw)-4-n > MaxDataBytes {
		return "", nil, errors.New("invalid binary data size")
	}
	id := string(raw[4 : 4+n])
	if !ValidStreamID(id) {
		return "", nil, errors.New("invalid binary stream ID")
	}
	return id, raw[4+n:], nil
}

func EncodeData(id string, payload []byte) ([]byte, error) {
	if !ValidStreamID(id) || len(payload) > MaxDataBytes {
		return nil, errors.New("invalid binary data")
	}
	raw := make([]byte, 4+len(id)+len(payload))
	raw[0], raw[1] = 2, 4
	binary.BigEndian.PutUint16(raw[2:4], uint16(len(id)))
	copy(raw[4:], id)
	copy(raw[4+len(id):], payload)
	return raw, nil
}
