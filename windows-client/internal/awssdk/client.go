// Package awssdk is the concrete, us-east-1-only AWS adapter for EC2
// inventory, IAM instance-profile readiness, and SSM Run Command.
package awssdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
	"mobile-egress/windows-client/internal/cloud"
)

type AccessKeyCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type Client struct {
	ec2 *ec2.Client
	iam *iam.Client
	ssm *ssm.Client
}

var dedicatedProfileAttachmentDelays = []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}

func NewAccessKey(ctx context.Context, value AccessKeyCredentials) (*Client, error) {
	if strings.TrimSpace(value.AccessKeyID) == "" || strings.TrimSpace(value.SecretAccessKey) == "" {
		return nil, errors.New("AWS access key ID and secret access key are required")
	}
	provider := credentials.NewStaticCredentialsProvider(value.AccessKeyID, value.SecretAccessKey, value.SessionToken)
	configuration, err := config.LoadDefaultConfig(ctx, config.WithRegion(cloud.Region), config.WithCredentialsProvider(provider))
	if err != nil {
		return nil, errors.New("load AWS access-key configuration")
	}
	return New(configuration), nil
}

func NewProfile(ctx context.Context, profile string) (*Client, error) {
	if strings.TrimSpace(profile) == "" {
		return nil, errors.New("AWS shared configuration profile is required")
	}
	configuration, err := config.LoadDefaultConfig(ctx, config.WithRegion(cloud.Region), config.WithSharedConfigProfile(profile))
	if err != nil {
		return nil, errors.New("load AWS IAM Identity Center profile")
	}
	return New(configuration), nil
}

func New(configuration aws.Config) *Client {
	configuration.Region = cloud.Region
	return &Client{ec2: ec2.NewFromConfig(configuration), iam: iam.NewFromConfig(configuration), ssm: ssm.NewFromConfig(configuration)}
}

func (client *Client) Instances(ctx context.Context) ([]cloud.Instance, error) {
	if client == nil || client.ec2 == nil || client.iam == nil || client.ssm == nil {
		return nil, errors.New("AWS client is unavailable")
	}
	paginator := ec2.NewDescribeInstancesPaginator(client.ec2, &ec2.DescribeInstancesInput{Filters: []ec2types.Filter{
		{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		{Name: aws.String("platform"), Values: []string{"windows"}},
	}})
	rawInstances := make([]ec2types.Instance, 0)
	imageIDs := make(map[string]struct{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, errors.New("list EC2 Windows instances")
		}
		for _, reservation := range page.Reservations {
			for _, instance := range reservation.Instances {
				rawInstances = append(rawInstances, instance)
				if instance.ImageId != nil {
					imageIDs[*instance.ImageId] = struct{}{}
				}
			}
		}
	}
	descriptions, err := client.imageDescriptions(ctx, imageIDs)
	if err != nil {
		return nil, err
	}
	ssmOnline, err := client.ssmOnline(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]cloud.Instance, 0, len(rawInstances))
	for _, instance := range rawInstances {
		item := cloud.Instance{
			ID: aws.ToString(instance.InstanceId), State: string(instance.State.Name), Platform: string(instance.Platform),
			Architecture: string(instance.Architecture), ImageDescription: descriptions[aws.ToString(instance.ImageId)],
			SSMOnline: ssmOnline[aws.ToString(instance.InstanceId)],
		}
		for _, tag := range instance.Tags {
			if aws.ToString(tag.Key) == "Name" {
				item.Name = aws.ToString(tag.Value)
				break
			}
		}
		if instance.IamInstanceProfile != nil {
			item.ProfileARN = aws.ToString(instance.IamInstanceProfile.Arn)
			item.RoleName, _ = client.roleForProfile(ctx, item.ProfileARN)
		}
		result = append(result, item)
	}
	result = cloud.FilterSupportedInstances(result)
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func (client *Client) imageDescriptions(ctx context.Context, ids map[string]struct{}) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	values := make([]string, 0, len(ids))
	for id := range ids {
		values = append(values, id)
	}
	output, err := client.ec2.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: values})
	if err != nil {
		return nil, errors.New("describe EC2 Windows images")
	}
	for _, image := range output.Images {
		result[aws.ToString(image.ImageId)] = aws.ToString(image.Description)
	}
	return result, nil
}

