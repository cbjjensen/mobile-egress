package relayadmin

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

// Peer is an immutable copy of kernel-authenticated connection credentials.
type Peer struct {
	uid    uint32
	groups []uint32
}

func NewPeer(uid uint32, groups []uint32) Peer {
	return Peer{uid: uid, groups: append([]uint32(nil), groups...)}
}

func (peer Peer) UID() uint32 { return peer.uid }

func (peer Peer) Groups() []uint32 { return append([]uint32(nil), peer.groups...) }

func (peer Peer) clone() Peer { return NewPeer(peer.uid, peer.groups) }

type AuthorizeFunc func(context.Context, Peer, Operation) bool

// Handler is the typed relay lifecycle boundary. Implementations map their
// internal errors to PublicError when a more specific public code is safe.
type Handler interface {
	Status(context.Context, Peer) (StatusResult, error)
	Setup(context.Context, Peer, SetupRequest) (OwnerBootstrapResult, error)
	Rotate(context.Context, Peer, RotateRequest) (EndpointRotationResult, error)
	Repair(context.Context, Peer) (RepairResult, error)
}

type Server struct {
	Authorize      AuthorizeFunc
	Handler        Handler
	Replay         ReplayStore
	OperationLimit time.Duration
	IOLimit        time.Duration
}

// ServeConn handles exactly one request and at most one response, then closes
// the connection. Framing/JSON without a validated request ID is silent.
func (server *Server) ServeConn(parent context.Context, connection net.Conn, peer Peer) {
	if connection == nil {
		return
	}
	defer connection.Close()
	if parent == nil {
		parent = context.Background()
	}
	if !setConnectionDeadline(connection, parent, server.ioLimit()) {
		return
	}
	requestRaw, err := ReadFrame(connection)
	if err != nil || rejectAvailableTrailing(connection, parent, server.ioLimit()) != nil {
		return
	}
	if !setConnectionDeadline(connection, parent, server.ioLimit()) {
		return
	}

	request, err := ParseRequest(requestRaw)
	if err != nil {
		if request.RequestID == "" {
			return
		}
		code := ErrorInvalidRequest
		var protocolError *ProtocolError
		if errors.As(err, &protocolError) && protocolError.Code.Valid() {
			code = protocolError.Code
		}
		server.writeError(parent, connection, request.RequestID, request.Operation, code)
		return
	}

	operationContext, cancelOperation := context.WithDeadline(parent, boundedDeadline(parent, server.operationLimit()))
	defer cancelOperation()
	immutablePeer := peer.clone()
	if server.Authorize == nil || !server.Authorize(operationContext, immutablePeer.clone(), request.Operation) {
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorUnauthorized)
		return
	}
	if operationContext.Err() != nil {
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorDeadlineExceeded)
		return
	}
	if server.Replay == nil || server.Handler == nil {
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorUnavailable)
		return
	}

	key := ReplayKey{RequestID: request.RequestID, Digest: request.Digest, Operation: request.Operation}
	reservation, err := server.Replay.Reserve(operationContext, key)
	if err != nil {
		if operationContext.Err() != nil {
			server.writeError(parent, connection, request.RequestID, request.Operation, ErrorDeadlineExceeded)
		} else {
			server.writeError(parent, connection, request.RequestID, request.Operation, ErrorUnavailable)
		}
		return
	}
	if operationContext.Err() != nil {
		if reservation.Decision == ReplayExecute {
			server.Replay.Release(context.Background(), key)
		}
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorDeadlineExceeded)
		return
	}
	switch reservation.Decision {
	case ReplayCached:
		cached, parseErr := ParseResponse(reservation.Response)
		if parseErr != nil || cached.RequestID != request.RequestID || cached.Operation != request.Operation || cached.Version != Version {
			server.writeError(parent, connection, request.RequestID, request.Operation, ErrorUnavailable)
			return
		}
		server.writeBody(parent, connection, reservation.Response)
		return
	case ReplayDuplicate:
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorDuplicateRequest)
		return
	case ReplayBusy:
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorBusy)
		return
	case ReplayExecute:
	default:
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorUnavailable)
		return
	}

	completed := false
	defer func() {
		if !completed {
			server.Replay.Release(context.Background(), key)
		}
	}()

	type handlerResult struct {
		result any
		err    error
	}
	resultChannel := make(chan handlerResult, 1)
	go func() {
		result, handlerErr := server.dispatch(operationContext, immutablePeer.clone(), request)
		resultChannel <- handlerResult{result: result, err: handlerErr}
	}()

	var responseBody []byte
	select {
	case result := <-resultChannel:
		if operationContext.Err() != nil {
			responseBody, _ = MarshalErrorResponse(request.RequestID, request.Operation, ErrorDeadlineExceeded)
		} else if result.err != nil {
			responseBody, _ = MarshalErrorResponse(request.RequestID, request.Operation, publicHandlerCode(result.err))
		} else {
			responseBody, err = MarshalSuccessResponse(request.RequestID, request.Operation, result.result)
			if err != nil {
				responseBody, _ = MarshalErrorResponse(request.RequestID, request.Operation, ErrorOperationFailed)
			}
		}
	case <-operationContext.Done():
		responseBody, _ = MarshalErrorResponse(request.RequestID, request.Operation, ErrorDeadlineExceeded)
	}

	completeContext, cancelComplete := context.WithTimeout(context.Background(), server.operationLimit())
	err = server.Replay.Complete(completeContext, key, responseBody)
	cancelComplete()
	if err != nil {
		server.writeError(parent, connection, request.RequestID, request.Operation, ErrorUnavailable)
		return
	}
	completed = true
	server.writeBody(parent, connection, responseBody)
}

