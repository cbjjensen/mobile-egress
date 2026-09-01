package relayadmin

import (
	"encoding/binary"
	"errors"
	"io"
)

var (
	ErrInvalidFrame      = errors.New("invalid relay admin frame")
	ErrFrameTooLarge     = errors.New("relay admin frame too large")
	ErrTrailingFrameData = errors.New("trailing relay admin frame data")
)

// ReadFrame reads one bounded four-byte-big-endian length-prefixed body.
func ReadFrame(reader io.Reader) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 {
		return nil, ErrInvalidFrame
	}
	if length > MaximumFrameSize {
		return nil, ErrFrameTooLarge
	}
	body := make([]byte, int(length))
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

// ReadFrameExact reads one frame and then requires EOF, rejecting trailing
// bytes or a second frame.
func ReadFrameExact(reader io.Reader) ([]byte, error) {
	body, err := ReadFrame(reader)
	if err != nil {
		return nil, err
	}
	var trailing [1]byte
	n, err := reader.Read(trailing[:])
	if n != 0 || err == nil {
		return nil, ErrTrailingFrameData
	}
	if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return body, nil
}

// WriteFrame writes a bounded body with full-write semantics.
func WriteFrame(writer io.Writer, body []byte) error {
	if len(body) == 0 {
		return ErrInvalidFrame
	}
	if len(body) > MaximumFrameSize {
		return ErrFrameTooLarge
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if err := writeFull(writer, prefix[:]); err != nil {
		return err
	}
	return writeFull(writer, body)
}

func writeFull(writer io.Writer, buffer []byte) error {
	for len(buffer) > 0 {
		n, err := writer.Write(buffer)
		if n > 0 {
			buffer = buffer[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
