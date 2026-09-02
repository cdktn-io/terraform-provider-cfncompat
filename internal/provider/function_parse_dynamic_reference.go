// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ function.Function = ParseDynamicReferenceFunction{}

// parseDynamicReferenceAttributeTypes is the fixed attribute set of the
// object provider::cfncompat::parse_dynamic_reference returns. It is fixed --
// rather than service-shaped -- so the return type is statically known to
// Terraform and to the generated CDK Terrain bindings, whatever the reference
// turns out to name. Segments a given service has no notion of are null.
var parseDynamicReferenceAttributeTypes = map[string]attr.Type{
	"service":       types.StringType,
	"name":          types.StringType,
	"version":       types.StringType,
	"secret_string": types.StringType,
	"json_key":      types.StringType,
	"version_stage": types.StringType,
	"version_id":    types.StringType,
}

// NewParseDynamicReferenceFunction returns a new instance of the
// provider::cfncompat::parse_dynamic_reference function.
func NewParseDynamicReferenceFunction() function.Function {
	return ParseDynamicReferenceFunction{}
}

// ParseDynamicReferenceFunction splits a CloudFormation dynamic reference
// string into its parts, so the parts can be wired into the matching
// cfncompat data source.
//
// It is pure: it resolves nothing. Resolution is the data sources' job, which
// is also the only place an AWS client exists.
type ParseDynamicReferenceFunction struct{}

func (f ParseDynamicReferenceFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "parse_dynamic_reference"
}

func (f ParseDynamicReferenceFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Split a CloudFormation dynamic reference into its parts",
		Description: "Parses a single {{resolve:...}} CloudFormation dynamic reference string into an object with a fixed attribute set (service, name, version, secret_string, json_key, version_stage, version_id), so the parts can be wired into the matching cfncompat data source. Purely syntactic: it resolves nothing.",
		MarkdownDescription: "Parses a single " +
			"[`{{resolve:...}}`](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html) " +
			"CloudFormation dynamic reference into its parts, so they can be wired into the matching " +
			"`cfncompat_ssm_parameter_value`, `cfncompat_ssm_secure_parameter_value` or " +
			"`cfncompat_secretsmanager_secret_value` data source. It is purely syntactic and resolves " +
			"nothing.\n\n" +
			"**This function is for references whose text is not known at synthesis time.** A synthesis " +
			"backend translating aws-cdk-lib constructs sees dynamic references as structured tokens " +
			"(`SecretValue.ssmSecure(name, version)`, `valueForStringParameter(scope, name)`), already split " +
			"into service and key parts, and wires those parts straight onto data source arguments -- no " +
			"function involved. The function earns its place only where the string's shape cannot be known " +
			"in TypeScript: a whole-string token whose value arrives at plan or apply time (a deploy-time " +
			"variable, another resource's attribute, a `CfnParameter` supplied at deploy time), or an escape " +
			"hatch such as `CfnResource.addPropertyOverride` or a `cfn-include` template where the reference " +
			"is assembled from tokens. There, only Go logic operating on the actual plan-time value can pull " +
			"it apart. A literal, hardcoded reference should never go through this function.\n\n" +
			"The returned object always has the same seven attributes; the ones the reference's service has " +
			"no notion of, and the segments it omits, are `null`:\n\n" +
			"| Attribute | `ssm` / `ssm-secure` | `secretsmanager` |\n" +
			"|---|---|---|\n" +
			"| `service` | `\"ssm\"` / `\"ssm-secure\"` | `\"secretsmanager\"` |\n" +
			"| `name` | the parameter name | the secret id (a name, or a full ARN) |\n" +
			"| `version` | the numeric version segment, as a string | `null` |\n" +
			"| `secret_string` | `null` | `\"SecretString\"`, or `null` when the segment is omitted or empty |\n" +
			"| `json_key` | `null` | the `json-key` segment |\n" +
			"| `version_stage` | `null` | the `version-stage` segment |\n" +
			"| `version_id` | `null` | the `version-id` segment |\n\n" +
			"The argument must be **exactly one** whole reference. A string with text around the reference, " +
			"or with more than one reference in it, is an error: splitting such a string and rebuilding it " +
			"with interpolation is the synthesis backend's job, one data source per distinct reference.\n\n" +
			"A `secret-id` that is a full ARN is handled: its seven colon-separated parts are consumed as the " +
			"id, and only what follows is read as positional segments.\n\n" +
			"```terraform\n" +
			"# The reference text is only known at plan time.\n" +
			"locals {\n" +
			"  ref = provider::cfncompat::parse_dynamic_reference(var.image_reference)\n" +
			"}\n\n" +
			"data \"cfncompat_ssm_parameter_value\" \"ami\" {\n" +
			"  name    = local.ref.name\n" +
			"  version = local.ref.version == null ? null : tonumber(local.ref.version)\n" +
			"}\n" +
			"```",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:                "reference",
				MarkdownDescription: "A single CloudFormation dynamic reference, e.g. `{{resolve:ssm:/my/param:3}}`. The whole string must be the reference.",
			},
		},
		Return: function.ObjectReturn{
			AttributeTypes: parseDynamicReferenceAttributeTypes,
		},
	}
}

func (f ParseDynamicReferenceFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var reference string

	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &reference))
	if resp.Error != nil {
		return
	}

	parsed, err := parseDynamicReference(reference)
	if err != nil {
		resp.Error = function.ConcatFuncErrors(resp.Error,
			function.NewArgumentFuncError(0, "parse_dynamic_reference: "+err.Error()))
		return
	}

	object, diags := types.ObjectValue(parseDynamicReferenceAttributeTypes, map[string]attr.Value{
		"service":       types.StringValue(parsed.Service),
		"name":          types.StringValue(parsed.Name),
		"version":       nullableString(parsed.Version),
		"secret_string": nullableString(parsed.SecretString),
		"json_key":      nullableString(parsed.JSONKey),
		"version_stage": nullableString(parsed.VersionStage),
		"version_id":    nullableString(parsed.VersionID),
	})
	if diags.HasError() {
		// Unreachable: the attribute types and the values above are built
		// from the same fixed map.
		resp.Error = function.ConcatFuncErrors(resp.Error,
			function.NewFuncError("parse_dynamic_reference: building the result object: "+diags.Errors()[0].Detail()))
		return
	}

	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, object))
}

// nullableString renders an absent segment as null rather than as an empty
// string, so `== null` is the way to test for a segment the reference omitted.
func nullableString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
