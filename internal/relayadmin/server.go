package relayadmin

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

const replayCleanupTimeout = 250 * time.Millisecond

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

// Mutation gives a mutation handler its durably reserved replay identity and
// the store-owned transaction token used for coordinated state/replay work.
type Mutation struct {
	Key         ReplayKey           `json:"-"`
	Transaction MutationTransaction `json:"-"`
}

// Handler is the typed relay lifecycle boundary. Mutation methods run only
// inside MutationReservation.Execute after durable pre-reservation.
type Handler interface {
	Status(context.Context, Peer) (StatusResult, error)
	Setup(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error)
	Rotate(context.Context, Peer, Mutation, RotateRequest) (EndpointRotationResult, error)
	Repair(context.Context, Peer, Mutation) (RepairResult, error)
}

type Server struct {
	Authorize      AuthorizeFunc
	Handler        Handler
	Replay         ReplayStore
	OperationLimit time.Duration
	IOLimit        time.Duration
}

// ServeOutcome exposes only the post-flush process action needed by the relay
// daemon. A true value is returned after a successful restarting repair frame
// has been fully written and the connection-close defer has run.
type ServeOutcome struct {
	RepairRestartReady bool
}

// ServeConn handles exactly one request and at most one response, then closes
// the connection. The request peer must write-half-close; EOF is required
// before parsing, authorization, replay reservation, or dispatch.
func (server *Server) ServeConn(parent context.Context, connection net.Conn, peer Peer) (outcome ServeOutcome) {
	if connection == nil {
		return
	}
	defer connection.Close()
	if parent == nil {
		parent = context.Background()
	}
	operationContext, cancelOperation := context.WithDeadline(parent, boundedDeadline(parent, server.operationLimit()))
	defer cancelOperation()
	stopInterrupt := interruptConnectionOnCancellation(operationContext, connection)
	defer stopInterrupt()
	if !setConnectionDeadline(connection, operationContext, server.ioLimit()) {
		return
	}
	requestRaw, err := ReadFrame(connection)
	if err != nil || requireRequestEOF(connection) != nil {
		return
	}
	if !setConnectionDeadline(connection, operationContext, server.ioLimit()) {
		return
	}

	request, err := ParseRequest(requestRaw)
	if err != nil {
		if request.RequestID == "" || !request.Operation.Valid() {
			return
		}
		code := ErrorInvalidRequest
		var protocolError *ProtocolError
		if errors.As(err, &protocolError) && protocolError.Code.Valid() {
			code = protocolError.Code
		}
		server.writeError(operationContext, connection, request.RequestID, request.Operation, code)
		return
	}

	immutablePeer := peer.clone()
	if server.Authorize == nil || !server.Authorize(operationContext, immutablePeer.clone(), request.Operation) {
		server.writeError(operationContext, connection, request.RequestID, request.Operation, ErrorUnauthorized)
		return
	}
	if operationContext.Err() != nil {
		return
	}
	if server.Replay == nil || server.Handler == nil {
		server.writeError(operationContext, connection, request.RequestID, request.Operation, ErrorUnavailable)
		return
	}

	key := ReplayKey{RequestID: request.RequestID, Digest: request.Digest, Operation: request.Operation}
	reservation, err := server.Replay.Reserve(operationContext, key)
	if err != nil {
		if operationContext.Err() == nil {
			server.writeError(operationContext, connection, request.RequestID, request.Operation, ErrorUnavailable)
		}
		return
	}
	if operationContext.Err() != nil {
		server.abandonBeforeExecution(key, reservation)
		return
	}
	switch reservation.Decision {
	case ReplayCached:
		cached, parseErr := ParseResponse(reservation.Response)
		if parseErr != nil || cached.RequestID != request.RequestID || cached.Operation != request.Operation || cached.Version != Version {
			server.writeError(operationContext, connection, request.RequestID, request.Operation, ErrorUnavailable)
			return
		}
		if operationContext.Err() == nil && server.writeBody(operationContext, connection, reservation.Response) {
			return flushedResponseOutcome(cached)
		}
		return
	case ReplayDuplicate:
		server.writeError(operationContext, connection, request.RequestID, request.Operation, ErrorDuplicateRequest)
		return
	case ReplayBusy:
		server.writeError(operationContext, connection, request.RequestID, request.Operation, ErrorBusy)
		return
	case ReplayExecute:
	default:
		server.writeError(operationContext, connection, request.RequestID, request.Operation, ErrorUnavailable)
		return
	}

	if request.Operation == OperationStatus {
		server.executeStatus(operationContext, connection, immutablePeer, request, key)
		return
	}
	return server.executeMutation(operationContext, connection, immutablePeer, request, key, reservation.Mutation)
}

