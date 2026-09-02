package cloud

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestFilterSupportedInstancesRequiresRunningWindowsServer2019AMD64(t *testing.T) {
	t.Parallel()

	instances := []Instance{
		{ID: "i-good", State: "running", Platform: "windows", Architecture: "x86_64", ImageDescription: "Microsoft Windows Server 2019 Base"},
		{ID: "i-private-ami", State: "running", Platform: "windows", Architecture: "x86_64"},
		{ID: "i-stopped", State: "stopped", Platform: "windows", Architecture: "x86_64", ImageDescription: "Microsoft Windows Server 2019 Base"},
		{ID: "i-linux", State: "running", Platform: "linux", Architecture: "x86_64", ImageDescription: "Amazon Linux"},
		{ID: "i-2022", State: "running", Platform: "windows", Architecture: "x86_64", ImageDescription: "Microsoft Windows Server 2022 Base"},
		{ID: "i-arm", State: "running", Platform: "windows", Architecture: "arm64", ImageDescription: "Microsoft Windows Server 2019 Base"},
	}
	if got := FilterSupportedInstances(instances); !reflect.DeepEqual(got, []Instance{instances[0], instances[1]}) {
		t.Fatalf("FilterSupportedInstances() = %#v", got)
	}
}

func TestEnsureSSMCreatesDedicatedProfileOnlyWhenAbsent(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{}
	manager := NewManager(provider)
	result, err := manager.EnsureSSM(context.Background(), Instance{ID: "i-no-profile"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || provider.createdFor != "i-no-profile" || provider.attachedPolicyRole != "" || provider.replacedProfile {
		t.Fatalf("EnsureSSM() result/provider = %#v/%#v", result, provider)
	}
}

func TestEnsureSSMExistingRoleRequiresExplicitConfirmationAndNeverReplacesProfile(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{roleHasSSM: false}
	manager := NewManager(provider)
	instance := Instance{ID: "i-existing", ProfileARN: "arn:aws:iam::123:instance-profile/Existing", RoleName: "ExistingRole"}
	result, err := manager.EnsureSSM(context.Background(), instance, false)
	if !errors.Is(err, ErrConfirmationRequired) || result.RoleName != "ExistingRole" {
		t.Fatalf("unconfirmed EnsureSSM() = %#v/%v", result, err)
	}
	if provider.attachedPolicyRole != "" || provider.createdFor != "" || provider.replacedProfile {
		t.Fatalf("unconfirmed EnsureSSM() mutated IAM: %#v", provider)
	}

	result, err = manager.EnsureSSM(context.Background(), instance, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || provider.attachedPolicyRole != "ExistingRole" || provider.attachedPolicyARN != AmazonSSMManagedInstanceCoreARN || provider.replacedProfile {
		t.Fatalf("confirmed EnsureSSM() = %#v/%#v", result, provider)
	}
}

func TestEnsureSSMLeavesCompliantExistingRoleUnchanged(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{roleHasSSM: true}
	manager := NewManager(provider)
	result, err := manager.EnsureSSM(context.Background(), Instance{
		ID: "i-ready", ProfileARN: "arn:aws:iam::123:instance-profile/Ready", RoleName: "ReadyRole",
	}, false)
	if err != nil || result.Changed || provider.attachedPolicyRole != "" || provider.createdFor != "" {
		t.Fatalf("compliant EnsureSSM() = %#v/%#v/%v", result, provider, err)
	}
}

func TestManagerEnforcesTenNodeLimit(t *testing.T) {
	t.Parallel()

	manager := NewManager(&fakeProvider{})
	if err := manager.ValidateSelection(instanceIDs(10)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateSelection(instanceIDs(11)); err == nil {
		t.Fatal("ValidateSelection() accepted more than ten nodes")
	}
}

func instanceIDs(count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = fmt.Sprintf("i-%d", index)
	}
	return result
}

type fakeProvider struct {
	roleHasSSM         bool
	createdFor         string
	attachedPolicyRole string
	attachedPolicyARN  string
	replacedProfile    bool
}

func (provider *fakeProvider) CreateAndAttachDedicatedSSMProfile(_ context.Context, instanceID string) (string, error) {
	provider.createdFor = instanceID
	return "MobileEgressSSMRole", nil
}

func (provider *fakeProvider) RoleHasManagedPolicy(context.Context, string, string) (bool, error) {
	return provider.roleHasSSM, nil
}

func (provider *fakeProvider) AttachManagedPolicy(_ context.Context, roleName, policyARN string) error {
	provider.attachedPolicyRole = roleName
	provider.attachedPolicyARN = policyARN
	return nil
}
