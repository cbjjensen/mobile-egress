package relayadmin

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

var ErrTransport = errors.New("relay admin transport unavailable")

type DialFunc func(context.Context) (net.Conn, error)

type Client struct {
	Dial           DialFunc
	Random         io.Reader
	OperationLimit time.Duration
	IOLimit        time.Duration
}

func (client *Client) Status(ctx context.Context) (StatusResult, error) {
	response, err := client.do(ctx, OperationStatus, StatusRequest{})
	if err != nil {
		return StatusResult{}, err
	}
	result, ok := response.Result.(StatusResult)
	if !ok {
		return StatusResult{}, ErrInvalidResponse
	}
	return result, nil
}

func (client *Client) Setup(ctx context.Context, request SetupRequest) (OwnerBootstrapResult, error) {
	response, err := client.do(ctx, OperationSetup, request)
	if err != nil {
		return OwnerBootstrapResult{}, err
	}
	result, ok := response.Result.(OwnerBootstrapResult)
	if !ok {
		return OwnerBootstrapResult{}, ErrInvalidResponse
	}
	return result, nil
}

func (client *Client) Rotate(ctx context.Context, request RotateRequest) (EndpointRotationResult, error) {
	response, err := client.do(ctx, OperationRotate, request)
	if err != nil {
		return EndpointRotationResult{}, err
	}
	result, ok := response.Result.(EndpointRotationResult)
	if !ok {
		return EndpointRotationResult{}, ErrInvalidResponse
	}
	return result, nil
}

func (client *Client) Repair(ctx context.Context) (RepairResult, error) {
	response, err := client.do(ctx, OperationRepair, RepairRequest{})
	if err != nil {
		return RepairResult{}, err
	}
	result, ok := response.Result.(RepairResult)
	if !ok {
		return RepairResult{}, ErrInvalidResponse
	}
	return result, nil
}

func (client *Client) do(parent context.Context, operation Operation, params any) (Response, error) {
	if parent == nil {
		parent = context.Background()
	}
	if client == nil || client.Dial == nil {
		return Response{}, ErrTransport
	}
	operationContext, cancel := context.WithDeadline(parent, boundedDeadline(parent, cappedLimit(client.OperationLimit)))
	defer cancel()
	requestID, err := GenerateRequestID(client.Random)
	if err != nil {
		return Response{}, ErrTransport
	}
	requestBody, err := MarshalRequest(requestID, operation, params)
	if err != nil {
		return Response{}, ErrInvalidRequest
	}

	for attempt := 0; attempt < 2; attempt++ {
		response, retryable, err := client.attempt(operationContext, requestID, operation, requestBody)
		if err == nil {
			if !response.OK {
				return Response{}, &PublicError{Code: response.ErrorCode}
			}
			return response, nil
		}
		if contextErr := completedContextError(operationContext); contextErr != nil {
			return Response{}, contextErr
		}
		if !retryable || attempt == 1 {
			return Response{}, err
		}
	}
	return Response{}, ErrTransport
}

func completedContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func (client *Client) attempt(ctx context.Context, requestID string, operation Operation, requestBody []byte) (Response, bool, error) {
	connection, err := client.Dial(ctx)
	if err != nil {
		return Response{}, true, ErrTransport
	}
	if connection == nil {
		return Response{}, true, ErrTransport
	}
	defer connection.Close()
	halfCloser, ok := connection.(interface{ CloseWrite() error })
	if !ok {
		return Response{}, false, ErrTransport
	}
	stopInterrupt := interruptConnectionOnCancellation(ctx, connection)
	defer stopInterrupt()
	if !setConnectionDeadline(connection, ctx, cappedLimit(client.IOLimit)) {
		return Response{}, true, ErrTransport
	}
	if err := WriteFrame(connection, requestBody); err != nil {
		return Response{}, true, ErrTransport
	}
	if err := halfCloser.CloseWrite(); err != nil {
		return Response{}, true, ErrTransport
	}
	responseBody, err := ReadFrameExact(connection)
	if err != nil {
		if errors.Is(err, ErrInvalidFrame) || errors.Is(err, ErrFrameTooLarge) || errors.Is(err, ErrTrailingFrameData) {
			return Response{}, false, ErrInvalidResponse
		}
		return Response{}, true, ErrTransport
	}
	response, err := ParseResponse(responseBody)
	if err != nil {
		return Response{}, false, ErrInvalidResponse
	}
	if response.Version != Version || response.RequestID != requestID || response.Operation != operation {
		return Response{}, false, ErrInvalidResponse
	}
	return response, false, nil
}
