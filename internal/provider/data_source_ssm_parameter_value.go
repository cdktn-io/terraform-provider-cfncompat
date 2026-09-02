// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure SsmParameterValueDataSource satisfies the framework data source
// interfaces.
var _ datasource.DataSource = &SsmParameterValueDataSource{}
var _ datasource.DataSourceWithConfigure = &SsmParameterValueDataSource{}
var _ datasource.DataSourceWithValidateConfig = &SsmParameterValueDataSource{}

// NewSsmParameterValueDataSource returns a new instance of the
// cfncompat_ssm_parameter_value data source.
func NewSsmParameterValueDataSource() datasource.DataSource {
	return &SsmParameterValueDataSource{}
}

// SsmParameterValueDataSource resolves a scalar Systems Manager Parameter
// Store value the way CloudFormation does for
// `AWS::SSM::Parameter::Value<String>` (and the AWS-specific inner types) and
// for a whole-value `{{resolve:ssm:...}}` dynamic reference.
//
// See:
//   - https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/cloudformation-supplied-parameter-types.html
//   - https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm.html
type SsmParameterValueDataSource struct {
	providerData *ProviderData
	// clients is the SSM client plus the EC2/Route 53 clients the
	// AWS-specific value-type existence checks use. It is a field so unit
	// tests can substitute fakes.
	clients ssmDataSourceClients
}

// SsmParameterValueDataSourceModel is the Terraform data model for
// cfncompat_ssm_parameter_value.
type SsmParameterValueDataSourceModel struct {
	Name           types.String `tfsdk:"name"`
	Version        types.Int64  `tfsdk:"version"`
	Label          types.String `tfsdk:"label"`
	ValueType      types.String `tfsdk:"value_type"`
	AllowedPattern types.String `tfsdk:"allowed_pattern"`
	AllowedValues  types.List   `tfsdk:"allowed_values"`
	Validate       types.Bool   `tfsdk:"validate"`

	Value            types.String `tfsdk:"value"`
	Arn              types.String `tfsdk:"arn"`
	Type             types.String `tfsdk:"type"`
	DataType         types.String `tfsdk:"data_type"`
	ResolvedVersion  types.Int64  `tfsdk:"resolved_version"`
	LastModifiedDate types.String `tfsdk:"last_modified_date"`
	Id               types.String `tfsdk:"id"`
}

func (d *SsmParameterValueDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssm_parameter_value"
}

