package service

import (
	"sync"

	"mobile-egress/internal/capacity"
	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

type outboundDataBudget struct {
	mu sync.Mutex

	frameCapacity int
	byteCapacity  int
	frames        int
	bytes         int
}

func newOutboundDataBudget(frameCapacity, byteCapacity int) *outboundDataBudget {
	return &outboundDataBudget{frameCapacity: frameCapacity, byteCapacity: byteCapacity}
}

func (budget *outboundDataBudget) tryReserve(byteCount int) (*outboundDataReservation, bool) {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if byteCount < 0 || budget.frames >= budget.frameCapacity || byteCount > budget.byteCapacity-budget.bytes {
		return nil, false
	}
	budget.frames++
	budget.bytes += byteCount
	return &outboundDataReservation{budget: budget, bytes: byteCount}, true
}

func (budget *outboundDataBudget) refund(frames, bytes int) {
	budget.mu.Lock()
	budget.frames -= frames
	budget.bytes -= bytes
	budget.mu.Unlock()
}

type outboundDataReservation struct {
	budget *outboundDataBudget
	bytes  int
	once   sync.Once
}

type outboundMailboxItem struct {
	envelope    protocol.Envelope
	mailbox     *outboundMailbox
	reservation *outboundDataReservation
	once        sync.Once
}

func (item *outboundMailboxItem) complete() {
	if item == nil {
		return
	}
	item.once.Do(func() {
		if item.reservation == nil {
			return
		}
		item.mailbox.completeData(item.envelope.StreamID)
		item.reservation.release()
	})
}

func (reservation *outboundDataReservation) release() {
	if reservation == nil {
		return
	}
	reservation.once.Do(func() {
		reservation.budget.refund(1, reservation.bytes)
	})
}

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
	perStreamDataCapacity int
	dataBudget            *outboundDataBudget
	controls              []*outboundMailboxItem
	data                  map[string][]*outboundMailboxItem
	readyStreams          []string
	streamDataCounts      map[string]int
	ready                 chan struct{}
	done                  chan struct{}
	closed                bool
}

func newSessionOutboundMailbox(_ enrollment.Role, dataBudget *outboundDataBudget) *outboundMailbox {
	return newOutboundMailbox(capacity.ControlFramesPerSession, capacity.DataFramesPerStream, dataBudget)
}

func newOutboundMailbox(controlCapacity, perStreamDataCapacity int, dataBudget *outboundDataBudget) *outboundMailbox {
	return &outboundMailbox{
		controlCapacity: controlCapacity, perStreamDataCapacity: perStreamDataCapacity, dataBudget: dataBudget,
		data: make(map[string][]*outboundMailboxItem), streamDataCounts: make(map[string]int),
		ready: make(chan struct{}, 1), done: make(chan struct{}),
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
		mailbox.controls = append(mailbox.controls, &outboundMailboxItem{envelope: envelope})
		mailbox.signalReady()
		return outboundAdmitted
	}

	if mailbox.streamDataCounts[envelope.StreamID] >= mailbox.perStreamDataCapacity {
		return outboundDataSaturated
	}
	reservation, ok := mailbox.dataBudget.tryReserve(len(envelope.Payload))
	if !ok {
		return outboundDataSaturated
	}
	item := &outboundMailboxItem{envelope: envelope, mailbox: mailbox, reservation: reservation}
	streamData := mailbox.data[envelope.StreamID]
	if len(streamData) == 0 {
		mailbox.readyStreams = append(mailbox.readyStreams, envelope.StreamID)
	}
	mailbox.data[envelope.StreamID] = append(streamData, item)
	mailbox.streamDataCounts[envelope.StreamID]++
	mailbox.signalReady()
	return outboundAdmitted
}

func (mailbox *outboundMailbox) poll() (*outboundMailboxItem, bool) {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	return mailbox.pollLocked()
}

func (mailbox *outboundMailbox) wait() (*outboundMailboxItem, bool) {
	for {
		mailbox.mu.Lock()
		envelope, ok := mailbox.pollLocked()
		closed := mailbox.closed
		mailbox.mu.Unlock()
		if ok {
			return envelope, true
		}
		if closed {
			return nil, false
		}
		select {
		case <-mailbox.ready:
		case <-mailbox.done:
		}
	}
}

func (mailbox *outboundMailbox) close() {
	mailbox.mu.Lock()
	if mailbox.closed {
		mailbox.mu.Unlock()
		return
	}
	mailbox.closed = true
	mailbox.controls = nil
	queued := make([]*outboundMailboxItem, 0)
	for _, streamData := range mailbox.data {
		queued = append(queued, streamData...)
	}
	mailbox.data = make(map[string][]*outboundMailboxItem)
	mailbox.readyStreams = nil
	close(mailbox.done)
	mailbox.mu.Unlock()
	for _, item := range queued {
		item.complete()
	}
}

func (mailbox *outboundMailbox) discardStreamData(streamID string) {
	mailbox.mu.Lock()
	streamData := mailbox.data[streamID]
	if len(streamData) == 0 {
		mailbox.mu.Unlock()
		return
	}
	delete(mailbox.data, streamID)
	for index, readyStreamID := range mailbox.readyStreams {
		if readyStreamID == streamID {
			mailbox.readyStreams = append(mailbox.readyStreams[:index], mailbox.readyStreams[index+1:]...)
			break
		}
	}
	mailbox.mu.Unlock()
	for _, item := range streamData {
		item.complete()
	}
}

func (mailbox *outboundMailbox) pollLocked() (*outboundMailboxItem, bool) {
	if len(mailbox.controls) > 0 {
		item := mailbox.controls[0]
		mailbox.controls[0] = nil
		mailbox.controls = mailbox.controls[1:]
		return item, true
	}
	if len(mailbox.readyStreams) == 0 {
		return nil, false
	}

	streamID := mailbox.readyStreams[0]
	mailbox.readyStreams = mailbox.readyStreams[1:]
	streamData := mailbox.data[streamID]
	item := streamData[0]
	streamData[0] = nil
	streamData = streamData[1:]
	if len(streamData) == 0 {
		delete(mailbox.data, streamID)
	} else {
		mailbox.data[streamID] = streamData
		mailbox.readyStreams = append(mailbox.readyStreams, streamID)
	}
	return item, true
}

func (mailbox *outboundMailbox) completeData(streamID string) {
	mailbox.mu.Lock()
	if mailbox.streamDataCounts[streamID] <= 1 {
		delete(mailbox.streamDataCounts, streamID)
	} else {
		mailbox.streamDataCounts[streamID]--
	}
	mailbox.mu.Unlock()
}

func (mailbox *outboundMailbox) signalReady() {
	select {
	case mailbox.ready <- struct{}{}:
	default:
	}
}
