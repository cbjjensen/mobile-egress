package relayclient

import (
	"encoding/base64"
	"errors"
)

// SendEndpointUpdate retains only the latest signed update until the phone
// advertises support. Credentials and trust validation stay with nodeservice
// and the phone; acknowledgement still uses the durable configuration protocol.
func (s *Session) SendEndpointUpdate(bundle string) error {
	if !s.direct || len(bundle) == 0 || len(bundle) > 87384 {
		return errors.New("invalid endpoint update")
	}
	s.endpointMu.Lock()
	defer s.endpointMu.Unlock()
	s.pendingEndpoint = bundle
	return s.sendEndpointUpdateLocked()
}

func (s *Session) enableEndpointUpdates() error {
	s.endpointMu.Lock()
	defer s.endpointMu.Unlock()
	s.endpointUpdates = true
	return s.sendEndpointUpdateLocked()
}

func (s *Session) sendEndpointUpdateLocked() error {
	if !s.endpointUpdates || s.pendingEndpoint == "" {
		return nil
	}
	err := s.send(wireEnvelope{Version: 1, Type: "endpoint_update", Payload: base64.RawURLEncoding.EncodeToString([]byte(s.pendingEndpoint))})
	if err == nil {
		s.pendingEndpoint = ""
	}
	return err
}
