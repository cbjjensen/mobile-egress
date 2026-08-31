package awssdk

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

func TestSelectedInstanceSSMFilterTargetsOnlyTheRequestedInstance(t *testing.T) {
	t.Parallel()

	filters, err := selectedInstanceSSMFilters("i-0123456789abcdef0")
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 1 || aws.ToString(filters[0].Key) != "InstanceIds" || len(filters[0].Values) != 1 || filters[0].Values[0] != "i-0123456789abcdef0" {
		t.Fatalf("selected SSM filters = %#v", filters)
	}
	if _, err := selectedInstanceSSMFilters("not-an-instance"); err == nil {
		t.Fatal("selectedInstanceSSMFilters accepted an invalid instance ID")
	}
	var _ []ssmtypes.InstanceInformationStringFilter = filters
}

func TestSelectedInstanceSSMStatusDistinguishesMissingAndCurrentRegistration(t *testing.T) {
	t.Parallel()

	missing, err := selectedInstanceSSMStatus("i-0123456789abcdef0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Registered || missing.Online || missing.PingStatus != "NotRegistered" {
		t.Fatalf("missing SSM status = %#v, want an explicit unregistered state", missing)
	}

	lastPing := time.Date(2026, time.August, 31, 18, 2, 3, 0, time.UTC)
	current, err := selectedInstanceSSMStatus("i-0123456789abcdef0", []ssmtypes.InstanceInformation{
		{InstanceId: aws.String("i-0ffffffffffffffff"), PingStatus: ssmtypes.PingStatusOnline},
		{
			InstanceId:       aws.String("i-0123456789abcdef0"),
			PingStatus:       ssmtypes.PingStatusOnline,
			AgentVersion:     aws.String("3.3.3050.0"),
			LastPingDateTime: &lastPing,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !current.Registered || !current.Online || current.PingStatus != "Online" || current.AgentVersion != "3.3.3050.0" || current.LastPingAt != "2026-08-31T18:02:03Z" {
		t.Fatalf("current SSM status = %#v, want selected-instance diagnostics", current)
	}
}

func TestSSMCommandFailureStageExtractsOnlyApprovedSanitizedMarkers(t *testing.T) {
	t.Parallel()

	stderr := "Client release operation failed [MOBILE_EGRESS_STAGE=pretrust-signature]\r\nAt C:\\ProgramData\\Amazon\\SSM\\script.ps1:1 char:1"
	if got, want := ssmCommandFailureStage(stderr), "pretrust-signature"; got != want {
		t.Fatalf("ssmCommandFailureStage() = %q, want %q", got, want)
	}
	for _, unsafe := range []string{
		"Client release operation failed [MOBILE_EGRESS_STAGE=private-output-marker]",
		"private-output-marker",
		"Client release operation failed [MOBILE_EGRESS_STAGE=pretrust-signature;secret]",
	} {
		if got := ssmCommandFailureStage(unsafe); got != "" {
			t.Fatalf("ssmCommandFailureStage(%q) = %q, want no unapproved detail", unsafe, got)
		}
	}
}

func TestDedicatedIAMResourceValidationRejectsNameCollisions(t *testing.T) {
	t.Parallel()

	const instanceID = "i-0123456789abcdef0"
	const resourceName = "MobileEgressSSM-0123456789abcdef0"
	trust := url.QueryEscape(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)
	tags := []iamtypes.Tag{
		{Key: aws.String("MobileEgressManaged"), Value: aws.String("true")},
		{Key: aws.String("MobileEgressInstance"), Value: aws.String(instanceID)},
	}
	role := &iamtypes.Role{RoleName: aws.String(resourceName), Path: aws.String("/"), AssumeRolePolicyDocument: aws.String(trust), Tags: tags}
	profile := &iamtypes.InstanceProfile{InstanceProfileName: aws.String(resourceName), Path: aws.String("/"), Tags: tags}
	if err := validateDedicatedRoleResource(role, resourceName, instanceID); err != nil {
		t.Fatalf("valid role rejected: %v", err)
	}
	if err := validateDedicatedProfileResource(profile, resourceName, instanceID); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}

	wrongTrust := *role
	wrongTrust.AssumeRolePolicyDocument = aws.String(url.QueryEscape(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"sts:AssumeRole"}]}`))
	if err := validateDedicatedRoleResource(&wrongTrust, resourceName, instanceID); err == nil {
		t.Fatal("dedicated role validation accepted an unrelated trust policy")
	}
	untagged := *profile
	untagged.Tags = nil
	if err := validateDedicatedProfileResource(&untagged, resourceName, instanceID); err == nil {
		t.Fatal("dedicated profile validation accepted an untagged name collision")
	}
	wrongRole := *profile
	wrongRole.Roles = []iamtypes.Role{{RoleName: aws.String("UnrelatedRole")}}
	if err := validateDedicatedProfileResource(&wrongRole, resourceName, instanceID); err == nil {
		t.Fatal("dedicated profile validation accepted an unrelated role")
	}
}

func TestDedicatedProfileAttachmentAllowsOnlyTheExpectedIdempotentRetry(t *testing.T) {
	t.Parallel()

	const instanceID = "i-0123456789abcdef0"
	const expectedName = "MobileEgressSSM-0123456789abcdef0"
	expected := &ec2types.IamInstanceProfile{Arn: aws.String("arn:aws:iam::123456789012:instance-profile/" + expectedName)}
	attached, err := validateAttachedDedicatedProfile(expected, expectedName)
	if err != nil || !attached {
		t.Fatalf("expected dedicated attachment = %v/%v, want attached", attached, err)
	}

	unrelated := &ec2types.IamInstanceProfile{Arn: aws.String("arn:aws:iam::123456789012:instance-profile/UnrelatedProfile")}
	if _, err := validateAttachedDedicatedProfile(unrelated, expectedName); err == nil {
		t.Fatal("unrelated attached profile was accepted as an idempotent retry")
	}

	attached, err = validateAttachedDedicatedProfile(nil, expectedName)
	if err != nil || attached {
		t.Fatalf("absent attachment = %v/%v, want not attached", attached, err)
	}
}

func TestDedicatedProfileAttachmentRetriesPropagationErrorsWithBackoff(t *testing.T) {
	t.Parallel()

	const expectedName = "MobileEgressSSM-0123456789abcdef0"
	associationAttempts := 0
	descriptions := 0
	waits := make([]time.Duration, 0)
	err := retryDedicatedProfileAttachment(
		context.Background(),
		expectedName,
		[]time.Duration{0, 2 * time.Second, 4 * time.Second},
		func(context.Context) (*ec2types.IamInstanceProfile, error) {
			descriptions++
			return nil, nil
		},
		func(context.Context) error {
			associationAttempts++
			if associationAttempts < 3 {
				return &smithy.GenericAPIError{Code: "InvalidParameterValue", Message: "profile has not propagated"}
			}
			return nil
		},
		func(_ context.Context, duration time.Duration) error {
			waits = append(waits, duration)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if associationAttempts != 3 || descriptions != 3 {
		t.Fatalf("attempts/descriptions = %d/%d, want 3/3", associationAttempts, descriptions)
	}
	if len(waits) != 2 || waits[0] != 2*time.Second || waits[1] != 4*time.Second {
		t.Fatalf("waits = %v, want [2s 4s]", waits)
	}
}

func TestDedicatedProfileAttachmentDoesNotRetryPermissionErrors(t *testing.T) {
	t.Parallel()

	associationAttempts := 0
	waits := 0
	err := retryDedicatedProfileAttachment(
		context.Background(),
		"MobileEgressSSM-0123456789abcdef0",
		[]time.Duration{0, 2 * time.Second, 4 * time.Second},
		func(context.Context) (*ec2types.IamInstanceProfile, error) { return nil, nil },
		func(context.Context) error {
			associationAttempts++
			return &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "denied"}
		},
		func(context.Context, time.Duration) error {
			waits++
			return nil
		},
	)
	if err == nil || err.Error() != "attach dedicated SSM instance profile: AWS permission denied" {
		t.Fatalf("retry error = %v, want safe permission classification", err)
	}
	if associationAttempts != 1 || waits != 0 {
		t.Fatalf("attempts/waits = %d/%d, want 1/0", associationAttempts, waits)
	}
}

func TestDedicatedProfileAttachmentStopsIfAnotherProfileAppears(t *testing.T) {
	t.Parallel()

	const expectedName = "MobileEgressSSM-0123456789abcdef0"
	descriptions := 0
	associationAttempts := 0
	err := retryDedicatedProfileAttachment(
		context.Background(),
		expectedName,
		[]time.Duration{0, 2 * time.Second, 4 * time.Second},
		func(context.Context) (*ec2types.IamInstanceProfile, error) {
			descriptions++
			if descriptions == 1 {
				return nil, nil
			}
			return &ec2types.IamInstanceProfile{Arn: aws.String("arn:aws:iam::123456789012:instance-profile/UnrelatedProfile")}, nil
		},
		func(context.Context) error {
			associationAttempts++
			return &smithy.GenericAPIError{Code: "IncorrectState", Message: "pending"}
		},
		func(context.Context, time.Duration) error { return nil },
	)
	if err == nil || err.Error() != "EC2 instance already has an unrelated instance profile; it was not replaced" {
		t.Fatalf("retry error = %v, want unrelated-profile guard", err)
	}
	if associationAttempts != 1 || descriptions != 2 {
		t.Fatalf("attempts/descriptions = %d/%d, want 1/2", associationAttempts, descriptions)
	}
}

func TestRequestInstanceRebootTargetsOnlyTheSelectedInstance(t *testing.T) {
	t.Parallel()

	var requested []string
	err := requestInstanceReboot(
		context.Background(),
		"i-0123456789abcdef0",
		func(_ context.Context, input *ec2.RebootInstancesInput, _ ...func(*ec2.Options)) (*ec2.RebootInstancesOutput, error) {
			requested = append([]string(nil), input.InstanceIds...)
			return &ec2.RebootInstancesOutput{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 || requested[0] != "i-0123456789abcdef0" {
		t.Fatalf("reboot targets = %v, want only the selected instance", requested)
	}
}

func TestRequestInstanceRebootRejectsInvalidIDBeforeAWS(t *testing.T) {
	t.Parallel()

	calls := 0
	err := requestInstanceReboot(
		context.Background(),
		"not-an-instance",
		func(context.Context, *ec2.RebootInstancesInput, ...func(*ec2.Options)) (*ec2.RebootInstancesOutput, error) {
			calls++
			return &ec2.RebootInstancesOutput{}, nil
		},
	)
	if err == nil || err.Error() != "invalid EC2 instance ID" {
		t.Fatalf("reboot error = %v, want invalid instance ID", err)
	}
	if calls != 0 {
		t.Fatalf("AWS reboot calls = %d, want 0", calls)
	}
}

func TestRequestInstanceRebootRedactsAWSFailure(t *testing.T) {
	t.Parallel()

	err := requestInstanceReboot(
		context.Background(),
		"i-0123456789abcdef0",
		func(context.Context, *ec2.RebootInstancesInput, ...func(*ec2.Options)) (*ec2.RebootInstancesOutput, error) {
			return nil, errors.New("provider detail that must not reach the UI")
		},
	)
	if err == nil || err.Error() != "request EC2 instance reboot" {
		t.Fatalf("reboot error = %v, want redacted provider failure", err)
	}
}
