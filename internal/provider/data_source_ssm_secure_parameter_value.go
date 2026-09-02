// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure SsmSecureParameterValueDataSource satisfies the framework data source
// interfaces.
var _ datasource.DataSource = &SsmSecureParameterValueDataSource{}
var _ datasource.DataSourceWithConfigure = &SsmSecureParameterValueDataSource{}
var _ datasource.DataSourceWithValidateConfig = &SsmSecureParameterValueDataSource{}

// NewSsmSecureParameterValueDataSource returns a new instance of the
// cfncompat_ssm_secure_parameter_value data source.
func NewSsmSecureParameterValueDataSource() datasource.DataSource {
	return &SsmSecureParameterValueDataSource{}
}

// SsmSecureParameterValueDataSource decrypts a SecureString Systems Manager
// parameter, the polyfill for CloudFormation's
// `{{resolve:ssm-secure:parameter-name:version}}` dynamic reference.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm-secure-strings.html
type SsmSecureParameterValueDataSource struct {
	providerData *ProviderData
	client       ssmParameterGetter
}

// SsmSecureParameterValueDataSourceModel is the Terraform data model for
// cfncompat_ssm_secure_parameter_value.
type SsmSecureParameterValueDataSourceModel struct {
	Name                 types.String `tfsdk:"name"`
	Version              types.Int64  `tfsdk:"version"`
	Label                types.String `tfsdk:"label"`
	SuppressStateWarning types.Bool   `tfsdk:"suppress_state_warning"`

	Value            types.String `tfsdk:"value"`
	Arn              types.String `tfsdk:"arn"`
	Type             types.String `tfsdk:"type"`
	ResolvedVersion  types.Int64  `tfsdk:"resolved_version"`
	LastModifiedDate types.String `tfsdk:"last_modified_date"`
	Id               types.String `tfsdk:"id"`
}

func (d *SsmSecureParameterValueDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssm_secure_parameter_value"
}