func (client *Client) ssmOnline(ctx context.Context) (map[string]bool, error) {
	result := make(map[string]bool)
	paginator := ssm.NewDescribeInstanceInformationPaginator(client.ssm, &ssm.DescribeInstanceInformationInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, errors.New("list Systems Manager instances")
		}
		for _, info := range page.InstanceInformationList {
			result[aws.ToString(info.InstanceId)] = info.PingStatus == ssmtypes.PingStatusOnline
		}
	}
	return result, nil
}

func selectedInstanceSSMFilters(instanceID string) ([]ssmtypes.InstanceInformationStringFilter, error) {
	if !validInstanceID(instanceID) {
		return nil, errors.New("invalid EC2 instance ID")
	}
	return []ssmtypes.InstanceInformationStringFilter{{Key: aws.String("InstanceIds"), Values: []string{instanceID}}}, nil
}

func (client *Client) InstanceSSMOnline(ctx context.Context, instanceID string) (bool, error) {
	status, err := client.InstanceSSMStatus(ctx, instanceID)
	return status.Online, err
}

func (client *Client) InstanceSSMStatus(ctx context.Context, instanceID string) (cloud.SSMInstanceStatus, error) {
	if client == nil || client.ssm == nil {
		return cloud.SSMInstanceStatus{}, errors.New("AWS client is unavailable")
	}
	filters, err := selectedInstanceSSMFilters(instanceID)
	if err != nil {
		return cloud.SSMInstanceStatus{}, err
	}
	output, err := client.ssm.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{Filters: filters})
	if err != nil {
		return cloud.SSMInstanceStatus{}, errors.New("check selected Systems Manager instance")
	}
	return selectedInstanceSSMStatus(instanceID, output.InstanceInformationList)
}

func selectedInstanceSSMStatus(instanceID string, information []ssmtypes.InstanceInformation) (cloud.SSMInstanceStatus, error) {
	if !validInstanceID(instanceID) {
		return cloud.SSMInstanceStatus{}, errors.New("invalid EC2 instance ID")
	}
	for _, info := range information {
		if aws.ToString(info.InstanceId) != instanceID {
			continue
		}
		status := cloud.SSMInstanceStatus{
			Registered:   true,
			Online:       info.PingStatus == ssmtypes.PingStatusOnline,
			PingStatus:   string(info.PingStatus),
			AgentVersion: aws.ToString(info.AgentVersion),
		}
		if info.LastPingDateTime != nil {
			status.LastPingAt = info.LastPingDateTime.UTC().Format(time.RFC3339Nano)
		}
		return status, nil
	}
	return cloud.SSMInstanceStatus{PingStatus: "NotRegistered"}, nil
}

type rebootInstancesFunc func(context.Context, *ec2.RebootInstancesInput, ...func(*ec2.Options)) (*ec2.RebootInstancesOutput, error)

func (client *Client) RebootInstance(ctx context.Context, instanceID string) error {
	if client == nil || client.ec2 == nil {
		return errors.New("AWS client is unavailable")
	}
	return requestInstanceReboot(ctx, instanceID, client.ec2.RebootInstances)
}

func requestInstanceReboot(ctx context.Context, instanceID string, reboot rebootInstancesFunc) error {
	if !validInstanceID(instanceID) {
		return errors.New("invalid EC2 instance ID")
	}
	if reboot == nil {
		return errors.New("request EC2 instance reboot")
	}
	if _, err := reboot(ctx, &ec2.RebootInstancesInput{InstanceIds: []string{instanceID}}); err != nil {
		return errors.New("request EC2 instance reboot")
	}
	return nil
}

func (client *Client) roleForProfile(ctx context.Context, profileARN string) (string, error) {
	profileName, err := resourceName(profileARN, "instance-profile")
	if err != nil {
		return "", err
	}
	output, err := client.iam.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(profileName)})
	if err != nil || output.InstanceProfile == nil || len(output.InstanceProfile.Roles) != 1 {
		return "", errors.New("resolve IAM instance profile role")
	}
	return aws.ToString(output.InstanceProfile.Roles[0].RoleName), nil
}