func (server *Server) executeStatus(ctx context.Context, connection net.Conn, peer Peer, request Request, key ReplayKey) {
	type handlerResult struct {
		result StatusResult
		err    error
	}
	resultChannel := make(chan handlerResult, 1)
	go func() {
		result, err := server.Handler.Status(ctx, peer.clone())
		resultChannel <- handlerResult{result: result, err: err}
	}()

	var responseBody []byte
	select {
	case result := <-resultChannel:
		if ctx.Err() != nil {
			server.abandonStatus(key)
			return
		}
		if result.err != nil {
			responseBody, _ = MarshalErrorResponse(request.RequestID, request.Operation, publicHandlerCode(result.err))
		} else {
			responseBody, _ = MarshalSuccessResponse(request.RequestID, request.Operation, result.result)
			if len(responseBody) == 0 {
				responseBody, _ = MarshalErrorResponse(request.RequestID, request.Operation, ErrorOperationFailed)
			}
		}
	case <-ctx.Done():
		server.abandonStatus(key)
		return
	}
	if err := server.Replay.CompleteStatus(ctx, key, responseBody); err != nil {
		server.abandonStatus(key)
		if ctx.Err() == nil {
			server.writeError(ctx, connection, request.RequestID, request.Operation, ErrorUnavailable)
		}
		return
	}
	if ctx.Err() == nil {
		server.writeBody(ctx, connection, responseBody)
	}
}

