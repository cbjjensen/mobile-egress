package adminservice

import (
	"testing"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

func TestAuthorizationTable(t *testing.T) {
	t.Parallel()

	const (
		adminGID = uint32(80)
		ownerUID = uint32(501)
	)
	root := relayadmin.NewPeer(0, []uint32{0})
	owner := relayadmin.NewPeer(ownerUID, []uint32{20, adminGID})
	otherAdmin := relayadmin.NewPeer(502, []uint32{20, adminGID})
	nonAdmin := relayadmin.NewPeer(503, []uint32{20})

	tests := []struct {
		name     string
		snapshot service.AdminSnapshot
		peer     relayadmin.Peer
		want     map[relayadmin.Operation]bool
	}{
		{
			name:     "safe absent root",
			snapshot: service.AdminSnapshot{Class: service.AdminStateAbsent},
			peer:     root,
			want: map[relayadmin.Operation]bool{
				relayadmin.OperationStatus: true, relayadmin.OperationSetup: false,
				relayadmin.OperationRotate: true, relayadmin.OperationRepair: true,
			},
		},
		{
			name:     "safe absent nonzero administrator",
			snapshot: service.AdminSnapshot{Class: service.AdminStateAbsent},
			peer:     owner,
			want: map[relayadmin.Operation]bool{
				relayadmin.OperationStatus: true, relayadmin.OperationSetup: true,
				relayadmin.OperationRotate: true, relayadmin.OperationRepair: true,
			},
		},
		{
			name:     "safe absent non administrator",
			snapshot: service.AdminSnapshot{Class: service.AdminStateAbsent},
			peer:     nonAdmin,
			want:     denyEveryOperation(),
		},
		{
			name: "ready recorded owner",
			snapshot: service.AdminSnapshot{
				Class: service.AdminStateReady, AdministrativeOwnerUID: ownerUID, OwnerUIDBound: true,
			},
			peer: owner,
			want: allowEveryOperation(),
		},
		{
			name: "ready root recovery",
			snapshot: service.AdminSnapshot{
				Class: service.AdminStateReady, AdministrativeOwnerUID: ownerUID, OwnerUIDBound: true,
			},
			peer: root,
			want: allowEveryOperation(),
		},
		{
			name: "ready other administrator",
			snapshot: service.AdminSnapshot{
				Class: service.AdminStateReady, AdministrativeOwnerUID: ownerUID, OwnerUIDBound: true,
			},
			peer: otherAdmin,
			want: denyEveryOperation(),
		},
		{
			name: "ready non administrator",
			snapshot: service.AdminSnapshot{
				Class: service.AdminStateReady, AdministrativeOwnerUID: ownerUID, OwnerUIDBound: true,
			},
			peer: nonAdmin,
			want: denyEveryOperation(),
		},
		{
			name: "incompatible readable owner",
			snapshot: service.AdminSnapshot{
				Class: service.AdminStateIncompatible, AdministrativeOwnerUID: ownerUID, OwnerUIDBound: true,
			},
			peer: owner,
			want: map[relayadmin.Operation]bool{
				relayadmin.OperationStatus: true, relayadmin.OperationSetup: false,
				relayadmin.OperationRotate: false, relayadmin.OperationRepair: true,
			},
		},
		{
			name: "incompatible readable root",
			snapshot: service.AdminSnapshot{
				Class: service.AdminStateIncompatible, AdministrativeOwnerUID: ownerUID, OwnerUIDBound: true,
			},
			peer: root,
			want: map[relayadmin.Operation]bool{
				relayadmin.OperationStatus: true, relayadmin.OperationSetup: false,
				relayadmin.OperationRotate: false, relayadmin.OperationRepair: true,
			},
		},
		{
			name: "incompatible readable other administrator",
			snapshot: service.AdminSnapshot{
				Class: service.AdminStateIncompatible, AdministrativeOwnerUID: ownerUID, OwnerUIDBound: true,
			},
			peer: otherAdmin,
			want: denyEveryOperation(),
		},
		{
			name:     "unsafe binding root",
			snapshot: service.AdminSnapshot{Class: service.AdminStateIncompatible},
			peer:     root,
			want: map[relayadmin.Operation]bool{
				relayadmin.OperationStatus: true, relayadmin.OperationSetup: false,
				relayadmin.OperationRotate: false, relayadmin.OperationRepair: true,
			},
		},
		{
			name:     "unsafe binding administrator",
			snapshot: service.AdminSnapshot{Class: service.AdminStateIncompatible},
			peer:     owner,
			want:     denyEveryOperation(),
		},
	}

	operations := []relayadmin.Operation{
		relayadmin.OperationStatus,
		relayadmin.OperationSetup,
		relayadmin.OperationRotate,
		relayadmin.OperationRepair,
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, operation := range operations {
				if got := authorizeSnapshot(test.snapshot, test.peer, operation, adminGID); got != test.want[operation] {
					t.Fatalf("authorizeSnapshot(%q) = %t, want %t", operation, got, test.want[operation])
				}
			}
		})
	}
}

func TestAuthorizationUsesCopiedPeerGroups(t *testing.T) {
	t.Parallel()

	groups := []uint32{20, 80}
	peer := relayadmin.NewPeer(501, groups)
	groups[1] = 999
	returned := peer.Groups()
	returned[1] = 999

	if !authorizeSnapshot(
		service.AdminSnapshot{Class: service.AdminStateAbsent},
		peer,
		relayadmin.OperationSetup,
		80,
	) {
		t.Fatal("administrator membership changed after caller mutated group slices")
	}
}

func allowEveryOperation() map[relayadmin.Operation]bool {
	return map[relayadmin.Operation]bool{
		relayadmin.OperationStatus: true,
		relayadmin.OperationSetup:  true,
		relayadmin.OperationRotate: true,
		relayadmin.OperationRepair: true,
	}
}

func denyEveryOperation() map[relayadmin.Operation]bool {
	return map[relayadmin.Operation]bool{
		relayadmin.OperationStatus: false,
		relayadmin.OperationSetup:  false,
		relayadmin.OperationRotate: false,
		relayadmin.OperationRepair: false,
	}
}
