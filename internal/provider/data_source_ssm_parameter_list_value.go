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

// Ensure SsmParameterListValueDataSource satisfies the framework data source
// interfaces.
var _ datasource.DataSource = &SsmParameterListValueDataSource{}
var _ datasource.DataSourceWithConfigure = &SsmParameterListValueDataSource{}
var _ datasource.DataSourceWithValidateConfig = &SsmParameterListValueDataSource{}

// NewSsmParameterListValueDataSource returns a new instance of the
// cfncompat_ssm_parameter_list_value data source.
func NewSsmParameterListValueDataSource() datasource.DataSource {
	return &SsmParameterListValueDataSource{}
}

// SsmParameterListValueDataSource resolves a Systems Manager Parameter Store
// value as a list of strings, the way CloudFormation resolves
// `AWS::SSM::Parameter::Value<List<String>>`,
// `AWS::SSM::Parameter::Value<CommaDelimitedList>` and their AWS-specific
// `List<...>` forms.
type SsmParameterListValueDataSource struct {
	providerData *ProviderData
	clients      ssmDataSourceClients
}

// SsmParameterListValueDataSourceModel is the Terraform data model for
// cfncompat_ssm_parameter_list_value.
type SsmParameterListValueDataSourceModel struct {
	Name           types.String `tfsdk:"name"`
	Version        types.Int64  `tfsdk:"version"`
	Label          types.String `tfsdk:"label"`
	ValueType      types.String `tfsdk:"value_type"`
	AllowedPattern types.String `tfsdk:"allowed_pattern"`
	AllowedValues  types.List   `tfsdk:"allowed_values"`
	Validate       types.Bool   `tfsdk:"validate"`

	Values           types.List   `tfsdk:"values"`
	RawValue         types.String `tfsdk:"raw_value"`
	Arn              types.String `tfsdk:"arn"`
	Type             types.String `tfsdk:"type"`
	ResolvedVersion  types.Int64  `tfsdk:"resolved_version"`
	LastModifiedDate types.String `tfsdk:"last_modified_date"`
	Id               types.String `tfsdk:"id"`
}

func (d *SsmParameterListValueDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssm_parameter_list_value"
}

