// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	awsbase "github.com/hashicorp/aws-sdk-go-base/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// defaultMaxRetries is applied when max_retries is not set in the provider
// configuration.
const defaultMaxRetries = 25

// providerModel is the Terraform configuration model for the cfncompat
// provider block itself (see CfncompatProvider.Schema).
type providerModel struct {
	AccessKey              types.String `tfsdk:"access_key"`
	SecretKey              types.String `tfsdk:"secret_key"`
	Token                  types.String `tfsdk:"token"`
	Profile                types.String `tfsdk:"profile"`
	Region                 types.String `tfsdk:"region"`
	SharedConfigFiles      types.List   `tfsdk:"shared_config_files"`
	SharedCredentialsFiles types.List   `tfsdk:"shared_credentials_files"`
	MaxRetries             types.Int64  `tfsdk:"max_retries"`
	SkipMetadataAPICheck   types.Bool   `tfsdk:"skip_metadata_api_check"`
	HTTPProxy              types.String `tfsdk:"http_proxy"`
	HTTPSProxy             types.String `tfsdk:"https_proxy"`
	NoProxy                types.String `tfsdk:"no_proxy"`
	Insecure               types.Bool   `tfsdk:"insecure"`
	CustomResourceBucket   types.String `tfsdk:"custom_resource_bucket"`

	AssumeRole                *providerAssumeRoleModel                `tfsdk:"assume_role"`
	AssumeRoleWithWebIdentity *providerAssumeRoleWithWebIdentityModel `tfsdk:"assume_role_with_web_identity"`
	Endpoints                 *providerEndpointsModel                 `tfsdk:"endpoints"`
}

// providerAssumeRoleModel is the `assume_role` provider configuration block.
type providerAssumeRoleModel struct {
	Duration          types.String `tfsdk:"duration"`
	ExternalID        types.String `tfsdk:"external_id"`
	Policy            types.String `tfsdk:"policy"`
	PolicyARNs        types.List   `tfsdk:"policy_arns"`
	RoleARN           types.String `tfsdk:"role_arn"`
	SessionName       types.String `tfsdk:"session_name"`
	SourceIdentity    types.String `tfsdk:"source_identity"`
	Tags              types.Map    `tfsdk:"tags"`
	TransitiveTagKeys types.Set    `tfsdk:"transitive_tag_keys"`
}

// providerAssumeRoleWithWebIdentityModel is the
// `assume_role_with_web_identity` provider configuration block.
type providerAssumeRoleWithWebIdentityModel struct {
	Duration             types.String `tfsdk:"duration"`
	Policy               types.String `tfsdk:"policy"`
	PolicyARNs           types.List   `tfsdk:"policy_arns"`
	RoleARN              types.String `tfsdk:"role_arn"`
	SessionName          types.String `tfsdk:"session_name"`
	WebIdentityToken     types.String `tfsdk:"web_identity_token"`
	WebIdentityTokenFile types.String `tfsdk:"web_identity_token_file"`
}

// providerEndpointsModel is the `endpoints` provider configuration block,
// used to override service endpoint URLs (e.g. for LocalStack testing).
type providerEndpointsModel struct {
	Lambda types.String `tfsdk:"lambda"`
	SNS    types.String `tfsdk:"sns"`
	S3     types.String `tfsdk:"s3"`
	STS    types.String `tfsdk:"sts"`
}

// EndpointsConfig holds resolved, optional per-service endpoint URL
// overrides. A zero value means no overrides were configured, and resources
// should use the default AWS endpoints for the resolved region.
type EndpointsConfig struct {
	Lambda string
	SNS    string
	S3     string
	STS    string
}

// toEndpointsConfig converts the (possibly nil) endpoints block into an
// EndpointsConfig value. A nil receiver yields the zero value.
func (m *providerEndpointsModel) toEndpointsConfig() EndpointsConfig {
	if m == nil {
		return EndpointsConfig{}
	}
	return EndpointsConfig{
		Lambda: m.Lambda.ValueString(),
		SNS:    m.SNS.ValueString(),
		S3:     m.S3.ValueString(),
		STS:    m.STS.ValueString(),
	}
}

// ProviderData is passed to resources via resp.ResourceData and
// resp.DataSourceData in their Configure methods. Provider-defined functions
// never receive it (they are configuration-free by design).
type ProviderData struct {
	// AwsConfig is the resolved aws-sdk-go-v2 configuration. It is usable
	// even when ConfigErr is non-nil (e.g. region may still be set), but
	// resources should treat it as untrustworthy for API calls in that case.
	AwsConfig aws.Config
	// ConfigErr is non-nil when credential/config resolution failed.
	// Configure() intentionally does not fail in this case: the 17
	// provider-defined functions do not need AWS credentials, so an
	// unconfigured or misconfigured provider must not break them. Resources
	// that DO need AWS access must check ConfigErr themselves and surface it
	// at that point.
	ConfigErr error
	// Region is the resolved AWS region, may be empty.
	Region string
	// CustomResourceBucket is the default S3 bucket for custom-resource
	// response transport; may be empty (resources fall back accordingly).
	CustomResourceBucket string
	// Endpoints holds lambda/sns/s3/sts endpoint URL overrides; may be zero.
	Endpoints EndpointsConfig
}

// providerModelToAWSBaseConfig maps the Terraform provider configuration
// model onto an aws-sdk-go-base/v2 Config. It is a pure function (no network
// or filesystem I/O) so it can be unit tested without AWS credentials,
// network access, or ambient environment configuration.
//
// version is the provider version, embedded in the AWS API User-Agent.
func providerModelToAWSBaseConfig(ctx context.Context, model providerModel, version string) (awsbase.Config, diag.Diagnostics) {
	var diags diag.Diagnostics

	cfg := awsbase.Config{
		AccessKey:              model.AccessKey.ValueString(),
		SecretKey:              model.SecretKey.ValueString(),
		Token:                  model.Token.ValueString(),
		Profile:                model.Profile.ValueString(),
		Region:                 model.Region.ValueString(),
		Insecure:               model.Insecure.ValueBool(),
		NoProxy:                model.NoProxy.ValueString(),
		HTTPProxyMode:          awsbase.HTTPProxyModeLegacy,
		CallerName:             "Terraform CFN-Compat Provider",
		CallerDocumentationURL: "https://registry.terraform.io/providers/cdktn-io/cfncompat",
		// Provider Configure must stay cheap and must never fail because
		// credentials could not be validated: the 17 provider-defined
		// functions must keep working with no AWS credentials configured at
		// all. Skipping credential validation avoids an STS
		// GetCallerIdentity call (and the associated network dependency and
		// failure mode) during Configure; resources that need AWS access
		// still get a fully resolved aws.Config to call with.
		SkipCredsValidation: true,
		APNInfo: &awsbase.APNInfo{
			PartnerName: "cdktn-io",
			Products: []awsbase.UserAgentProduct{
				{Name: "terraform-provider-cfncompat", Version: version, Comment: "+https://registry.terraform.io/providers/cdktn-io/cfncompat"},
			},
		},
	}

	if model.MaxRetries.IsNull() {
		cfg.MaxRetries = defaultMaxRetries
	} else {
		cfg.MaxRetries = int(model.MaxRetries.ValueInt64())
	}

	if !model.HTTPProxy.IsNull() {
		v := model.HTTPProxy.ValueString()
		cfg.HTTPProxy = &v
	}
	if !model.HTTPSProxy.IsNull() {
		v := model.HTTPSProxy.ValueString()
		cfg.HTTPSProxy = &v
	}

	switch {
	case model.SkipMetadataAPICheck.IsNull():
		cfg.EC2MetadataServiceEnableState = imds.ClientDefaultEnableState
	case model.SkipMetadataAPICheck.ValueBool():
		cfg.EC2MetadataServiceEnableState = imds.ClientDisabled
	default:
		cfg.EC2MetadataServiceEnableState = imds.ClientEnabled
	}

	if !model.SharedConfigFiles.IsNull() {
		files, d := providerConfigStringSlice(ctx, model.SharedConfigFiles)
		diags.Append(d...)
		cfg.SharedConfigFiles = files
	}
	if !model.SharedCredentialsFiles.IsNull() {
		files, d := providerConfigStringSlice(ctx, model.SharedCredentialsFiles)
		diags.Append(d...)
		cfg.SharedCredentialsFiles = files
	}

	if model.AssumeRole != nil {
		ar, d := model.AssumeRole.awsBaseAssumeRole(ctx)
		diags.Append(d...)
		cfg.AssumeRole = []awsbase.AssumeRole{ar}
	}

	if model.AssumeRoleWithWebIdentity != nil {
		ar, d := model.AssumeRoleWithWebIdentity.awsBaseAssumeRoleWithWebIdentity(ctx)
		diags.Append(d...)
		cfg.AssumeRoleWithWebIdentity = ar
	}

	if model.Endpoints != nil && !model.Endpoints.STS.IsNull() {
		cfg.StsEndpoint = model.Endpoints.STS.ValueString()
	}

	return cfg, diags
}

