package awssdk

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	oidctypes "github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
	"mobile-egress/windows-client/internal/cloud"
)

const deviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"

var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z0-9-]+-[0-9]$`)

type DeviceAuthorization struct {
	VerificationURL string    `json:"verificationUrl"`
	UserCode        string    `json:"userCode"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type IdentityCenterAccount struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type IdentityCenterLogin struct {
	oidc         *ssooidc.Client
	sso          *sso.Client
	clientID     string
	clientSecret string
	deviceCode   string
	interval     time.Duration
	expiresAt    time.Time
	public       DeviceAuthorization
}

type IdentityCenterSession struct {
	client      *sso.Client
	accessToken string
	expiresAt   time.Time
}

func BeginIdentityCenterLogin(ctx context.Context, startURL, ssoRegion string) (*IdentityCenterLogin, error) {
	if err := validateIdentityCenterInputs(startURL, ssoRegion); err != nil {
		return nil, err
	}
	configuration, err := config.LoadDefaultConfig(ctx, config.WithRegion(ssoRegion), config.WithCredentialsProvider(aws.AnonymousCredentials{}))
	if err != nil {
		return nil, errors.New("initialize IAM Identity Center login")
	}
	oidcClient := ssooidc.NewFromConfig(configuration)
	registered, err := oidcClient.RegisterClient(ctx, &ssooidc.RegisterClientInput{
		ClientName: aws.String("Mobile Egress"), ClientType: aws.String("public"),
		GrantTypes: []string{deviceCodeGrant, "refresh_token"}, Scopes: []string{"sso:account:access"},
	})
	if err != nil || aws.ToString(registered.ClientId) == "" || aws.ToString(registered.ClientSecret) == "" {
		return nil, errors.New("register IAM Identity Center desktop client")
	}
	started, err := oidcClient.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId: registered.ClientId, ClientSecret: registered.ClientSecret, StartUrl: aws.String(startURL),
	})
	if err != nil || aws.ToString(started.DeviceCode) == "" || aws.ToString(started.VerificationUriComplete) == "" {
		return nil, errors.New("start IAM Identity Center browser authorization")
	}
	interval := time.Duration(started.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	expiresAt := time.Now().UTC().Add(time.Duration(started.ExpiresIn) * time.Second)
	return &IdentityCenterLogin{
		oidc: oidcClient, sso: sso.NewFromConfig(configuration), clientID: aws.ToString(registered.ClientId),
		clientSecret: aws.ToString(registered.ClientSecret), deviceCode: aws.ToString(started.DeviceCode),
		interval: interval, expiresAt: expiresAt,
		public: DeviceAuthorization{
			VerificationURL: aws.ToString(started.VerificationUriComplete), UserCode: aws.ToString(started.UserCode), ExpiresAt: expiresAt,
		},
	}, nil
}

func (login *IdentityCenterLogin) Authorization() DeviceAuthorization {
	if login == nil {
		return DeviceAuthorization{}
	}
	return login.public
}

func (login *IdentityCenterLogin) Complete(ctx context.Context) (*IdentityCenterSession, error) {
	if login == nil || login.oidc == nil || login.sso == nil || time.Now().UTC().After(login.expiresAt) {
		return nil, errors.New("IAM Identity Center browser authorization expired")
	}
	interval := login.interval
	for {
		output, err := login.oidc.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId: aws.String(login.clientID), ClientSecret: aws.String(login.clientSecret),
			DeviceCode: aws.String(login.deviceCode), GrantType: aws.String(deviceCodeGrant),
		})
		if err == nil && aws.ToString(output.AccessToken) != "" {
			return &IdentityCenterSession{
				client: login.sso, accessToken: aws.ToString(output.AccessToken),
				expiresAt: time.Now().UTC().Add(time.Duration(output.ExpiresIn) * time.Second),
			}, nil
		}
		var pending *oidctypes.AuthorizationPendingException
		var slowDown *oidctypes.SlowDownException
		switch {
		case errors.As(err, &pending):
		case errors.As(err, &slowDown):
			interval += 5 * time.Second
		default:
			return nil, errors.New("complete IAM Identity Center browser authorization")
		}
		if time.Now().UTC().Add(interval).After(login.expiresAt) {
			return nil, errors.New("IAM Identity Center browser authorization expired")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (session *IdentityCenterSession) Accounts(ctx context.Context) ([]IdentityCenterAccount, error) {
	if err := session.valid(); err != nil {
		return nil, err
	}
	result := make([]IdentityCenterAccount, 0)
	var nextToken *string
	for {
		output, err := session.client.ListAccounts(ctx, &sso.ListAccountsInput{
			AccessToken: aws.String(session.accessToken), NextToken: nextToken,
		})
		if err != nil {
			return nil, errors.New("list IAM Identity Center accounts")
		}
		for _, account := range output.AccountList {
			result = append(result, IdentityCenterAccount{
				ID: aws.ToString(account.AccountId), Name: aws.ToString(account.AccountName), Email: aws.ToString(account.EmailAddress),
			})
		}
		nextToken = output.NextToken
		if nextToken == nil || aws.ToString(nextToken) == "" {
			break
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func (session *IdentityCenterSession) Roles(ctx context.Context, accountID string) ([]string, error) {
	if err := session.valid(); err != nil {
		return nil, err
	}
	if !validAccountID(accountID) {
		return nil, errors.New("invalid AWS account ID")
	}
	result := make([]string, 0)
	var nextToken *string
	for {
		output, err := session.client.ListAccountRoles(ctx, &sso.ListAccountRolesInput{
			AccessToken: aws.String(session.accessToken), AccountId: aws.String(accountID), NextToken: nextToken,
		})
		if err != nil {
			return nil, errors.New("list IAM Identity Center roles")
		}
		for _, role := range output.RoleList {
			if name := aws.ToString(role.RoleName); name != "" {
				result = append(result, name)
			}
		}
		nextToken = output.NextToken
		if nextToken == nil || aws.ToString(nextToken) == "" {
			break
		}
	}
	sort.Strings(result)
	return result, nil
}

func (session *IdentityCenterSession) Client(ctx context.Context, accountID, roleName string) (*Client, error) {
	if err := session.valid(); err != nil {
		return nil, err
	}
	if !validAccountID(accountID) || strings.TrimSpace(roleName) == "" || strings.ContainsAny(roleName, "\r\n") {
		return nil, errors.New("invalid IAM Identity Center account or role")
	}
	output, err := session.client.GetRoleCredentials(ctx, &sso.GetRoleCredentialsInput{
		AccessToken: aws.String(session.accessToken), AccountId: aws.String(accountID), RoleName: aws.String(roleName),
	})
	if err != nil || output.RoleCredentials == nil || aws.ToString(output.RoleCredentials.AccessKeyId) == "" || aws.ToString(output.RoleCredentials.SecretAccessKey) == "" {
		return nil, errors.New("retrieve IAM Identity Center role credentials")
	}
	provider := credentials.NewStaticCredentialsProvider(
		aws.ToString(output.RoleCredentials.AccessKeyId), aws.ToString(output.RoleCredentials.SecretAccessKey), aws.ToString(output.RoleCredentials.SessionToken),
	)
	configuration, err := config.LoadDefaultConfig(ctx, config.WithRegion(cloud.Region), config.WithCredentialsProvider(provider))
	if err != nil {
		return nil, errors.New("initialize AWS client from IAM Identity Center role")
	}
	return New(configuration), nil
}

func (session *IdentityCenterSession) valid() error {
	if session == nil || session.client == nil || session.accessToken == "" || time.Now().UTC().After(session.expiresAt) {
		return errors.New("IAM Identity Center session is unavailable or expired")
	}
	return nil
}

func validateIdentityCenterInputs(startURL, region string) error {
	parsed, err := url.Parse(strings.TrimSpace(startURL))
	if err != nil || parsed.Scheme != "https" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".awsapps.com") || parsed.Path != "/start" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("IAM Identity Center start URL must be an HTTPS awsapps.com /start URL")
	}
	if !regionPattern.MatchString(strings.TrimSpace(region)) {
		return errors.New("IAM Identity Center region is invalid")
	}
	return nil
}

func validAccountID(value string) bool {
	if len(value) != 12 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
