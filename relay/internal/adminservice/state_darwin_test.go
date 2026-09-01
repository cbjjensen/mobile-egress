//go:build darwin

package adminservice

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinStateMetadataConvertsStatWithoutSignExtension(t *testing.T) {
	t.Parallel()

	stat := unix.Stat_t{
		Dev:   -2,
		Ino:   0xfedcba9876543210,
		Mode:  uint16(unix.S_IFREG) | 0o640,
		Nlink: 3,
		Uid:   0x87654321,
		Gid:   0xfedcba98,
	}
	metadata := pathMetadataFromDarwinStat(stat)
	if metadata.Device != uint64(uint32(stat.Dev)) {
		t.Fatalf("Device = %#x, want zero-extended %#x", metadata.Device, uint64(uint32(stat.Dev)))
	}
	if metadata.Inode != uint64(stat.Ino) || metadata.UID != stat.Uid || metadata.GID != stat.Gid || metadata.Links != stat.Nlink {
		t.Fatalf("metadata = %#v, want inode/UID/GID/link fields copied exactly", metadata)
	}
	if metadata.RawType != uint16(unix.S_IFREG) || metadata.Type != pathTypeRegular || metadata.Permissions != 0o640 {
		t.Fatalf("metadata mode conversion = %#v, want raw regular type and permissions 0640", metadata)
	}
}

func TestDarwinStateOpenFlagsPreventBlockingAndSpecialDeviceEffects(t *testing.T) {
	t.Parallel()

	const alwaysRequired = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_NOFOLLOW_ANY
	for _, test := range []struct {
		name      string
		object    pathObjectType
		directory bool
	}{
		{name: "regular file", object: pathTypeRegular},
		{name: "directory", object: pathTypeDirectory, directory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			flags, err := darwinOpenFlags(pathMetadata{Type: test.object})
			if err != nil {
				t.Fatalf("darwinOpenFlags() error = %v", err)
			}
			if flags&alwaysRequired != alwaysRequired {
				t.Fatalf("darwinOpenFlags() = %#x, want required flags %#x", flags, alwaysRequired)
			}
			if got := flags&unix.O_DIRECTORY != 0; got != test.directory {
				t.Fatalf("darwinOpenFlags() O_DIRECTORY = %v, want %v", got, test.directory)
			}
		})
	}

	if _, err := darwinOpenFlags(pathMetadata{Type: pathTypeOther}); !errors.Is(err, errStatePathUnsafe) {
		t.Fatalf("darwinOpenFlags(special object) error = %v, want errStatePathUnsafe", err)
	}
}
