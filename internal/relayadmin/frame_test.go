package relayadmin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrameAcceptsExactly512KiBAndRejects512KiBPlusOneBeforeBodyRead(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte{'x'}, MaximumFrameSize)
	framed := append(framePrefix(len(body)), body...)
	got, err := ReadFrame(bytes.NewReader(framed))
	if err != nil {
		t.Fatalf("ReadFrame(exact limit) returned an error: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("ReadFrame(exact limit) changed the body")
	}

	reader := &countingReader{reader: bytes.NewReader(framePrefix(MaximumFrameSize + 1))}
	if _, err := ReadFrame(reader); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ReadFrame(over limit) error = %v, want ErrFrameTooLarge", err)
	}
	if reader.bytesRead != 4 {
		t.Fatalf("ReadFrame(over limit) read %d bytes, want only the four-byte prefix", reader.bytesRead)
	}
}

func TestFrameRejectsZeroShortPrefixShortBodyAndConcatenatedSecondFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
		err  error
	}{
		{name: "zero", raw: framePrefix(0), err: ErrInvalidFrame},
		{name: "short prefix", raw: []byte{0, 0, 1}, err: io.ErrUnexpectedEOF},
		{name: "short body", raw: append(framePrefix(4), 'a', 'b'), err: io.ErrUnexpectedEOF},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ReadFrame(bytes.NewReader(test.raw)); !errors.Is(err, test.err) {
				t.Fatalf("ReadFrame() error = %v, want %v", err, test.err)
			}
		})
	}

	first := append(framePrefix(2), 'o', 'k')
	second := append(framePrefix(1), 'x')
	if _, err := ReadFrameExact(bytes.NewReader(append(first, second...))); !errors.Is(err, ErrTrailingFrameData) {
		t.Fatalf("ReadFrameExact(concatenated) error = %v, want ErrTrailingFrameData", err)
	}
}

func TestFrameUsesFullReadsAndWritesAndRejectsOversizedOutput(t *testing.T) {
	t.Parallel()

	body := []byte("short I/O must still carry the full body")
	writer := &shortWriter{maximum: 3}
	if err := WriteFrame(writer, body); err != nil {
		t.Fatalf("WriteFrame(short writer) returned an error: %v", err)
	}
	want := append(framePrefix(len(body)), body...)
	if !bytes.Equal(writer.buffer.Bytes(), want) {
		t.Fatalf("WriteFrame(short writer) = %x, want %x", writer.buffer.Bytes(), want)
	}

	chunked := &shortReader{reader: bytes.NewReader(want), maximum: 2}
	got, err := ReadFrame(chunked)
	if err != nil {
		t.Fatalf("ReadFrame(short reader) returned an error: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("ReadFrame(short reader) = %q, want %q", got, body)
	}

	if err := WriteFrame(io.Discard, bytes.Repeat([]byte{'x'}, MaximumFrameSize+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WriteFrame(over limit) error = %v, want ErrFrameTooLarge", err)
	}

	if err := WriteFrame(zeroWriter{}, body); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("WriteFrame(zero writer) error = %v, want io.ErrNoProgress", err)
	}
}

func framePrefix(length int) []byte {
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(length))
	return prefix
}

type countingReader struct {
	reader    io.Reader
	bytesRead int
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	n, err := reader.reader.Read(buffer)
	reader.bytesRead += n
	return n, err
}

type shortReader struct {
	reader  io.Reader
	maximum int
}

func (reader *shortReader) Read(buffer []byte) (int, error) {
	if len(buffer) > reader.maximum {
		buffer = buffer[:reader.maximum]
	}
	return reader.reader.Read(buffer)
}

type shortWriter struct {
	buffer  bytes.Buffer
	maximum int
}

func (writer *shortWriter) Write(buffer []byte) (int, error) {
	if len(buffer) > writer.maximum {
		buffer = buffer[:writer.maximum]
	}
	return writer.buffer.Write(buffer)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }
