// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	awsbase "github.com/hashicorp/aws-sdk-go-base/v2"
	basediag "github.com/hashicorp/aws-sdk-go-base/v2/diag"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure CfncompatProvider satisfies various provider interfaces.
var _ provider.Provider = &CfncompatProvider{}
var _ provider.ProviderWithFunctions = &CfncompatProvider{}
var _ provider.ProviderWithEphemeralResources = &CfncompatProvider{}
var _ provider.ProviderWithActions = &CfncompatProvider{}

// CfncompatProvider defines the provider implementation.
type CfncompatProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

func (p *CfncompatProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "cfncompat"
	resp.Version = p.version
}

func (p *CfncompatProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The cfncompat provider exposes AWS CloudFormation intrinsic functions " +
			"(Fn::Cidr, Fn::Join, Fn::Select, Fn::FindInMap, condition functions, and more) as Terraform " +
			"provider-defined functions (provider::cfncompat::*), and a cfncompat_custom_resource resource " +
			"that faithfully emulates the CloudFormation Custom Resource deployment protocol, giving CDK " +
			"Terrain's Terraform/OpenTofu synthesis backend faithful CloudFormation compatibility semantics. " +
			"All configuration is optional: the provider-defined functions work with no AWS configuration at " +
			"all, only cfncompat_custom_resource requires AWS credentials/region to be resolvable.",
		MarkdownDescription: "The `cfncompat` provider exposes AWS CloudFormation intrinsic functions " +
			"(`Fn::Cidr`, `Fn::Join`, `Fn::Select`, `Fn::FindInMap`, condition functions, and more) as Terraform " +
			"provider-defined functions (`provider::cfncompat::*`), and a `cfncompat_custom_resource` resource " +
			"that faithfully emulates the CloudFormation Custom Resource deployment protocol, giving CDK " +
			"Terrain's Terraform/OpenTofu synthesis backend faithful CloudFormation compatibility semantics.\n\n" +
			"All configuration below is optional: the provider-defined functions work with no AWS configuration " +
			"at all (an empty `provider \"cfncompat\" {}` block is valid), only `cfncompat_custom_resource` " +
			"requires AWS credentials/region to be resolvable, and it uses the same credential-resolution " +
			"chain as the official `hashicorp/aws` provider.\n\n" +
			"See [CDK Terrain](https://github.com/open-constructs/cdk-terrain) for the synthesis backend this provider supports.",
		Attributes: map[string]schema.Attribute{
			"access_key": schema.StringAttribute{
				Optional: true,
				Description: "The AWS access key. Can also be sourced from the `AWS_ACCESS_KEY_ID` " +
					"environment variable, or via a shared credentials file if `profile` is specified.",
			},
			"secret_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "The AWS secret key. Can also be sourced from the `AWS_SECRET_ACCESS_KEY` " +
					"environment variable, or via a shared credentials file if `profile` is specified.",
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Session token for temporary credentials, typically provided after identity " +
					"federation or MFA login. Can also be sourced from the `AWS_SESSION_TOKEN` environment variable.",
			},
			"profile": schema.StringAttribute{
				Optional:    true,
				Description: "The AWS profile name as set in the shared credentials/config files.",
			},
			"region": schema.StringAttribute{
				Optional: true,
				Description: "The AWS region used by cfncompat_custom_resource API calls. Can also be sourced " +
					"from the `AWS_REGION`/`AWS_DEFAULT_REGION` environment variables, a shared config file, or " +
					"the EC2 Instance Metadata Service.",
			},
			"shared_config_files": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Paths to shared config files. If not set, defaults to `~/.aws/config`.",
			},
			"shared_credentials_files": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "Paths to shared credentials files. If not set, defaults to `~/.aws/credentials`.",
			},
			"max_retries": schema.Int64Attribute{
				Optional: true,
				Description: fmt.Sprintf(
					"The maximum number of times an AWS API request is retried on failure. If not set, defaults to %d.",
					defaultMaxRetries,
				),
			},
			"skip_metadata_api_check": schema.BoolAttribute{
				Optional: true,
				Description: "Skip the AWS EC2 Instance Metadata API check. Useful when running somewhere " +
					"without a metadata API endpoint (setting to `true` prevents authenticating via IMDS).",
			},
			"http_proxy": schema.StringAttribute{
				Optional: true,
				Description: "URL of a proxy to use for HTTP requests when accessing the AWS API. Can also be " +
					"set using the `HTTP_PROXY`/`http_proxy` environment variables.",
			},
			"https_proxy": schema.StringAttribute{
				Optional: true,
				Description: "URL of a proxy to use for HTTPS requests when accessing the AWS API. Can also be " +
					"set using the `HTTPS_PROXY`/`https_proxy` environment variables.",
			},
			"no_proxy": schema.StringAttribute{
				Optional: true,
				Description: "Comma-separated list of hosts that should not use HTTP or HTTPS proxies. Can " +
					"also be set using the `NO_PROXY`/`no_proxy` environment variables.",
			},
			"insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Explicitly allow the provider to perform \"insecure\" SSL requests. Defaults to `false`.",
			},
			"custom_resource_bucket": schema.StringAttribute{
				Optional: true,
				Description: "Default S3 bucket used for cfncompat_custom_resource response transport " +
					"(the pre-signed PUT URL the custom resource handler writes its response to) when a " +
					"cfncompat_custom_resource does not set its own `response_bucket`.",
			},
			"assume_role": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Configuration for assuming an IAM role prior to making AWS API calls.",
				Attributes: map[string]schema.Attribute{
					"duration": schema.StringAttribute{
						Optional: true,
						Description: "The duration of the role session, e.g. \"1h\". Parsed with Go's " +
							"time.ParseDuration (valid units: ns, us, ms, s, m, h).",
					},
					"external_id": schema.StringAttribute{
						Optional:    true,
						Description: "External identifier to use when assuming the role.",
					},
					"policy": schema.StringAttribute{
						Optional:    true,
						Description: "IAM policy in JSON format used as a session policy.",
					},
					"policy_arns": schema.ListAttribute{
						ElementType: types.StringType,
						Optional:    true,
						Description: "ARNs of IAM policies used as managed session policies.",
					},
					"role_arn": schema.StringAttribute{
						Required:    true,
						Description: "ARN of the IAM role to assume.",
					},
					"session_name": schema.StringAttribute{
						Optional:    true,
						Description: "Session name to use when assuming the role.",
					},
					"source_identity": schema.StringAttribute{
						Optional:    true,
						Description: "Source identity to use when assuming the role.",
					},
					"tags": schema.MapAttribute{
						ElementType: types.StringType,
						Optional:    true,
						Description: "Map of assume role session tags.",
					},
					"transitive_tag_keys": schema.SetAttribute{
						ElementType: types.StringType,
						Optional:    true,
						Description: "Set of assume role session tag keys to pass to any subsequent sessions.",
					},
				},
			},
			"assume_role_with_web_identity": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Configuration for assuming an IAM role using a web identity token.",
				Attributes: map[string]schema.Attribute{
					"duration": schema.StringAttribute{
						Optional: true,
						Description: "The duration of the role session, e.g. \"1h\". Parsed with Go's " +
							"time.ParseDuration (valid units: ns, us, ms, s, m, h).",
					},
					"policy": schema.StringAttribute{
						Optional:    true,
						Description: "IAM policy in JSON format used as a session policy.",
					},
					"policy_arns": schema.ListAttribute{
						ElementType: types.StringType,
						Optional:    true,
						Description: "ARNs of IAM policies used as managed session policies.",
					},
					"role_arn": schema.StringAttribute{
						Required:    true,
						Description: "ARN of the IAM role to assume.",
					},
					"session_name": schema.StringAttribute{
						Optional:    true,
						Description: "Session name to use when assuming the role.",
					},
					"web_identity_token": schema.StringAttribute{
						Optional: true,
						Description: "Value of a web identity token from an OIDC/OAuth provider. One of " +
							"`web_identity_token` or `web_identity_token_file` is required.",
					},
					"web_identity_token_file": schema.StringAttribute{
						Optional: true,
						Description: "File containing a web identity token from an OIDC/OAuth provider. One of " +
							"`web_identity_token_file` or `web_identity_token` is required.",
					},
				},
			},
			"endpoints": schema.SingleNestedAttribute{
				Optional: true,
				Description: "Service endpoint URL overrides, primarily for testing against LocalStack. " +
					"Only used by cfncompat_custom_resource; the provider-defined functions make no AWS API calls.",
				Attributes: map[string]schema.Attribute{
					"lambda": schema.StringAttribute{
						Optional:    true,
						Description: "Override the default Lambda service endpoint URL.",
					},
					"sns": schema.StringAttribute{
						Optional:    true,
						Description: "Override the default SNS service endpoint URL.",
					},
					"s3": schema.StringAttribute{
						Optional:    true,
						Description: "Override the default S3 service endpoint URL.",
					},
					"sts": schema.StringAttribute{
						Optional:    true,
						Description: "Override the default STS service endpoint URL.",
					},
				},
			},
		},
	}
}