func (server *Server) dispatch(ctx context.Context, peer Peer, request Request) (any, error) {
	switch request.Operation {
	case OperationStatus:
		return server.Handler.Status(ctx, peer)
	case OperationSetup:
		params, ok := request.Params.(SetupRequest)
		if !ok {
			return nil, &PublicError{Code: ErrorInvalidRequest}
		}
		return server.Handler.Setup(ctx, peer, params)
	case OperationRotate:
		params, ok := request.Params.(RotateRequest)
		if !ok {
			return nil, &PublicError{Code: ErrorInvalidRequest}
		}
		return server.Handler.Rotate(ctx, peer, params)
	case OperationRepair:
		return server.Handler.Repair(ctx, peer)
	default:
		return nil, &PublicError{Code: ErrorUnsupportedOperation}
	}
}

func publicHandlerCode(err error) ErrorCode {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrorDeadlineExceeded
	}
	var publicError *PublicError
	if errors.As(err, &publicError) && publicError.Code.Valid() {
		return publicError.Code
	}
	return ErrorOperationFailed
}

func (server *Server) writeError(ctx context.Context, connection net.Conn, requestID string, operation Operation, code ErrorCode) {
	body, err := marshalErrorResponseUnchecked(requestID, operation, code)
	if err != nil {
		return
	}
	server.writeBody(ctx, connection, body)
}

func (server *Server) writeBody(ctx context.Context, connection net.Conn, body []byte) {
	if !setConnectionDeadline(connection, ctx, server.ioLimit()) {
		return
	}
	_ = WriteFrame(connection, body)
}

func (server *Server) operationLimit() time.Duration {
	return cappedLimit(server.OperationLimit)
}

func (server *Server) ioLimit() time.Duration {
	return cappedLimit(server.IOLimit)
}

func cappedLimit(configured time.Duration) time.Duration {
	if configured <= 0 || configured > OperationTimeout {
		return OperationTimeout
	}
	return configured
}

func boundedDeadline(ctx context.Context, limit time.Duration) time.Time {
	deadline := time.Now().Add(cappedLimit(limit))
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		return callerDeadline
	}
	return deadline
}

func setConnectionDeadline(connection net.Conn, ctx context.Context, limit time.Duration) bool {
	return connection.SetDeadline(boundedDeadline(ctx, limit)) == nil
}

func rejectAvailableTrailing(connection net.Conn, ctx context.Context, limit time.Duration) error {
	// A short probe window lets already-issued bytes rendezvous on unbuffered
	// streams such as net.Pipe while keeping ordinary request latency bounded.
	probeDeadline := time.Now().Add(5 * time.Millisecond)
	maximumDeadline := boundedDeadline(ctx, limit)
	if maximumDeadline.Before(probeDeadline) {
		probeDeadline = maximumDeadline
	}
	if err := connection.SetReadDeadline(probeDeadline); err != nil {
		return err
	}
	var trailing [1]byte
	n, err := connection.Read(trailing[:])
	if n != 0 || err == nil {
		return ErrTrailingFrameData
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return nil
	}
	// EOF is a valid half-close: the peer can still read the response.
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
