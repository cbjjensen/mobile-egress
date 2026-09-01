package adminservice

import "testing"

func TestParseCanonicalAdminGID(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want uint32
	}{
		{raw: "1", want: 1},
		{raw: "80", want: 80},
		{raw: "4294967295", want: 4294967295},
	} {
		t.Run("accept_"+test.raw, func(t *testing.T) {
			got, err := ParseCanonicalAdminGID(test.raw)
			if err != nil {
				t.Fatalf("ParseCanonicalAdminGID(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("ParseCanonicalAdminGID(%q) = %d, want %d", test.raw, got, test.want)
			}
		})
	}

	for _, raw := range []string{
		"", "0", "00", "080", "+1", "-1", " 1", "1 ", "1 0", "1\t0", "abc", "1a", "4294967296",
	} {
		t.Run("reject_"+raw, func(t *testing.T) {
			if got, err := ParseCanonicalAdminGID(raw); err == nil {
				t.Fatalf("ParseCanonicalAdminGID(%q) = %d, want error", raw, got)
			}
		})
	}
}

func TestPeerFromXucredCopiesUIDAndGroups(t *testing.T) {
	snapshot := xucredSnapshot{Version: 0, UID: 501, NGroups: 3}
	snapshot.Groups[0] = 20
	snapshot.Groups[1] = 80
	snapshot.Groups[2] = 701

	peer, err := peerFromXucred(snapshot)
	if err != nil {
		t.Fatalf("peerFromXucred: %v", err)
	}
	snapshot.Groups[0] = 999
	first := peer.Groups()
	first[1] = 998
	if peer.UID() != 501 {
		t.Fatalf("UID = %d, want 501", peer.UID())
	}
	want := []uint32{20, 80, 701}
	got := peer.Groups()
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("groups = %v, want %v", got, want)
		}
	}
}

func TestPeerFromXucredAcceptsZeroAndSixteenGroups(t *testing.T) {
	zero, err := peerFromXucred(xucredSnapshot{Version: 0, UID: 1, NGroups: 0})
	if err != nil {
		t.Fatalf("zero groups: %v", err)
	}
	if len(zero.Groups()) != 0 {
		t.Fatalf("zero groups = %v", zero.Groups())
	}

	snapshot := xucredSnapshot{Version: 0, UID: 2, NGroups: 16}
	for index := range snapshot.Groups {
		snapshot.Groups[index] = uint32(index + 1)
	}
	sixteen, err := peerFromXucred(snapshot)
	if err != nil {
		t.Fatalf("sixteen groups: %v", err)
	}
	if got := sixteen.Groups(); len(got) != 16 || got[0] != 1 || got[15] != 16 {
		t.Fatalf("sixteen groups = %v", got)
	}
}

func TestPeerFromXucredRejectsVersionNegativeAndOversizedGroups(t *testing.T) {
	for _, snapshot := range []xucredSnapshot{
		{Version: 1, UID: 501, NGroups: 0},
		{Version: 0, UID: 501, NGroups: -1},
		{Version: 0, UID: 501, NGroups: 17},
	} {
		if peer, err := peerFromXucred(snapshot); err == nil {
			t.Fatalf("peerFromXucred(%+v) = UID %d groups %v, want error", snapshot, peer.UID(), peer.Groups())
		}
	}
}
