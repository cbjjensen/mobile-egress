// Package cloud defines the guarded AWS/SSM boundary used by the desktop
// controller. It deliberately has no EC2 create/terminate or ingress APIs.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	Region                          = "us-east-1"
	MaximumManagedNodes             = 10
	AmazonSSMManagedInstanceCoreARN = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
)

var ErrConfirmationRequired = errors.New("explicit confirmation is required before changing the existing IAM role")

type Instance struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	State            string `json:"state"`
	Platform         string `json:"platform"`
	Architecture     string `json:"architecture"`
	ImageDescription string `json:"imageDescription"`
	ProfileARN       string `json:"profileArn,omitempty"`
	RoleName         string `json:"roleName,omitempty"`
	SSMOnline        bool   `json:"ssmOnline"`
}

type SSMProfileResult struct {
	Changed  bool   `json:"changed"`
	RoleName string `json:"roleName"`
}

type SSMInstanceStatus struct {
	Registered   bool   `json:"registered"`
	Online       bool   `json:"online"`
	PingStatus   string `json:"pingStatus"`
	AgentVersion string `json:"agentVersion,omitempty"`
	LastPingAt   string `json:"lastPingAt,omitempty"`
}

type IAMProvider interface {
	CreateAndAttachDedicatedSSMProfile(context.Context, string) (string, error)
	RoleHasManagedPolicy(context.Context, string, string) (bool, error)
	AttachManagedPolicy(context.Context, string, string) error
}

type Manager struct {
	provider IAMProvider
}

func NewManager(provider IAMProvider) *Manager {
	return &Manager{provider: provider}
}

func FilterSupportedInstances(instances []Instance) []Instance {
	result := make([]Instance, 0, len(instances))
	for _, instance := range instances {
		if strings.EqualFold(instance.State, "running") && strings.EqualFold(instance.Platform, "windows") &&
			strings.EqualFold(instance.Architecture, "x86_64") && strings.Contains(strings.ToLower(instance.ImageDescription), "windows server 2019") {
			result = append(result, instance)
		}
	}
	return result
}

func (manager *Manager) EnsureSSM(ctx context.Context, instance Instance, confirmExistingRoleChange bool) (SSMProfileResult, error) {
	if manager == nil || manager.provider == nil || strings.TrimSpace(instance.ID) == "" {
		return SSMProfileResult{}, errors.New("AWS IAM provider and EC2 instance are required")
	}
	if instance.ProfileARN == "" {
		roleName, err := manager.provider.CreateAndAttachDedicatedSSMProfile(ctx, instance.ID)
		if err != nil {
			return SSMProfileResult{}, fmt.Errorf("create dedicated SSM instance profile: %w", err)
		}
		return SSMProfileResult{Changed: true, RoleName: roleName}, nil
	}
	if strings.TrimSpace(instance.RoleName) == "" {
		return SSMProfileResult{}, errors.New("existing instance profile role could not be resolved; profile was not changed")
	}
	hasPolicy, err := manager.provider.RoleHasManagedPolicy(ctx, instance.RoleName, AmazonSSMManagedInstanceCoreARN)
	if err != nil {
		return SSMProfileResult{}, fmt.Errorf("inspect existing IAM role: %w", err)
	}
	if hasPolicy {
		return SSMProfileResult{RoleName: instance.RoleName}, nil
	}
	result := SSMProfileResult{RoleName: instance.RoleName}
	if !confirmExistingRoleChange {
		return result, ErrConfirmationRequired
	}
	if err := manager.provider.AttachManagedPolicy(ctx, instance.RoleName, AmazonSSMManagedInstanceCoreARN); err != nil {
		return result, fmt.Errorf("add Systems Manager policy to existing IAM role: %w", err)
	}
	result.Changed = true
	return result, nil
}

func (manager *Manager) ValidateSelection(instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return errors.New("select at least one EC2 instance")
	}
	if len(instanceIDs) > MaximumManagedNodes {
		return fmt.Errorf("at most %d EC2 instances can be managed", MaximumManagedNodes)
	}
	seen := make(map[string]struct{}, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		instanceID = strings.TrimSpace(instanceID)
		if instanceID == "" {
			return errors.New("EC2 instance ID is required")
		}
		if _, exists := seen[instanceID]; exists {
			return errors.New("EC2 instance selection contains duplicates")
		}
		seen[instanceID] = struct{}{}
	}
	return nil
}