func (d *SsmParameterValueDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	valueTypes := sortedValueTypeNames(cfnScalarValueTypes)

	resp.Schema = schema.Schema{
		Description: "Resolves a Systems Manager Parameter Store parameter as a string, the way CloudFormation " +
			"resolves a whole-value {{resolve:ssm:...}} dynamic reference (value_type unset) or an " +
			"AWS::SSM::Parameter::Value<...> template parameter type (value_type set). The resolved value is " +
			"NOT marked sensitive; use cfncompat_ssm_secure_parameter_value for a SecureString parameter.",
		MarkdownDescription: "Resolves a Systems Manager Parameter Store parameter as a string. It has two " +
			"modes, because CloudFormation has two resolution paths with materially different rules " +
			"(verified live -- see [`RFCs/dynamic-ssm/live-test-results.md`](https://github.com/cdktn-io/terraform-provider-cfncompat/blob/main/RFCs/dynamic-ssm/live-test-results.md)):\n\n" +
			"* **`value_type` unset -- dynamic-reference semantics**, i.e. a whole-value " +
			"[`{{resolve:ssm:name[:version]}}`](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm.html). " +
			"A `String` **or** a `StringList` parameter is accepted, and `value` is the stored string exactly " +
			"as Systems Manager returns it -- for a `StringList` that is the comma-joined value, **untrimmed** " +
			"(T6e).\n" +
			"* **`value_type` set -- typed template-parameter semantics**, i.e. " +
			"[`AWS::SSM::Parameter::Value<T>`](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/cloudformation-supplied-parameter-types.html). " +
			"The parameter's Systems Manager type must be `String`; a `StringList` is rejected the way " +
			"CloudFormation rejects it (T4d), because CloudFormation compares the *declared* type against the " +
			"template's shape and ignores the content. The resolved value is then validated against the inner " +
			"type (`AWS::EC2::Image::Id` and friends).\n\n" +
			"A `SecureString` fails the read in both modes, with CloudFormation's own message for that mode; " +
			"read it through `cfncompat_ssm_secure_parameter_value`. A `StringList` you want as a real list " +
			"belongs in `cfncompat_ssm_parameter_list_value`.\n\n" +
			"`value` is **not** marked sensitive, matching CloudFormation, which returns the resolved value of " +
			"a Systems Manager parameter type in `DescribeStacks` (`Parameter.ResolvedValue`). This is a " +
			"deliberate divergence from `hashicorp/aws`'s `aws_ssm_parameter`, which marks `value` sensitive " +
			"unconditionally.\n\n" +
			"Terraform re-reads a data source on **every plan**, which is *faithful* here: live testing showed " +
			"that CloudFormation re-resolves both SSM-typed template parameters (T5, even on a " +
			"`UsePreviousValue=true` no-op update) and `{{resolve:ssm:...}}` references (T6, even when the " +
			"template text is byte-identical) on every stack operation. Set `version` (or `label`) to pin, and " +
			"read `resolved_version` to see what was actually resolved.\n\n" +
			"Requires the `ssm:GetParameter` permission, plus the permission of the `value_type`'s existence " +
			"check when `validate` is left at its default of `true`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`name`"},
				},
				Description: "The Systems Manager parameter name, or its full ARN. An ARN is required for a " +
					"parameter shared from another AWS account. Names are case-sensitive.",
				MarkdownDescription: "The Systems Manager parameter name (for example " +
					"`/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64`), or its full ARN. " +
					"A parameter shared from another AWS account must be given as its full ARN -- which " +
					"CloudFormation supports for the `AWS::SSM::Parameter::Value<...>` parameter type but not " +
					"for `{{resolve:ssm:...}}` dynamic references, so this data source is a superset there. " +
					"Names are case-sensitive.",
			},
			"version": schema.Int64Attribute{
				Optional: true,
				Validators: []validator.Int64{
					int64AtLeastValidator{argument: "`version`", min: 1},
				},
				Description: "Pin the read to a specific parameter version. Mutually exclusive with `label`. " +
					"Unset means the latest version at read time.",
				MarkdownDescription: "Pin the read to a specific parameter version, as the `version` segment of " +
					"`{{resolve:ssm:name:version}}` does -- and, verified live (T1), as a `name:version` value " +
					"behind an `AWS::SSM::Parameter::Value<...>` parameter does too, including in its " +
					"`Default`. Mutually exclusive with `label`. Unset means the latest version **at read " +
					"time**, which Terraform repeats on every plan.",
			},
			"label": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`label`"},
				},
				Description: "Pin the read to a parameter label. Mutually exclusive with `version`. " +
					"CloudFormation does not support labels in dynamic references; this data source does.",
				MarkdownDescription: "Pin the read to a [parameter label](https://docs.aws.amazon.com/systems-manager/latest/userguide/sysman-paramstore-labels.html). " +
					"Mutually exclusive with `version`.\n\n" +
					"~> This is a **cfncompat extension, not a polyfill**: CloudFormation supports labels " +
					"nowhere. A label in a dynamic reference is rejected with *\"Incorrect format is used in the " +
					"following SSM reference\"* (T6d), and a `name:label` value behind an " +
					"`AWS::SSM::Parameter::Value<...>` parameter produces an opaque `InternalFailure` (T1c). " +
					"It exists here because `GetParameter` supports it; a synthesis backend translating " +
					"CloudFormation input must never emit it.",
			},
			"value_type": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringOneOfValidator{argument: "`value_type`", allowed: valueTypes},
				},
				Description: "The CloudFormation inner type of AWS::SSM::Parameter::Value<...>. Leave it " +
					"unset for {{resolve:ssm:...}} dynamic-reference semantics (String or StringList accepted, " +
					"raw untrimmed value). Setting it selects typed template-parameter semantics: the " +
					"parameter's Systems Manager type must be String, and the resolved value is validated " +
					"against the inner type. Valid values: " + strings.Join(valueTypes, ", ") + ".",
				MarkdownDescription: "The CloudFormation **inner type** of " +
					"`AWS::SSM::Parameter::Value<...>`. It has **no default**, because unset and set are two " +
					"different CloudFormation resolution paths:\n\n" +
					"* **Unset** -- dynamic-reference semantics. A `String` or a `StringList` parameter is " +
					"accepted and `value` is the raw stored string, untrimmed. This is the mode a synthesis " +
					"backend uses for a `{{resolve:ssm:...}}` reference.\n" +
					"* **Set** -- typed template-parameter semantics. The parameter's Systems Manager type must " +
					"be `String`; a `StringList` fails with CloudFormation's own *\"Types for SSM parameters " +
					"[...] defined in CFN template and SSM are incompatible\"*. This is the mode a backend uses " +
					"for a `CfnParameter` whose type is `AWS::SSM::Parameter::Value<T>`, with `T` here.\n\n" +
					"`String` applies no value validation (CloudFormation does not validate a plain `String` " +
					"either); it only switches on the strict type check. Every other value is one of " +
					"CloudFormation's AWS-specific parameter types, checked both syntactically and -- unless " +
					"`validate = false` -- for existence in this account and region, as CloudFormation checks " +
					"them (T3). Valid values:\n\n" +
					markdownList(valueTypes),
			},
			"validate": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Whether an AWS-specific value_type also checks that the resolved value exists " +
					"in this account and region, as CloudFormation does. Defaults to true. Setting it to false " +
					"keeps only the syntactic check and makes no extra API call.",
				MarkdownDescription: "Whether an AWS-specific `value_type` also checks that the resolved value " +
					"**exists** in this account and region -- the check CloudFormation performs when it " +
					"validates a typed parameter. Defaults to `true`.\n\n" +
					"Each check costs one extra API call per plan and needs its own IAM permission (for " +
					"example `ec2:DescribeImages` for `AWS::EC2::Image::Id`). `validate = false` keeps only the " +
					"always-on syntactic check and makes no extra call. It has no effect when `value_type` is " +
					"`String`, which has no existence check.",
			},
			"allowed_pattern": schema.StringAttribute{
				Optional: true,
				Description: "A regular expression the resolved value must match in full. A cfncompat " +
					"extension, not a CloudFormation polyfill: CloudFormation's AllowedPattern on an SSM-typed " +
					"Parameter validates the parameter NAME, not the resolved value.",
				MarkdownDescription: "A regular expression the resolved value must match **in full** (the " +
					"expression is anchored at both ends).\n\n" +
					"~> This is a **cfncompat extension, not a polyfill.** Live testing (T2) showed that " +
					"`AllowedPattern` on an `AWS::SSM::Parameter::Value<...>` template `Parameter` validates " +
					"the raw parameter **name** you pass in, never the resolved value: a pattern of " +
					"`^hello-.*$` against a parameter whose value is `hello-v2` is *rejected*, while " +
					"`^/cfncompat.*$` -- which matches the name -- is accepted. CloudFormation therefore has no " +
					"custom-regex validation of resolved SSM values at all. A synthesis backend must **not** " +
					"map a `CfnParameter`'s `AllowedPattern` onto this argument; there it constrains the " +
					"literal name, which the backend holds at synth time and can check itself.",
			},
			"allowed_values": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "The exact set of values the resolved value may take. A cfncompat extension, not " +
					"a CloudFormation polyfill -- see allowed_pattern.",
				MarkdownDescription: "The exact set of values the resolved value may take.\n\n" +
					"~> A **cfncompat extension, not a polyfill**, for the same reason as `allowed_pattern`: " +
					"CloudFormation's `AllowedValues` on an SSM-typed `Parameter` is checked against the " +
					"parameter name, not the resolved value (T2c).",
			},

			"value": schema.StringAttribute{
				Computed: true,
				Description: "The resolved parameter value. Not marked sensitive: CloudFormation exposes the " +
					"resolved value of a Systems Manager parameter type in DescribeStacks.",
				MarkdownDescription: "The resolved parameter value -- what `Ref` yields for an " +
					"`AWS::SSM::Parameter::Value<...>` parameter, and what a whole-value `{{resolve:ssm:...}}` " +
					"reference expands to. For a `StringList` read in dynamic-reference mode it is the " +
					"comma-joined string exactly as stored, **untrimmed** (T6e) -- the typed `List<...>` path, " +
					"which does trim, lives in `cfncompat_ssm_parameter_list_value`.\n\n" +
					"**Not** marked sensitive, deliberately: CloudFormation returns this value in " +
					"`DescribeStacks` as `Parameter.ResolvedValue`, and marking it sensitive would poison every " +
					"downstream attribute it flows into. A `SecureString` never reaches this attribute -- the " +
					"read fails instead.",
			},
			"arn": schema.StringAttribute{
				Computed:            true,
				Description:         "The ARN of the resolved parameter.",
				MarkdownDescription: "The ARN of the resolved parameter.",
			},
			"type": schema.StringAttribute{
				Computed: true,
				Description: "The Systems Manager parameter type that was read: \"String\", or \"StringList\" " +
					"when value_type is unset. A SecureString always fails the read.",
				MarkdownDescription: "The Systems Manager parameter type that was read: `String`, or " +
					"`StringList` when `value_type` is unset (dynamic-reference mode accepts both). A " +
					"`SecureString` always fails the read, with a diagnostic naming " +
					"`cfncompat_ssm_secure_parameter_value`.",
			},
			"data_type": schema.StringAttribute{
				Computed: true,
				Description: "The Systems Manager data type of the parameter: \"text\" or \"aws:ec2:image\". " +
					"Independent of value_type.",
				MarkdownDescription: "The Systems Manager **data type** of the parameter: `text` or " +
					"`aws:ec2:image`. This is Systems Manager's own AMI-id validation, and is independent of " +
					"`value_type`: the two can disagree, exactly as they can in CloudFormation.",
			},
			"resolved_version": schema.Int64Attribute{
				Computed: true,
				Description: "The parameter version that was actually read. Pin `version` to this value to " +
					"stop later plans from picking up a newer version.",
				MarkdownDescription: "The parameter version that was actually read. With no `version`/`label` " +
					"pin, this is the latest version at read time; feeding it back into `version` reproduces " +
					"CloudFormation's pinned-reference behaviour.",
			},
			"last_modified_date": schema.StringAttribute{
				Computed:            true,
				Description:         "When the resolved parameter version was last modified, as an RFC 3339 timestamp in UTC.",
				MarkdownDescription: "When the resolved parameter version was last modified, as an RFC 3339 timestamp in UTC.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Identifier for this data source: the resolved parameter ARN.",
				MarkdownDescription: "Identifier for this data source: the resolved parameter ARN.",
			},
		},
	}
}