func (p *CfncompatProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var model providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	baseConfig, diags := providerModelToAWSBaseConfig(ctx, model, p.version)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Lenient Configure: the 17 provider-defined functions must keep working
	// even when AWS credentials cannot be resolved at all (e.g. no
	// environment, no shared config, no IMDS). So a credential/config
	// resolution error is captured on ProviderData.ConfigErr instead of
	// being added to resp.Diagnostics. Resources that actually need AWS
	// access (cfncompat_custom_resource) surface it when they are used.
	_, awsConfig, awsDiags := awsbase.GetAwsConfig(ctx, &baseConfig)

	var configErr error
	for _, d := range awsDiags {
		if d.Severity() != basediag.SeverityError {
			continue
		}
		if configErr == nil {
			configErr = fmt.Errorf("%s: %s", d.Summary(), d.Detail())
		}
	}

	providerData := &ProviderData{
		AwsConfig:            awsConfig,
		ConfigErr:            configErr,
		Region:               awsConfig.Region,
		CustomResourceBucket: model.CustomResourceBucket.ValueString(),
		Endpoints:            model.Endpoints.toEndpointsConfig(),
	}

	resp.ResourceData = providerData
	resp.DataSourceData = providerData
}

// Resources returns the resources supported by this provider.
// Currently none; future resources will include:
//   - cfncompat_custom_resource
func (p *CfncompatProvider) Resources(ctx context.Context) []func() resource.Resource {
	return nil
}

// EphemeralResources returns the ephemeral resources supported by this provider.
func (p *CfncompatProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return nil
}

// DataSources returns the data sources supported by this provider.
func (p *CfncompatProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return nil
}

// Functions returns the provider-defined functions, each implementing the
// CloudFormation intrinsic function of the same name.
func (p *CfncompatProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{
		NewBase64Function,
		NewCidrFunction,
		NewConditionAndFunction,
		NewConditionContainsFunction,
		NewConditionEachMemberEqualsFunction,
		NewConditionEachMemberInFunction,
		NewConditionEqualsFunction,
		NewConditionIfFunction,
		NewConditionNotFunction,
		NewConditionOrFunction,
		NewFindInMapFunction,
		NewJoinFunction,
		NewLengthFunction,
		NewSelectFunction,
		NewSplitFunction,
		NewSubFunction,
		NewToJsonStringFunction,
	}
}

// Actions returns the provider actions.
func (p *CfncompatProvider) Actions(ctx context.Context) []func() action.Action {
	return nil
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &CfncompatProvider{
			version: version,
		}
	}
}