func (d *SsmSecureParameterValueDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Decrypts a SecureString Systems Manager Parameter Store parameter -- the polyfill for " +
			"CloudFormation's {{resolve:ssm-secure:...}} dynamic reference. `value` is marked sensitive. " +
			"Unlike CloudFormation, which never stores a secure string value, Terraform stores the decrypted " +
			"value in plaintext in state; every successful read emits a warning saying so unless " +
			"suppress_state_warning is set.",
		MarkdownDescription: "Decrypts a `SecureString` Systems Manager Parameter Store parameter -- the " +
			"polyfill for CloudFormation's " +
			"[`{{resolve:ssm-secure:parameter-name:version}}`](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm-secure-strings.html) " +
			"dynamic reference, and for aws-cdk-lib's `SecretValue.ssmSecure(name, version)`.\n\n" +
			"~> **The decrypted value lands in Terraform state in plaintext.** This is the one place where " +
			"this provider *cannot* be CloudFormation-faithful: CloudFormation never stores a secure string " +
			"value, storing only the literal dynamic reference and resolving it during each stack operation, " +
			"and it never returns the value from any API. Terraform has no equivalent -- a data source result " +
			"is state. Mitigate with OpenTofu [state encryption](https://opentofu.org/docs/language/state/encryption/), " +
			"a state backend with encryption at rest and tight access control, or by not using this data " +
			"source for values that must never be persisted.\n\n" +
			"`value` is marked `Sensitive`, which keeps it out of plan output but *not* out of state.\n\n" +
			"Terraform re-reads a data source on **every plan**, which is *faithful* here: live testing (T6) " +
			"showed CloudFormation re-resolves an `ssm`/`ssm-secure` dynamic reference on every stack " +
			"operation that reaches the resource, even when the template text is byte-identical.\n\n" +
			"Note also that `ssm-secure` is legal in only eleven allow-listed, password-shaped resource " +
			"properties in CloudFormation; this data source has no such restriction, so a configuration that " +
			"must stay CloudFormation-portable has to respect that list itself.\n\n" +
			"Requires `ssm:GetParameter` and `kms:Decrypt` on the parameter's KMS key.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`name`"},
				},
				Description:         "The Systems Manager parameter name, or its full ARN. Names are case-sensitive.",
				MarkdownDescription: "The Systems Manager parameter name, or its full ARN. Names are case-sensitive. CloudFormation cannot read a secure string from another account or a public parameter; this data source is a superset there, because `GetParameter` is.",
			},
			"version": schema.Int64Attribute{
				Optional: true,
				Validators: []validator.Int64{
					int64AtLeastValidator{argument: "`version`", min: 1},
				},
				Description:         "Pin the read to a specific parameter version. Mutually exclusive with `label`.",
				MarkdownDescription: "Pin the read to a specific parameter version, as the `version` segment of `{{resolve:ssm-secure:name:version}}` does. Mutually exclusive with `label`. Unset means the latest version at read time.",
			},
			"label": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`label`"},
				},
				Description: "Pin the read to a parameter label. Mutually exclusive with `version`.",
				MarkdownDescription: "Pin the read to a [parameter label](https://docs.aws.amazon.com/systems-manager/latest/userguide/sysman-paramstore-labels.html). Mutually exclusive with `version`.\n\n" +
					"~> A **cfncompat extension, not a polyfill**: a label in an `ssm-secure` reference is " +
					"rejected by CloudFormation with *\"Incorrect format is used in the following SSM " +
					"reference\"* (T8e). A synthesis backend must never emit it.",
			},
			"suppress_state_warning": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Suppress the per-read warning that the decrypted value is stored in Terraform " +
					"state. Defaults to false.",
				MarkdownDescription: "Suppress the per-read warning that the decrypted value is stored in " +
					"Terraform state. Defaults to `false`. Set it to `true` once the state-exposure trade-off " +
					"has been consciously accepted (for example under OpenTofu state encryption), so the " +
					"warning does not drown out real diagnostics on every plan.",
			},

			"value": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				Description:         "The decrypted parameter value. Marked sensitive; stored in Terraform state in plaintext.",
				MarkdownDescription: "The decrypted parameter value. Marked `Sensitive`, so it is redacted in plan output -- but it **is** written to Terraform state in plaintext.",
			},
			"arn": schema.StringAttribute{
				Computed:            true,
				Description:         "The ARN of the resolved parameter.",
				MarkdownDescription: "The ARN of the resolved parameter.",
			},
			"type": schema.StringAttribute{
				Computed: true,
				Description: "The Systems Manager parameter type that was read. Normally \"SecureString\"; a " +
					"String or StringList parameter is read successfully but warns.",
				MarkdownDescription: "The Systems Manager parameter type that was read. Normally " +
					"`SecureString`. A `String`/`StringList` parameter is read successfully, with a warning: " +
					"treating a non-secret as a secret is safe, so the read is not failed, but it is almost " +
					"always a mistake in the configuration. (CloudFormation's own behaviour for that " +
					"combination is still unknown -- see `RFCs/007-dynamic-reference-polyfill.md` §6.3.)",
			},
			"resolved_version": schema.Int64Attribute{
				Computed:            true,
				Description:         "The parameter version that was actually read.",
				MarkdownDescription: "The parameter version that was actually read. Feed it back into `version` to pin -- and note CloudFormation's rollback hazard: if a pinned secure-string version is later deleted, a rollback that needs it fails.",
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

func (d *SsmSecureParameterValueDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	validateVersionLabelExclusive(ctx, req.Config, &resp.Diagnostics)
}

func (d *SsmSecureParameterValueDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, ok := configuredProviderData(req, resp)
	d.providerData = pd
	if !ok {
		return
	}
	d.client = newSSMClient(pd)
}

func (d *SsmSecureParameterValueDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model SsmSecureParameterValueDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !awsDataSourceReady(d.providerData, "cfncompat_ssm_secure_parameter_value", "decrypt a Systems Manager parameter", &resp.Diagnostics) {
		return
	}

	selector := ssmParameterSelector(model.Name.ValueString(), model.Version, model.Label)
	parameter, err := getSSMParameter(ctx, d.client, selector)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Read Systems Manager Parameter", err.Error())
		return
	}

	// A non-SecureString parameter is read rather than rejected. The
	// asymmetry with cfncompat_ssm_parameter_value (which *errors* on a
	// SecureString) is deliberate and safety-directed: exposing a secret
	// through a non-sensitive attribute is a security bug and must fail,
	// while reading a plaintext value through a sensitive attribute is
	// merely over-cautious.
	//
	// CloudFormation's behaviour here is still unknown after live testing:
	// T8c in RFCs/dynamic-ssm/live-test-results.md pointed an ssm-secure
	// reference at a plain String parameter and got the *allow-listed
	// property* rejection instead ("SSM Secure reference is not supported
	// in: [...]"), because that check fires first and every property a test
	// can cheaply target is off the allow-list. So this warns rather than
	// guessing at an error.
	if parameter.Type != ssmTypeSecureString {
		resp.Diagnostics.AddWarning(
			"Parameter Is Not a SecureString",
			fmt.Sprintf("the Systems Manager parameter %q has type %q, not SecureString, so nothing about "+
				"this read is actually encrypted. cfncompat_ssm_secure_parameter_value is the polyfill for "+
				"CloudFormation's {{resolve:ssm-secure:...}} dynamic reference, which exists specifically for "+
				"SecureString parameters. The value is still returned (and still marked sensitive), but "+
				"cfncompat_ssm_parameter_value is almost certainly the data source you want -- its `value` is "+
				"not sensitive and so does not poison the attributes it flows into.",
				parameter.Name, parameter.Type),
		)
	}

	suppress := model.SuppressStateWarning.ValueBool()
	if !suppress {
		addSecretStateWarning(&resp.Diagnostics,
			fmt.Sprintf("The decrypted value of the Systems Manager parameter %q", parameter.Name),
			"cfncompat_ssm_secure_parameter_value",
			"CloudFormation never stores a secure string value: it stores only the literal "+
				"{{resolve:ssm-secure:...}} reference, resolves it during each stack operation, and returns "+
				"the value from no API at all.")
	}

	model.SuppressStateWarning = types.BoolValue(suppress)
	model.Value = types.StringValue(parameter.Value)
	model.Arn = types.StringValue(parameter.ARN)
	model.Type = types.StringValue(parameter.Type)
	model.ResolvedVersion = types.Int64Value(parameter.Version)
	model.LastModifiedDate = types.StringValue(parameter.LastModifiedDate)
	model.Id = types.StringValue(ssmDataSourceID(parameter))

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// addSecretStateWarning emits the warning both secret-reading data sources
// raise on every successful read: the value they resolved is now in Terraform
// state in plaintext, which is not what CloudFormation does.
func addSecretStateWarning(diags *diag.Diagnostics, what, dataSourceName, cfnContrast string) {
	diags.AddWarning(
		"Secret Value Is Stored in Terraform State",
		what+" is written to Terraform state in plaintext. Marking the attribute sensitive keeps it out "+
			"of plan output, but state is not protected by it.\n\n"+
			cfnContrast+" Terraform has no equivalent: a data source result is state.\n\n"+
			"Mitigations: OpenTofu state encryption "+
			"(https://opentofu.org/docs/language/state/encryption/), a state backend with encryption at rest "+
			"and least-privilege access, and treating the state file as a secret.\n\n"+
			"This provider intends to offer ephemeral/write-only semantics -- which never touch state -- as "+
			"soon as a consumer can accept them; that needs write-only attributes on the resources these "+
			"values flow into, which the awscc provider does not yet expose.\n\n"+
			"Set `suppress_state_warning = true` on "+dataSourceName+" to silence this warning once the "+
			"trade-off has been accepted.",
	)
}
