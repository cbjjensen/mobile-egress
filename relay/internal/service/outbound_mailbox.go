package service

import (
	"sync"

	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

const (
	agentOutboundControlCapacity  = 512
	agentOutboundDataCapacity     = 256
	clientOutboundControlCapacity = 64
	clientOutboundDataCapacity    = 32
	outboundDataPerStreamCapacity = 2
)

type outboundAdmission uint8

const (
	outboundAdmitted outboundAdmission = iota
	outboundDataSaturated
	outboundControlSaturated
	outboundClosed
)

type outboundMailbox struct {
	mu sync.Mutex

	controlCapacity       int
	dataCapacity          int
	perStreamDataCapacity int
	controls              []protocol.Envelope
	data                  map[string][]protocol.Envelope
	readyStreams          []string
	dataCount             int
	ready                 chan struct{}
	done                  chan struct{}
	closed                bool
}

func newSessionOutboundMailbox(role enrollment.Role) *outboundMailbox {
	if role == enrollment.RoleAgent {
		return newOutboundMailbox(agentOutboundControlCapacity, agentOutboundDataCapacity, outboundDataPerStreamCapacity)
	}
	return newOutboundMailbox(clientOutboundControlCapacity, clientOutboundDataCapacity, outboundDataPerStreamCapacity)
}

func newOutboundMailbox(controlCapacity, dataCapacity, perStreamDataCapacity int) *outboundMailbox {
	return &outboundMailbox{
		controlCapacity: controlCapacity, dataCapacity: dataCapacity, perStreamDataCapacity: perStreamDataCapacity,
		data: make(map[string][]protocol.Envelope), ready: make(chan struct{}, 1), done: make(chan struct{}),
	}
}

func (mailbox *outboundMailbox) enqueue(envelope protocol.Envelope) outboundAdmission {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	if mailbox.closed {
		return outboundClosed
	}
	if envelope.Type != protocol.TypeData {
		if len(mailbox.controls) >= mailbox.controlCapacity {
			return outboundControlSaturated
		}
		mailbox.controls = append(mailbox.controls, envelope)
		mailbox.signalReady()
		return outboundAdmitted
	}

	streamData := mailbox.data[envelope.StreamID]
	if mailbox.dataCount >= mailbox.dataCapacity || len(streamData) >= mailbox.perStreamDataCapacity {
		return outboundDataSaturated
	}
	if len(streamData) == 0 {
		mailbox.readyStreams = append(mailbox.readyStreams, envelope.StreamID)
	}
	mailbox.data[envelope.StreamID] = append(streamData, envelope)
	mailbox.dataCount++
	mailbox.signalReady()
	return outboundAdmitted
}

func (mailbox *outboundMailbox) poll() (protocol.Envelope, bool) {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	return mailbox.pollLocked()
}

func (mailbox *outboundMailbox) wait() (protocol.Envelope, bool) {
	for {
		mailbox.mu.Lock()
		envelope, ok := mailbox.pollLocked()
		closed := mailbox.closed
		mailbox.mu.Unlock()
		if ok {
			return envelope, true
		}
		if closed {
			return protocol.Envelope{}, false
		}
		select {
		case <-mailbox.ready:
		case <-mailbox.done:
		}
	}
}

func (mailbox *outboundMailbox) close() {
	mailbox.mu.Lock()
	if !mailbox.closed {
		mailbox.closed = true
		mailbox.controls = nil
		mailbox.data = make(map[string][]protocol.Envelope)
		mailbox.readyStreams = nil
		mailbox.dataCount = 0
		close(mailbox.done)
	}
	mailbox.mu.Unlock()
}

func (mailbox *outboundMailbox) discardStreamData(streamID string) {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	streamData := mailbox.data[streamID]
	if len(streamData) == 0 {
		return
	}
	mailbox.dataCount -= len(streamData)
	delete(mailbox.data, streamID)
	for index, readyStreamID := range mailbox.readyStreams {
		if readyStreamID == streamID {
			mailbox.readyStreams = append(mailbox.readyStreams[:index], mailbox.readyStreams[index+1:]...)
			break
		}
	}
}

func (mailbox *outboundMailbox) pollLocked() (protocol.Envelope, bool) {
	if len(mailbox.controls) > 0 {
		envelope := mailbox.controls[0]
		mailbox.controls[0] = protocol.Envelope{}
		mailbox.controls = mailbox.controls[1:]
		return envelope, true
	}
	if len(mailbox.readyStreams) == 0 {
		return protocol.Envelope{}, false
	}

	streamID := mailbox.readyStreams[0]
	mailbox.readyStreams = mailbox.readyStreams[1:]
	streamData := mailbox.data[streamID]
	envelope := streamData[0]
	streamData[0] = protocol.Envelope{}
	streamData = streamData[1:]
	mailbox.dataCount--
	if len(streamData) == 0 {
		delete(mailbox.data, streamID)
	} else {
		mailbox.data[streamID] = streamData
		mailbox.readyStreams = append(mailbox.readyStreams, streamID)
	}
	return envelope, true
}

func (mailbox *outboundMailbox) signalReady() {
	select {
	case mailbox.ready <- struct{}{}:
	default:
	}
}