func (d *SsmParameterListValueDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	valueTypes := sortedValueTypeNames(cfnListValueTypes)

	resp.Schema = schema.Schema{
		Description: "Resolves a StringList Systems Manager Parameter Store parameter as a list(string), the " +
			"way CloudFormation resolves AWS::SSM::Parameter::Value<List<String>>, " +
			"AWS::SSM::Parameter::Value<CommaDelimitedList> and their AWS-specific List<...> forms: the stored " +
			"value is split on commas and each member string is whitespace-trimmed. Only StringList " +
			"parameters are accepted, exactly as CloudFormation accepts only StringList behind a list-shaped " +
			"parameter type.",
		MarkdownDescription: "Resolves a Systems Manager Parameter Store parameter as a real " +
			"`list(string)`, the way CloudFormation resolves " +
			"`AWS::SSM::Parameter::Value<List<String>>`, `AWS::SSM::Parameter::Value<CommaDelimitedList>` and " +
			"their AWS-specific `List<...>` forms.\n\n" +
			"Systems Manager always returns a `StringList` as a single comma-joined string; this data source " +
			"splits it on `,` and trims the surrounding whitespace of each member. The trimming is verified " +
			"CloudFormation behaviour for this path: a `StringList` holding `\"a,b, c ,d\"` resolves through a " +
			"`List<String>` parameter type as `a,b,c,d` (T4a). Note the asymmetry -- the *dynamic-reference* " +
			"path does **not** trim, and returns `\"a,b, c ,d\"` verbatim (T6e); that path is " +
			"`cfncompat_ssm_parameter_value` with `value_type` unset, and `raw_value` here is the same " +
			"untrimmed string.\n\n" +
			"This makes the data source the one-node replacement for `Fn::Split(',', {{resolve:ssm:...}})` -- " +
			"the shape aws-cdk-lib emits in `StringListParameter.fromStringListParameterName`.\n\n" +
			"~> **Only a `StringList` parameter is accepted.** CloudFormation compares the parameter's " +
			"*declared* Systems Manager type against the template's shape and ignores the content, so a " +
			"`String` parameter cannot satisfy a list-shaped parameter type however many commas its value " +
			"holds -- it is rejected with *\"Types for SSM parameters [...] defined in CFN template and SSM " +
			"are incompatible\"* (T4b/T4c). This data source mirrors that rejection. A `SecureString` fails " +
			"too; use `cfncompat_ssm_secure_parameter_value`.\n\n" +
			"`values` is **not** marked sensitive, for the same reason as " +
			"`cfncompat_ssm_parameter_value.value`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`name`"},
				},
				Description: "The Systems Manager parameter name, or its full ARN. An ARN is required for a " +
					"parameter shared from another AWS account. Names are case-sensitive.",
				MarkdownDescription: "The Systems Manager parameter name, or its full ARN. A parameter shared " +
					"from another AWS account must be given as its full ARN. Names are case-sensitive.",
			},
			"version": schema.Int64Attribute{
				Optional: true,
				Validators: []validator.Int64{
					int64AtLeastValidator{argument: "`version`", min: 1},
				},
				Description:         "Pin the read to a specific parameter version. Mutually exclusive with `label`.",
				MarkdownDescription: "Pin the read to a specific parameter version. Mutually exclusive with `label`. Unset means the latest version at read time.",
			},
			"label": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`label`"},
				},
				Description: "Pin the read to a parameter label. Mutually exclusive with `version`.",
				MarkdownDescription: "Pin the read to a [parameter label](https://docs.aws.amazon.com/systems-manager/latest/userguide/sysman-paramstore-labels.html). Mutually exclusive with `version`.\n\n" +
					"~> A **cfncompat extension, not a polyfill**: CloudFormation supports Systems Manager " +
					"labels nowhere -- neither in a dynamic reference (T6d) nor behind a typed parameter " +
					"(T1c). A synthesis backend translating CloudFormation input must never emit it.",
			},
			"value_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					stringOneOfValidator{argument: "`value_type`", allowed: valueTypes},
				},
				Description: "The CloudFormation inner type of AWS::SSM::Parameter::Value<...>, which decides " +
					"how each element of the resolved list is validated. Defaults to \"List<String>\". Valid " +
					"values: " + strings.Join(valueTypes, ", ") + ".",
				MarkdownDescription: "The CloudFormation **inner type** of `AWS::SSM::Parameter::Value<...>`, " +
					"which decides how each element of the resolved list is validated. Defaults to " +
					"`List<String>`.\n\n" +
					"`List<String>` and `CommaDelimitedList` apply no per-element type validation. Every " +
					"`List<AWS::...>` value checks each element both syntactically and -- unless " +
					"`validate = false` -- for existence, in one batched API call. CloudFormation has no " +
					"`List<AWS::EC2::KeyPair::KeyName>`, so neither does this data source. Valid values:\n\n" +
					markdownList(valueTypes),
			},
			"validate": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Whether an AWS-specific value_type also checks that every element exists in this " +
					"account and region, as CloudFormation does. Defaults to true.",
				MarkdownDescription: "Whether an AWS-specific `value_type` also checks that every element " +
					"**exists** in this account and region, as CloudFormation does. Defaults to `true`. The " +
					"check is one batched API call for the whole list (`route53:GetHostedZone` excepted -- " +
					"Route 53 has no batch read, so that one costs a call per distinct id).",
			},
			"allowed_pattern": schema.StringAttribute{
				Optional: true,
				Description: "A regular expression every element must match in full. A cfncompat extension, " +
					"not a CloudFormation polyfill: CloudFormation's AllowedPattern validates the parameter " +
					"NAME, not the resolved value.",
				MarkdownDescription: "A regular expression **every element** must match in full.\n\n" +
					"~> A **cfncompat extension, not a polyfill.** CloudFormation's `AllowedPattern` on an " +
					"SSM-typed `Parameter` validates the parameter **name**, not the resolved value (T2); it " +
					"has no custom-regex validation of resolved SSM values at all. A synthesis backend must " +
					"not map a `CfnParameter`'s constraints onto this argument.",
			},
			"allowed_values": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "The exact set of values every element may take. A cfncompat extension, not a " +
					"CloudFormation polyfill -- see allowed_pattern.",
				MarkdownDescription: "The exact set of values **every element** may take.\n\n" +
					"~> A **cfncompat extension, not a polyfill**, for the same reason as `allowed_pattern` (T2).",
			},

			"values": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "The resolved list: the stored value split on commas, each member " +
					"whitespace-trimmed, as CloudFormation's typed List<...> resolution does. Not marked sensitive.",
				MarkdownDescription: "The resolved list -- what `Ref` yields for an " +
					"`AWS::SSM::Parameter::Value<List<...>>` parameter: the stored value split on `,` with each " +
					"member whitespace-trimmed, verified against CloudFormation (T4a).\n\n" +
					"Note the degenerate case, which is CloudFormation's too: an empty stored value yields a " +
					"one-element list containing the empty string, not an empty list.",
			},
			"raw_value": schema.StringAttribute{
				Computed: true,
				Description: "The parameter value exactly as Systems Manager stores it, before splitting and " +
					"trimming: the comma-joined string, whitespace included.",
				MarkdownDescription: "The parameter value exactly as Systems Manager stores it, before " +
					"splitting and trimming: the comma-joined string, whitespace included. This is precisely " +
					"what a whole-value `{{resolve:ssm:...}}` dynamic reference expands to for a `StringList` " +
					"parameter (T6e), and it is why `values` and `raw_value` can disagree about whitespace.",
			},
			"arn": schema.StringAttribute{
				Computed:            true,
				Description:         "The ARN of the resolved parameter.",
				MarkdownDescription: "The ARN of the resolved parameter.",
			},
			"type": schema.StringAttribute{
				Computed: true,
				Description: "The Systems Manager parameter type that was read. Always \"StringList\": " +
					"CloudFormation accepts nothing else behind a list-shaped parameter type.",
				MarkdownDescription: "The Systems Manager parameter type that was read. Always `StringList`: " +
					"CloudFormation accepts nothing else behind a list-shaped parameter type (T4b/T4c).",
			},
			"resolved_version": schema.Int64Attribute{
				Computed:            true,
				Description:         "The parameter version that was actually read.",
				MarkdownDescription: "The parameter version that was actually read. Feed it back into `version` to pin.",
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

func (d *SsmParameterListValueDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var model SsmParameterListValueDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if p, summary, detail, conflict := validateVersionLabelExclusive(model.Version, model.Label); conflict {
		resp.Diagnostics.AddAttributeError(p, summary, detail)
	}
}

func (d *SsmParameterListValueDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *provider.ProviderData, got: %T. This is a bug in the cfncompat provider; please report it.", req.ProviderData),
		)
		return
	}

	d.providerData = pd
	if pd.ConfigErr != nil {
		return
	}
	d.clients = newSSMDataSourceClients(pd)
}

