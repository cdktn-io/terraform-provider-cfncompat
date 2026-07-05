// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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
			"provider-defined functions (provider::cfncompat::*), giving CDK Terrain's Terraform/OpenTofu " +
			"synthesis backend faithful CloudFormation compatibility semantics. It requires no configuration.",
		MarkdownDescription: "The `cfncompat` provider exposes AWS CloudFormation intrinsic functions " +
			"(`Fn::Cidr`, `Fn::Join`, `Fn::Select`, `Fn::FindInMap`, condition functions, and more) as Terraform " +
			"provider-defined functions (`provider::cfncompat::*`), giving CDK Terrain's Terraform/OpenTofu " +
			"synthesis backend faithful CloudFormation compatibility semantics. It requires no configuration.\n\n" +
			"See [CDK Terrain](https://github.com/open-constructs/cdk-terrain) for the synthesis backend this provider supports.",
	}
}

func (p *CfncompatProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
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