func (d *SsmParameterValueDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	validateVersionLabelExclusive(ctx, req.Config, &resp.Diagnostics)
}

func (d *SsmParameterValueDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, ok := configuredProviderData(req, resp)
	d.providerData = pd
	if !ok {
		return
	}
	d.clients = newSSMDataSourceClients(pd)
}

func (d *SsmParameterValueDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model SsmParameterValueDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !awsDataSourceReady(d.providerData, "cfncompat_ssm_parameter_value", "read a Systems Manager parameter", &resp.Diagnostics) {
		return
	}

	// value_type has no default: unset and set are two different
	// CloudFormation resolution paths (see the schema, and
	// RFCs/dynamic-ssm/live-test-results.md).
	typed := !model.ValueType.IsNull() && !model.ValueType.IsUnknown()

	var spec cfnValueTypeSpec
	if typed {
		valueType := model.ValueType.ValueString()
		var ok bool
		spec, ok = cfnScalarValueTypes[valueType]
		if !ok {
			resp.Diagnostics.AddError(
				"Invalid value_type",
				fmt.Sprintf("%q is not a CloudFormation-supplied parameter type valid for a scalar value. Valid values are: %s.",
					valueType, strings.Join(sortedValueTypeNames(cfnScalarValueTypes), ", ")),
			)
			return
		}
	}

	doValidate := ssmValidateFlag(model.Validate)

	constraints, err := cfnParameterConstraintsFrom(ctx, model.AllowedPattern, model.AllowedValues)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Parameter Constraints", err.Error())
		return
	}

	selector := ssmParameterSelector(model.Name.ValueString(), model.Version, model.Label)
	parameter, err := getSSMParameter(ctx, d.clients.SSM, selector)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Read Systems Manager Parameter", err.Error())
		return
	}

	// The accepted Systems Manager types differ per mode, and so do
	// CloudFormation's own rejection messages.
	switch {
	case parameter.Type == ssmTypeSecureString && typed:
		resp.Diagnostics.AddError(
			"SecureString Parameter Read Through a Non-Sensitive Data Source",
			errSSMSecureAsTemplateParameter(parameter.Name).Error(),
		)
		return
	case parameter.Type == ssmTypeSecureString:
		resp.Diagnostics.AddError(
			"SecureString Parameter Read Through a Non-Sensitive Data Source",
			errSSMSecureThroughPlainSSM(parameter.Name).Error(),
		)
		return
	case parameter.Type == ssmTypeStringList && typed:
		resp.Diagnostics.AddError(
			"Incompatible Systems Manager Parameter Type",
			errSSMTypeIncompatible(parameter.Name, ssmTypeStringList, "a scalar type", model.ValueType.ValueString()).Error()+
				". Read it through the cfncompat_ssm_parameter_list_value data source, whose `values` "+
				"attribute is a real list(string) -- or leave `value_type` unset for "+
				"{{resolve:ssm:...}} dynamic-reference semantics, which do accept a StringList and "+
				"return its raw comma-joined value",
		)
		return
	case parameter.Type == ssmTypeString, parameter.Type == ssmTypeStringList:
		// String is accepted in both modes; StringList only in
		// dynamic-reference mode, where the raw stored string is the value.
	default:
		resp.Diagnostics.AddError(
			"Unexpected Systems Manager Parameter Type",
			unexpectedSSMTypeDetail(parameter.Name, parameter.Type),
		)
		return
	}

	values := []string{parameter.Value}

	if typed {
		if err := validateCfnValueType(ctx, spec, values, doValidate, d.clients.Validator); err != nil {
			resp.Diagnostics.AddError(
				fmt.Sprintf("Resolved Value Is Not a Valid %s", spec.Name),
				fmt.Sprintf("the Systems Manager parameter %q was resolved, but %s", parameter.Name, err.Error()),
			)
			return
		}
	}
	if err := constraints.validate(values); err != nil {
		resp.Diagnostics.AddError(
			"Resolved Value Violates a Parameter Constraint",
			fmt.Sprintf("the Systems Manager parameter %q was resolved, but %s", parameter.Name, err.Error()),
		)
		return
	}

	model.Validate = types.BoolValue(doValidate)
	model.Value = types.StringValue(parameter.Value)
	model.Arn = types.StringValue(parameter.ARN)
	model.Type = types.StringValue(parameter.Type)
	model.DataType = types.StringValue(parameter.DataType)
	model.ResolvedVersion = types.Int64Value(parameter.Version)
	model.LastModifiedDate = types.StringValue(parameter.LastModifiedDate)
	model.Id = types.StringValue(ssmDataSourceID(parameter))

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
