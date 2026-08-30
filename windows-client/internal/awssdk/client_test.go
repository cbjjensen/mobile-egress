package awssdk

import (
	"net/url"
	"testing"

	aws "github.com/aws/aws-sdk-go-v2/aws"
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