func (server *Server) executeMutation(ctx context.Context, connection net.Conn, peer Peer, request Request, key ReplayKey, reservation MutationReservation) (outcome ServeOutcome) {
	if reservation == nil || reservation.Key() != key {
		server.writeError(ctx, connection, request.RequestID, request.Operation, ErrorUnavailable)
		return
	}
	if ctx.Err() != nil {
		server.abandonMutationBeforeExecution(reservation)
		return
	}
	type executionResult struct {
		response []byte
		err      error
	}
	resultChannel := make(chan executionResult, 1)
	go func() {
		response, err := reservation.Execute(ctx, func(executionContext context.Context, transaction MutationTransaction) ([]byte, error) {
			if transaction == nil || transaction.ReplayKey() != key || executionContext.Err() != nil {
				return nil, ErrReplayState
			}
			result, handlerErr := server.dispatchMutation(executionContext, peer.clone(), Mutation{Key: key, Transaction: transaction}, request)
			if executionContext.Err() != nil {
				return nil, executionContext.Err()
			}
			if handlerErr != nil {
				code, determinate := determinateMutationErrorCode(handlerErr)
				if !determinate {
					return nil, ErrMutationIndeterminate
				}
				response, marshalErr := MarshalErrorResponse(request.RequestID, request.Operation, code)
				if marshalErr != nil {
					return nil, ErrMutationIndeterminate
				}
				return response, nil
			}
			response, marshalErr := MarshalSuccessResponse(request.RequestID, request.Operation, result)
			if marshalErr != nil {
				return nil, ErrMutationIndeterminate
			}
			return response, nil
		})
		resultChannel <- executionResult{response: response, err: err}
	}()

	select {
	case result := <-resultChannel:
		if result.err != nil {
			server.abandonMutationBeforeExecution(reservation)
			if ctx.Err() == nil {
				code := ErrorUnavailable
				if errors.Is(result.err, ErrMutationIndeterminate) {
					code = ErrorOperationFailed
				}
				server.writeError(ctx, connection, request.RequestID, request.Operation, code)
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
		response, parseErr := ParseResponse(result.response)
		if parseErr != nil || response.RequestID != key.RequestID || response.Operation != key.Operation {
			server.writeError(ctx, connection, request.RequestID, request.Operation, ErrorUnavailable)
			return
		}
		if server.writeBody(ctx, connection, result.response) {
			return flushedResponseOutcome(response)
		}
	case <-ctx.Done():
		// Abandon removes only a reservation that Execute has not started. An
		// executing mutation remains durable and becomes indeterminate.
		server.abandonMutationBeforeExecution(reservation)
		return
	}
	return
}

func flushedResponseOutcome(response Response) ServeOutcome {
	if !response.OK || response.Operation != OperationRepair {
		return ServeOutcome{}
	}
	result, ok := response.Result.(RepairResult)
	return ServeOutcome{RepairRestartReady: ok && result.Restarting}
}

func (server *Server) dispatchMutation(ctx context.Context, peer Peer, mutation Mutation, request Request) (any, error) {
	switch request.Operation {
	case OperationSetup:
		params, ok := request.Params.(SetupRequest)
		if !ok {
			return nil, &PublicError{Code: ErrorInvalidRequest}
		}
		return server.Handler.Setup(ctx, peer, mutation, params)
	case OperationRotate:
		params, ok := request.Params.(RotateRequest)
		if !ok {
			return nil, &PublicError{Code: ErrorInvalidRequest}
		}
		return server.Handler.Rotate(ctx, peer, mutation, params)
	case OperationRepair:
		return server.Handler.Repair(ctx, peer, mutation)
	default:
		return nil, &PublicError{Code: ErrorUnsupportedOperation}
	}
}

func (server *Server) abandonBeforeExecution(key ReplayKey, reservation ReplayReservation) {
	if reservation.Decision != ReplayExecute {
		return
	}
	if key.Operation == OperationStatus {
		server.abandonStatus(key)
		return
	}
	if reservation.Mutation != nil && reservation.Mutation.Key() == key {
		server.abandonMutationBeforeExecution(reservation.Mutation)
	}
}

func (server *Server) abandonStatus(key ReplayKey) {
	cleanupContext, cancel := context.WithTimeout(context.Background(), replayCleanupTimeout)
	defer cancel()
	_ = server.Replay.AbandonStatus(cleanupContext, key)
}

func (server *Server) abandonMutationBeforeExecution(reservation MutationReservation) {
	if reservation == nil {
		return
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), replayCleanupTimeout)
	defer cancel()
	_ = reservation.Abandon(cleanupContext)
}

func publicHandlerCode(err error) ErrorCode {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrorDeadlineExceeded
	}
	var publicError *PublicError
	if errors.As(err, &publicError) && publicError != nil && publicError.Code.Valid() {
		return publicError.Code
	}
	return ErrorOperationFailed
}

func determinateMutationErrorCode(err error) (ErrorCode, bool) {
	var publicError *PublicError
	if !errors.As(err, &publicError) || publicError == nil || !publicError.Code.Valid() {
		return "", false
	}
	return publicError.Code, true
}

func (server *Server) writeError(ctx context.Context, connection net.Conn, requestID string, operation Operation, code ErrorCode) {
	if ctx.Err() != nil || !operation.Valid() {
		return
	}
	body, err := MarshalErrorResponse(requestID, operation, code)
	if err != nil {
		return
	}
	server.writeBody(ctx, connection, body)
}

func (server *Server) writeBody(ctx context.Context, connection net.Conn, body []byte) bool {
	if ctx.Err() != nil || !setConnectionDeadline(connection, ctx, server.ioLimit()) {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	return WriteFrame(connection, body) == nil
}

func (server *Server) operationLimit() time.Duration { return cappedLimit(server.OperationLimit) }

func (server *Server) ioLimit() time.Duration { return cappedLimit(server.IOLimit) }

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
	if ctx.Err() != nil {
		_ = connection.SetDeadline(time.Now())
		return false
	}
	return connection.SetDeadline(boundedDeadline(ctx, limit)) == nil
}

func requireRequestEOF(connection net.Conn) error {
	var trailing [1]byte
	n, err := connection.Read(trailing[:])
	if n != 0 || err == nil {
		return ErrTrailingFrameData
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func interruptConnectionOnCancellation(ctx context.Context, connection net.Conn) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = connection.SetDeadline(time.Now())
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}