func (client *Client) CreateAndAttachDedicatedSSMProfile(ctx context.Context, instanceID string) (string, error) {
	if !validInstanceID(instanceID) {
		return "", errors.New("invalid EC2 instance ID")
	}
	suffix := strings.TrimPrefix(instanceID, "i-")
	roleName := "MobileEgressSSM-" + suffix
	profileName := roleName
	described, err := client.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil || len(described.Reservations) != 1 || len(described.Reservations[0].Instances) != 1 {
		return "", errors.New("verify EC2 instance profile absence")
	}
	profileAlreadyAttached, err := validateAttachedDedicatedProfile(described.Reservations[0].Instances[0].IamInstanceProfile, profileName)
	if err != nil {
		return "", err
	}
	trust := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	tags := dedicatedTags(instanceID)
	if _, err := client.iam.CreateRole(ctx, &iam.CreateRoleInput{RoleName: aws.String(roleName), AssumeRolePolicyDocument: aws.String(trust), Tags: tags}); err != nil && !isAlreadyExists(err) {
		return "", errors.New("create dedicated SSM IAM role")
	}
	roleOutput, err := client.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil || validateDedicatedRoleResource(roleOutput.Role, roleName, instanceID) != nil {
		return "", errors.New("existing dedicated SSM role name is not owned by Mobile Egress")
	}
	if err := client.ensureDedicatedRolePermissionsSafe(ctx, roleName); err != nil {
		return "", err
	}
	if err := client.AttachManagedPolicy(ctx, roleName, cloud.AmazonSSMManagedInstanceCoreARN); err != nil {
		return "", err
	}
	if _, err := client.iam.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{InstanceProfileName: aws.String(profileName), Tags: tags}); err != nil && !isAlreadyExists(err) {
		return "", errors.New("create dedicated SSM instance profile")
	}
	profile, err := client.iam.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(profileName)})
	if err != nil || validateDedicatedProfileResource(profile.InstanceProfile, profileName, instanceID) != nil {
		return "", errors.New("verify dedicated SSM instance profile")
	}
	switch len(profile.InstanceProfile.Roles) {
	case 0:
		if _, err := client.iam.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
			InstanceProfileName: aws.String(profileName), RoleName: aws.String(roleName),
		}); err != nil {
			return "", errors.New("add dedicated role to SSM instance profile")
		}
	case 1:
		if aws.ToString(profile.InstanceProfile.Roles[0].RoleName) != roleName {
			return "", errors.New("dedicated SSM instance profile contains an unexpected role and was not changed")
		}
	default:
		return "", errors.New("dedicated SSM instance profile contains unexpected roles and was not changed")
	}
	if !profileAlreadyAttached {
		err := retryDedicatedProfileAttachment(
			ctx,
			profileName,
			dedicatedProfileAttachmentDelays,
			func(ctx context.Context) (*ec2types.IamInstanceProfile, error) {
				described, err := client.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
				if err != nil || len(described.Reservations) != 1 || len(described.Reservations[0].Instances) != 1 {
					return nil, errors.New("verify EC2 instance profile before attachment")
				}
				return described.Reservations[0].Instances[0].IamInstanceProfile, nil
			},
			func(ctx context.Context) error {
				_, err := client.ec2.AssociateIamInstanceProfile(ctx, &ec2.AssociateIamInstanceProfileInput{
					InstanceId: aws.String(instanceID), IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String(profileName)},
				})
				return err
			},
			waitForAWSPropagation,
		)
		if err != nil {
			return "", err
		}
	}
	return roleName, nil
}

func retryDedicatedProfileAttachment(
	ctx context.Context,
	expectedName string,
	delays []time.Duration,
	describe func(context.Context) (*ec2types.IamInstanceProfile, error),
	associate func(context.Context) error,
	wait func(context.Context, time.Duration) error,
) error {
	if len(delays) == 0 || describe == nil || associate == nil || wait == nil {
		return errors.New("attach dedicated SSM instance profile")
	}
	for attempt, delay := range delays {
		if attempt > 0 {
			if err := wait(ctx, delay); err != nil {
				return errors.New("attach dedicated SSM instance profile: retry interrupted")
			}
		}
		profile, err := describe(ctx)
		if err != nil {
			return err
		}
		attached, err := validateAttachedDedicatedProfile(profile, expectedName)
		if err != nil {
			return err
		}
		if attached {
			return nil
		}
		err = associate(ctx)
		if err == nil {
			return nil
		}
		if isProfileAttachmentPermissionError(err) {
			return errors.New("attach dedicated SSM instance profile: AWS permission denied")
		}
		if !isRetryableProfileAttachmentError(err) {
			return errors.New("attach dedicated SSM instance profile")
		}
	}
	return errors.New("attach dedicated SSM instance profile after waiting for AWS propagation")
}