func (d *SsmParameterListValueDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model SsmParameterListValueDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !ssmDataSourceReady(d.providerData, "cfncompat_ssm_parameter_list_value", "read a Systems Manager parameter", &resp.Diagnostics) {
		return
	}

	valueType := model.ValueType.ValueString()
	if model.ValueType.IsNull() || model.ValueType.IsUnknown() {
		valueType = cfnValueTypeListString
	}
	spec, ok := cfnListValueTypes[valueType]
	if !ok {
		resp.Diagnostics.AddError(
			"Invalid value_type",
			fmt.Sprintf("%q is not a CloudFormation-supplied parameter type valid for a list value. Valid values are: %s.",
				valueType, strings.Join(sortedValueTypeNames(cfnListValueTypes), ", ")),
		)
		return
	}

	doValidate := true
	if !model.Validate.IsNull() && !model.Validate.IsUnknown() {
		doValidate = model.Validate.ValueBool()
	}

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

	// CloudFormation compares the SSM parameter's *declared* type against the
	// template's shape and ignores the content, so a String parameter cannot
	// satisfy a List-shaped parameter type however many commas its value has
	// (RFCs/dynamic-ssm/live-test-results.md, T4b/T4c).
	switch parameter.Type {
	case ssmTypeStringList:
		// The only type CloudFormation accepts behind a List<...> or
		// CommaDelimitedList parameter type.
	case ssmTypeString:
		resp.Diagnostics.AddError(
			"Incompatible Systems Manager Parameter Type",
			errSSMTypeIncompatible(parameter.Name, ssmTypeString, "a list type", spec.Name).Error()+
				". Read a String parameter through the cfncompat_ssm_parameter_value data source; if you "+
				"genuinely need to split its value, do it with provider::cfncompat::split, which is "+
				"CloudFormation's own Fn::Split",
		)
		return
	case ssmTypeSecureString:
		resp.Diagnostics.AddError(
			"SecureString Parameter Read Through a Non-Sensitive Data Source",
			fmt.Sprintf("the Systems Manager parameter %q is a SecureString, and "+
				"cfncompat_ssm_parameter_list_value exposes `values` as a non-sensitive attribute. Use the "+
				"cfncompat_ssm_secure_parameter_value data source instead. (CloudFormation refuses a "+
				"SecureString behind any AWS::SSM::Parameter::Value<...> type, and has no list form of "+
				"{{resolve:ssm-secure:...}} either.)", parameter.Name),
		)
		return
	default:
		resp.Diagnostics.AddError(
			"Unexpected Systems Manager Parameter Type",
			fmt.Sprintf("the Systems Manager parameter %q has type %q, which this provider does not know how to "+
				"resolve. This is a bug in the cfncompat provider; please report it.", parameter.Name, parameter.Type),
		)
		return
	}

	values := ssmListSplit(parameter.Value)

	if err := validateCfnValueType(ctx, spec, values, doValidate, d.clients.Validator); err != nil {
		resp.Diagnostics.AddError(
			fmt.Sprintf("Resolved Value Is Not a Valid %s", spec.Name),
			fmt.Sprintf("the Systems Manager parameter %q was resolved, but %s", parameter.Name, err.Error()),
		)
		return
	}
	if err := constraints.validate(values); err != nil {
		resp.Diagnostics.AddError(
			"Resolved Value Violates a Parameter Constraint",
			fmt.Sprintf("the Systems Manager parameter %q was resolved, but %s", parameter.Name, err.Error()),
		)
		return
	}

	valueList, diags := types.ListValueFrom(ctx, types.StringType, values)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	model.ValueType = types.StringValue(spec.Name)
	model.Validate = types.BoolValue(doValidate)
	model.Values = valueList
	model.RawValue = types.StringValue(parameter.Value)
	model.Arn = types.StringValue(parameter.ARN)
	model.Type = types.StringValue(parameter.Type)
	model.ResolvedVersion = types.Int64Value(parameter.Version)
	model.LastModifiedDate = types.StringValue(parameter.LastModifiedDate)
	model.Id = types.StringValue(ssmDataSourceID(parameter))

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
