package setup

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func payloadFixture(t *testing.T, names []string) []byte {
	t.Helper()
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("signed fixture " + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestEmbeddedPayloadRejectsUnexpectedMissingDuplicateAndTraversalBeforeWriting(t *testing.T) {
	for _, badName := range []string{"../outside.exe", "payload/mobile-egress-admin.exe", "MOBILE-EGRESS-ADMIN.EXE", "extra.exe", AdminExecutableName} {
		t.Run(badName, func(t *testing.T) {
			directory := t.TempDir()
			names := append(append([]string{}, payloadNames...), badName)
			if err := extractPayload(payloadFixture(t, names), directory); err == nil {
				t.Fatal("accepted invalid archive")
			}
			entries, _ := os.ReadDir(directory)
			if len(entries) != 0 {
				t.Fatal("wrote files before validating archive")
			}
		})
	}
	if err := extractPayload(payloadFixture(t, payloadNames[:1]), t.TempDir()); err == nil {
		t.Fatal("accepted incomplete archive")
	}
}

func TestEmbeddedPayloadExtractsExactFilesAndNeverOverwrites(t *testing.T) {
	directory := t.TempDir()
	data := payloadFixture(t, payloadNames)
	if err := extractPayload(data, directory); err != nil {
		t.Fatal(err)
	}
	for _, name := range payloadNames {
		actual, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(actual) != "signed fixture "+name {
			t.Fatalf("incorrect extraction %s: %v", name, err)
		}
	}
	if err := extractPayload(data, directory); err == nil {
		t.Fatal("overwrote existing file")
	}
}

func TestEmbeddedPayloadRejectsCorruption(t *testing.T) {
	data := payloadFixture(t, payloadNames)
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	offset, err := r.File[0].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	data[offset] ^= 0xff
	if err := extractPayload(data, t.TempDir()); err == nil {
		t.Fatal("accepted corrupt archive")
	}
}

func TestEmbeddedPayloadRejectsExcessiveExpansionBeforeWriting(t *testing.T) {
	data := payloadFixture(t, payloadNames)
	central := bytes.Index(data, []byte{'P', 'K', 1, 2})
	if central < 0 {
		t.Fatal("fixture missing ZIP directory")
	}
	binary.LittleEndian.PutUint32(data[central+24:], (512<<20)+1)
	directory := t.TempDir()
	if err := extractPayload(data, directory); err == nil {
		t.Fatal("accepted excessive archive expansion")
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 0 {
		t.Fatal("wrote oversized payload")
	}
}