func waitForAWSPropagation(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableProfileAttachmentError(err error) bool {
	switch apiErrorCode(err) {
	case "ClientInvalidParameterValue", "IncorrectState", "InvalidInstanceID.NotFound", "InvalidParameterValue":
		return true
	default:
		return false
	}
}

func isProfileAttachmentPermissionError(err error) bool {
	switch apiErrorCode(err) {
	case "AccessDenied", "AccessDeniedException", "AuthFailure", "UnauthorizedOperation":
		return true
	default:
		return false
	}
}

func validateAttachedDedicatedProfile(profile *ec2types.IamInstanceProfile, expectedName string) (bool, error) {
	if profile == nil {
		return false, nil
	}
	actualName, err := resourceName(aws.ToString(profile.Arn), "instance-profile")
	if err != nil || actualName != expectedName {
		return false, errors.New("EC2 instance already has an unrelated instance profile; it was not replaced")
	}
	return true, nil
}

func dedicatedTags(instanceID string) []iamtypes.Tag {
	return []iamtypes.Tag{
		{Key: aws.String("MobileEgressManaged"), Value: aws.String("true")},
		{Key: aws.String("MobileEgressInstance"), Value: aws.String(instanceID)},
	}
}

func validateDedicatedRoleResource(role *iamtypes.Role, expectedName, instanceID string) error {
	if role == nil || aws.ToString(role.RoleName) != expectedName || aws.ToString(role.Path) != "/" || !hasDedicatedTags(role.Tags, instanceID) {
		return errors.New("dedicated role identity does not match")
	}
	policyDocument, err := url.QueryUnescape(aws.ToString(role.AssumeRolePolicyDocument))
	if err != nil {
		return errors.New("dedicated role trust policy is invalid")
	}
	var policy struct {
		Version   string `json:"Version"`
		Statement []struct {
			Effect    string `json:"Effect"`
			Action    string `json:"Action"`
			Principal struct {
				Service string `json:"Service"`
			} `json:"Principal"`
		} `json:"Statement"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(policyDocument))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&policy) != nil || decoder.Decode(&struct{}{}) != io.EOF || policy.Version != "2012-10-17" || len(policy.Statement) != 1 {
		return errors.New("dedicated role trust policy is invalid")
	}
	statement := policy.Statement[0]
	if statement.Effect != "Allow" || statement.Action != "sts:AssumeRole" || statement.Principal.Service != "ec2.amazonaws.com" {
		return errors.New("dedicated role trust policy is invalid")
	}
	return nil
}

func validateDedicatedProfileResource(profile *iamtypes.InstanceProfile, expectedName, instanceID string) error {
	if profile == nil || aws.ToString(profile.InstanceProfileName) != expectedName || aws.ToString(profile.Path) != "/" || !hasDedicatedTags(profile.Tags, instanceID) {
		return errors.New("dedicated profile identity does not match")
	}
	if len(profile.Roles) > 1 || (len(profile.Roles) == 1 && aws.ToString(profile.Roles[0].RoleName) != expectedName) {
		return errors.New("dedicated profile contains an unexpected role")
	}
	return nil
}

func hasDedicatedTags(tags []iamtypes.Tag, instanceID string) bool {
	values := make(map[string]string, len(tags))
	for _, tag := range tags {
		values[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return values["MobileEgressManaged"] == "true" && values["MobileEgressInstance"] == instanceID
}

func (client *Client) ensureDedicatedRolePermissionsSafe(ctx context.Context, roleName string) error {
	attached := iam.NewListAttachedRolePoliciesPaginator(client.iam, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(roleName)})
	for attached.HasMorePages() {
		page, err := attached.NextPage(ctx)
		if err != nil {
			return errors.New("inspect dedicated SSM role policies")
		}
		for _, policy := range page.AttachedPolicies {
			if aws.ToString(policy.PolicyArn) != cloud.AmazonSSMManagedInstanceCoreARN {
				return errors.New("dedicated SSM role has unexpected managed policies and was not changed")
			}
		}
	}
	inline := iam.NewListRolePoliciesPaginator(client.iam, &iam.ListRolePoliciesInput{RoleName: aws.String(roleName)})
	for inline.HasMorePages() {
		page, err := inline.NextPage(ctx)
		if err != nil {
			return errors.New("inspect dedicated SSM role inline policies")
		}
		if len(page.PolicyNames) != 0 {
			return errors.New("dedicated SSM role has unexpected inline policies and was not changed")
		}
	}
	return nil
}

func (client *Client) RoleHasManagedPolicy(ctx context.Context, roleName, policyARN string) (bool, error) {
	paginator := iam.NewListAttachedRolePoliciesPaginator(client.iam, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(roleName)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return false, errors.New("list IAM role policies")
		}
		for _, policy := range page.AttachedPolicies {
			if aws.ToString(policy.PolicyArn) == policyARN {
				return true, nil
			}
		}
	}
	return false, nil
}

func (client *Client) AttachManagedPolicy(ctx context.Context, roleName, policyARN string) error {
	if policyARN != cloud.AmazonSSMManagedInstanceCoreARN {
		return errors.New("only AmazonSSMManagedInstanceCore may be attached")
	}
	if _, err := client.iam.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{RoleName: aws.String(roleName), PolicyArn: aws.String(policyARN)}); err != nil {
		return errors.New("attach AmazonSSMManagedInstanceCore policy")
	}
	return nil
}

func (client *Client) RunPowerShell(ctx context.Context, instanceID, script string) (string, error) {
	if !validInstanceID(instanceID) || script == "" || len(script) > 128<<10 {
		return "", errors.New("invalid SSM PowerShell command")
	}
	output, err := client.ssm.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunPowerShellScript"), InstanceIds: []string{instanceID},
		Parameters:     map[string][]string{"commands": {script}, "executionTimeout": {"600"}},
		TimeoutSeconds: aws.Int32(30),
	})
	if err != nil || output.Command == nil || output.Command.CommandId == nil {
		return "", errors.New("send SSM command")
	}
	commandID := aws.ToString(output.Command.CommandId)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		invocation, err := client.ssm.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId: aws.String(commandID), InstanceId: aws.String(instanceID),
		})
		if err == nil {
			switch invocation.Status {
			case ssmtypes.CommandInvocationStatusSuccess:
				return aws.ToString(invocation.StandardOutputContent), nil
			case ssmtypes.CommandInvocationStatusCancelled, ssmtypes.CommandInvocationStatusCancelling,
				ssmtypes.CommandInvocationStatusFailed, ssmtypes.CommandInvocationStatusTimedOut:
				return "", errors.New("SSM command failed")
			}
		} else if !isInvocationPending(err) {
			return "", errors.New("read SSM command status")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func resourceName(rawARN, kind string) (string, error) {
	parsed, err := url.Parse(rawARN)
	if err == nil && parsed.Scheme == "arn" {
		parts := strings.Split(rawARN, ":")
		if len(parts) == 6 {
			resource := parts[5]
			prefix := kind + "/"
			if strings.HasPrefix(resource, prefix) && len(resource) > len(prefix) {
				return strings.TrimPrefix(resource, prefix), nil
			}
		}
	}
	return "", errors.New("invalid IAM resource ARN")
}

func isAlreadyExists(err error) bool { return apiErrorCode(err) == "EntityAlreadyExists" }
func isInvocationPending(err error) bool {
	code := apiErrorCode(err)
	return code == "InvocationDoesNotExist"
}

func apiErrorCode(err error) string {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		return apiError.ErrorCode()
	}
	return ""
}

func validInstanceID(value string) bool {
	if !strings.HasPrefix(value, "i-") || len(value) < 10 || len(value) > 32 {
		return false
	}
	for _, character := range value[2:] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

var _ cloud.IAMProvider = (*Client)(nil)
var _ cloud.CommandRunner = (*Client)(nil)
