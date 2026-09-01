package adminservice

import (
	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

func authorizeSnapshot(snapshot service.AdminSnapshot, peer relayadmin.Peer, operation relayadmin.Operation, adminGID uint32) bool {
	if !operation.Valid() {
		return false
	}
	uid := peer.UID()
	switch snapshot.Class {
	case service.AdminStateAbsent:
		if uid == 0 {
			return operation != relayadmin.OperationSetup
		}
		if !peerInGroup(peer, adminGID) {
			return false
		}
		return true
	case service.AdminStateReady:
		if !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID == 0 {
			return uid == 0 && (operation == relayadmin.OperationStatus || operation == relayadmin.OperationRepair)
		}
		return uid == 0 || uid == snapshot.AdministrativeOwnerUID
	case service.AdminStateIncompatible:
		authority := uid == 0 || snapshot.OwnerUIDBound && snapshot.AdministrativeOwnerUID != 0 && uid == snapshot.AdministrativeOwnerUID
		return authority && (operation == relayadmin.OperationStatus || operation == relayadmin.OperationRepair)
	default:
		return false
	}
}

func peerInGroup(peer relayadmin.Peer, group uint32) bool {
	for _, candidate := range peer.Groups() {
		if candidate == group {
			return true
		}
	}
	return false
}
