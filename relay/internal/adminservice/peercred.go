package adminservice

import (
	"errors"
	"strconv"

	"mobile-egress/internal/relayadmin"
)

var errInvalidAdminPeerCredentials = errors.New("invalid relay admin peer credentials")

func ParseCanonicalAdminGID(raw string) (uint32, error) {
	parsed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != raw {
		return 0, errInvalidAdminPeerCredentials
	}
	return uint32(parsed), nil
}

type xucredSnapshot struct {
	Version uint32
	UID     uint32
	NGroups int16
	Groups  [16]uint32
}

func peerFromXucred(snapshot xucredSnapshot) (relayadmin.Peer, error) {
	if snapshot.Version != 0 || snapshot.NGroups < 0 || snapshot.NGroups > int16(len(snapshot.Groups)) {
		return relayadmin.Peer{}, errInvalidAdminPeerCredentials
	}
	groups := make([]uint32, int(snapshot.NGroups))
	copy(groups, snapshot.Groups[:int(snapshot.NGroups)])
	return relayadmin.NewPeer(snapshot.UID, groups), nil
}
