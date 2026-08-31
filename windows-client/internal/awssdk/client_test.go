package awssdk

import (
	"net/url"
	"testing"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

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