// awsBaseAssumeRole maps the assume_role provider configuration block onto
// an awsbase.AssumeRole.
func (m *providerAssumeRoleModel) awsBaseAssumeRole(ctx context.Context) (awsbase.AssumeRole, diag.Diagnostics) {
	var diags diag.Diagnostics

	ar := awsbase.AssumeRole{
		RoleARN:        m.RoleARN.ValueString(),
		ExternalID:     m.ExternalID.ValueString(),
		Policy:         m.Policy.ValueString(),
		SessionName:    m.SessionName.ValueString(),
		SourceIdentity: m.SourceIdentity.ValueString(),
	}

	if !m.Duration.IsNull() && m.Duration.ValueString() != "" {
		d, err := time.ParseDuration(m.Duration.ValueString())
		if err != nil {
			diags.AddError(
				"Invalid assume_role.duration",
				fmt.Sprintf("could not parse %q as a duration: %s", m.Duration.ValueString(), err),
			)
		} else {
			ar.Duration = d
		}
	}

	if !m.PolicyARNs.IsNull() {
		arns, d := providerConfigStringSlice(ctx, m.PolicyARNs)
		diags.Append(d...)
		ar.PolicyARNs = arns
	}
	if !m.Tags.IsNull() {
		tags, d := providerConfigStringMap(ctx, m.Tags)
		diags.Append(d...)
		ar.Tags = tags
	}
	if !m.TransitiveTagKeys.IsNull() {
		keys, d := providerConfigStringSet(ctx, m.TransitiveTagKeys)
		diags.Append(d...)
		ar.TransitiveTagKeys = keys
	}

	return ar, diags
}

// awsBaseAssumeRoleWithWebIdentity maps the assume_role_with_web_identity
// provider configuration block onto an awsbase.AssumeRoleWithWebIdentity.
func (m *providerAssumeRoleWithWebIdentityModel) awsBaseAssumeRoleWithWebIdentity(ctx context.Context) (*awsbase.AssumeRoleWithWebIdentity, diag.Diagnostics) {
	var diags diag.Diagnostics

	ar := &awsbase.AssumeRoleWithWebIdentity{
		RoleARN:              m.RoleARN.ValueString(),
		Policy:               m.Policy.ValueString(),
		SessionName:          m.SessionName.ValueString(),
		WebIdentityToken:     m.WebIdentityToken.ValueString(),
		WebIdentityTokenFile: m.WebIdentityTokenFile.ValueString(),
	}

	if !m.Duration.IsNull() && m.Duration.ValueString() != "" {
		d, err := time.ParseDuration(m.Duration.ValueString())
		if err != nil {
			diags.AddError(
				"Invalid assume_role_with_web_identity.duration",
				fmt.Sprintf("could not parse %q as a duration: %s", m.Duration.ValueString(), err),
			)
		} else {
			ar.Duration = d
		}
	}

	if !m.PolicyARNs.IsNull() {
		arns, d := providerConfigStringSlice(ctx, m.PolicyARNs)
		diags.Append(d...)
		ar.PolicyARNs = arns
	}

	return ar, diags
}

// providerConfigStringSlice converts a types.List of strings into a []string.
func providerConfigStringSlice(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	var out []string
	diags := list.ElementsAs(ctx, &out, false)
	return out, diags
}

// providerConfigStringSet converts a types.Set of strings into a []string.
func providerConfigStringSet(ctx context.Context, set types.Set) ([]string, diag.Diagnostics) {
	var out []string
	diags := set.ElementsAs(ctx, &out, false)
	return out, diags
}

// providerConfigStringMap converts a types.Map of strings into a
// map[string]string.
func providerConfigStringMap(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	var out map[string]string
	diags := m.ElementsAs(ctx, &out, false)
	return out, diags
}
