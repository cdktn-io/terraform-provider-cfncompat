// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure SecretsManagerSecretValueDataSource satisfies the framework data
// source interfaces.
var _ datasource.DataSource = &SecretsManagerSecretValueDataSource{}
var _ datasource.DataSourceWithConfigure = &SecretsManagerSecretValueDataSource{}

// secretsManagerGetter is the subset of the Secrets Manager API this data
// source uses. Implemented by *secretsmanager.Client; faked in tests.
type secretsManagerGetter interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// NewSecretsManagerSecretValueDataSource returns a new instance of the
// cfncompat_secretsmanager_secret_value data source.
func NewSecretsManagerSecretValueDataSource() datasource.DataSource {
	return &SecretsManagerSecretValueDataSource{}
}

// SecretsManagerSecretValueDataSource resolves a Secrets Manager secret the
// way CloudFormation resolves
// `{{resolve:secretsmanager:secret-id:SecretString:json-key:version-stage:version-id}}`.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-secretsmanager.html
type SecretsManagerSecretValueDataSource struct {
	providerData *ProviderData
	client       secretsManagerGetter
}

// SecretsManagerSecretValueDataSourceModel is the Terraform data model for
// cfncompat_secretsmanager_secret_value.
type SecretsManagerSecretValueDataSourceModel struct {
	SecretID             types.String `tfsdk:"secret_id"`
	JSONKey              types.String `tfsdk:"json_key"`
	VersionStage         types.String `tfsdk:"version_stage"`
	VersionID            types.String `tfsdk:"version_id"`
	SuppressStateWarning types.Bool   `tfsdk:"suppress_state_warning"`

	Value             types.String `tfsdk:"value"`
	Arn               types.String `tfsdk:"arn"`
	Name              types.String `tfsdk:"name"`
	ResolvedVersionID types.String `tfsdk:"resolved_version_id"`
	VersionStages     types.List   `tfsdk:"version_stages"`
	CreatedDate       types.String `tfsdk:"created_date"`
	Id                types.String `tfsdk:"id"`
}

func (d *SecretsManagerSecretValueDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secretsmanager_secret_value"
}

func (d *SecretsManagerSecretValueDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Resolves a Secrets Manager secret the way CloudFormation resolves a " +
			"{{resolve:secretsmanager:...}} dynamic reference: the whole SecretString, or the value of one " +
			"JSON key inside it. `value` is marked sensitive, and -- unlike CloudFormation, which never " +
			"persists a resolved secret -- is stored in Terraform state in plaintext; every successful read " +
			"warns unless suppress_state_warning is set.",
		MarkdownDescription: "Resolves a Secrets Manager secret the way CloudFormation resolves a " +
			"[`{{resolve:secretsmanager:secret-id:SecretString:json-key:version-stage:version-id}}`](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-secretsmanager.html) " +
			"dynamic reference, and the polyfill for aws-cdk-lib's `SecretValue.secretsManager()` -- the shape " +
			"the RDS, CodePipeline and other L2s use for credentials.\n\n" +
			"With no `json_key`, `value` is the entire `SecretString`. With `json_key`, the secret must parse " +
			"as a JSON object and `value` is that key's value -- exactly CloudFormation's " +
			"`{{resolve:secretsmanager:MySecret:SecretString:password}}`.\n\n" +
			"~> **The resolved secret lands in Terraform state in plaintext.** CloudFormation resolves the " +
			"reference during a stack operation and persists nothing; Terraform has no equivalent, because a " +
			"data source result *is* state. Mitigate with OpenTofu " +
			"[state encryption](https://opentofu.org/docs/language/state/encryption/) and a locked-down state " +
			"backend.\n\n" +
			"CloudFormation supports `SecretString` only, and so does this data source: a secret that holds " +
			"only `SecretBinary` fails the read.\n\n" +
			"~> **Rotation produces a diff here where CloudFormation shows none.** Live testing (T9) " +
			"confirmed that a `secretsmanager` reference is re-resolved *only* when CloudFormation is " +
			"independently updating the resource that holds it: after a `put-secret-value`, an unrelated " +
			"stack update left the consuming resource serving the old value, and only a genuine property " +
			"change on that resource picked the new one up. This is the opposite of `ssm`/`ssm-secure`, which " +
			"re-resolve on every deploy. Terraform re-reads this data source on every plan, so a rotation " +
			"shows up immediately as a diff on everything downstream. Mitigate by pinning `version_id`, or " +
			"with `lifecycle { ignore_changes = [...] }` on the consuming resource.\n\n" +
			"Requires the `secretsmanager:GetSecretValue` permission (and `kms:Decrypt` when the secret uses " +
			"a customer-managed key).",
		Attributes: map[string]schema.Attribute{
			"secret_id": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`secret_id`"},
				},
				Description: "The name or ARN of the secret. A secret in another AWS account must be given as " +
					"its full ARN, exactly as CloudFormation requires.",
				MarkdownDescription: "The name or ARN of the secret -- the `secret-id` segment of the dynamic " +
					"reference. A secret in **another AWS account** must be given as its full ARN, exactly as " +
					"CloudFormation requires.",
			},
			"json_key": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`json_key`"},
				},
				Description: "The key of the key/value pair inside the JSON SecretString whose value to " +
					"return. Unset returns the entire SecretString.",
				MarkdownDescription: "The key of the key/value pair inside the JSON `SecretString` whose value " +
					"to return -- the `json-key` segment. Unset returns the entire `SecretString`.\n\n" +
					"The secret must then parse as a JSON **object** and contain the key; either failure is an " +
					"error, as it is in CloudFormation. The key's value may be a JSON string, number or " +
					"boolean (rendered as written); an object, array or `null` is an error, since there is no " +
					"single string to substitute.",
			},
			"version_stage": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`version_stage`"},
				},
				Description: "The staging label of the secret version to read. Defaults to AWSCURRENT when " +
					"neither version_stage nor version_id is set.",
				MarkdownDescription: "The staging label of the secret version to read -- the `version-stage` " +
					"segment. When neither `version_stage` nor `version_id` is set, Secrets Manager returns the " +
					"`AWSCURRENT` version, which is also CloudFormation's default.\n\n" +
					"Both `version_stage` and `version_id` may be set: `GetSecretValue` then requires that the " +
					"stage actually be attached to that version and errors if it is not. That is the API's own " +
					"rule and this data source passes both through unchanged. Prefer setting neither, so " +
					"secret rotation works without a configuration change.",
			},
			"version_id": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringNotEmptyValidator{argument: "`version_id`"},
				},
				Description: "The unique identifier of the secret version to read -- the version-id segment.",
				MarkdownDescription: "The unique identifier of the secret version to read -- the `version-id` " +
					"segment. See `version_stage` for how the two interact.",
			},
			"suppress_state_warning": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Suppress the per-read warning that the resolved secret is stored in Terraform " +
					"state. Defaults to false.",
				MarkdownDescription: "Suppress the per-read warning that the resolved secret is stored in " +
					"Terraform state. Defaults to `false`.",
			},

			"value": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				Description: "The resolved secret: the entire SecretString, or the value of json_key inside " +
					"it. Marked sensitive; stored in Terraform state in plaintext.",
				MarkdownDescription: "The resolved secret: the entire `SecretString`, or the value of " +
					"`json_key` inside it. Marked `Sensitive`, so it is redacted in plan output -- but it " +
					"**is** written to Terraform state in plaintext.",
			},
			"arn": schema.StringAttribute{
				Computed:            true,
				Description:         "The ARN of the secret.",
				MarkdownDescription: "The ARN of the secret.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				Description:         "The friendly name of the secret.",
				MarkdownDescription: "The friendly name of the secret.",
			},
			"resolved_version_id": schema.StringAttribute{
				Computed:            true,
				Description:         "The unique identifier of the secret version that was actually read.",
				MarkdownDescription: "The unique identifier of the secret version that was actually read. Note that pinning it defeats rotation, which is why CloudFormation's documented best practice is a versionless reference.",
			},
			"version_stages": schema.ListAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				Description:         "The staging labels attached to the secret version that was read.",
				MarkdownDescription: "The staging labels attached to the secret version that was read (for example `AWSCURRENT`).",
			},
			"created_date": schema.StringAttribute{
				Computed:            true,
				Description:         "When the secret version was created, as an RFC 3339 timestamp in UTC.",
				MarkdownDescription: "When the secret version was created, as an RFC 3339 timestamp in UTC.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Identifier for this data source: the secret ARN.",
				MarkdownDescription: "Identifier for this data source: the secret ARN.",
			},
		},
	}
}

func (d *SecretsManagerSecretValueDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.client = secretsmanager.NewFromConfig(pd.AwsConfig, func(o *secretsmanager.Options) {
		if pd.Endpoints.SecretsManager != "" {
			o.BaseEndpoint = aws.String(pd.Endpoints.SecretsManager)
		}
	})
}

func (d *SecretsManagerSecretValueDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model SecretsManagerSecretValueDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !ssmDataSourceReady(d.providerData, "cfncompat_secretsmanager_secret_value", "read a Secrets Manager secret", &resp.Diagnostics) {
		return
	}
	if d.client == nil {
		resp.Diagnostics.AddError(
			"cfncompat_secretsmanager_secret_value Not Configured",
			"no Secrets Manager client was configured. This is a bug in the cfncompat provider; please report it.",
		)
		return
	}

	secretID := model.SecretID.ValueString()

	input := &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretID)}
	if v := model.VersionStage.ValueString(); v != "" {
		input.VersionStage = aws.String(v)
	}
	if v := model.VersionID.ValueString(); v != "" {
		input.VersionId = aws.String(v)
	}

	out, err := d.client.GetSecretValue(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Read Secrets Manager Secret", mapGetSecretValueError(secretID, err).Error())
		return
	}
	if out == nil {
		resp.Diagnostics.AddError(
			"Failed to Read Secrets Manager Secret",
			fmt.Sprintf("Secrets Manager GetSecretValue returned no result for %q.", secretID),
		)
		return
	}

	if out.SecretString == nil {
		// CloudFormation's reference pattern has exactly one supported
		// secret-string segment, "SecretString"; there is no SecretBinary
		// form of the dynamic reference at all.
		resp.Diagnostics.AddError(
			"Secret Has No SecretString",
			fmt.Sprintf("the Secrets Manager secret %q holds only a SecretBinary value. CloudFormation's "+
				"secretsmanager dynamic reference supports SecretString only -- its `secret-string` segment "+
				"has exactly one legal value -- so this data source does too. Store the value as a "+
				"SecretString, or read the binary through the hashicorp/aws provider's "+
				"aws_secretsmanager_secret_version data source.", secretID),
		)
		return
	}

	secretString := aws.ToString(out.SecretString)
	value := secretString

	if jsonKey := model.JSONKey.ValueString(); jsonKey != "" {
		extracted, err := extractSecretJSONKey(secretString, jsonKey)
		if err != nil {
			resp.Diagnostics.AddError(
				"Failed to Extract json_key From Secret",
				fmt.Sprintf("the Secrets Manager secret %q was read, but %s", secretID, err.Error()),
			)
			return
		}
		value = extracted
	}

	suppress := model.SuppressStateWarning.ValueBool()
	if !suppress {
		what := fmt.Sprintf("The value read from the Secrets Manager secret %q", secretID)
		addSecretStateWarning(&resp.Diagnostics, what, "cfncompat_secretsmanager_secret_value",
			"CloudFormation resolves a secretsmanager dynamic reference during a stack operation and "+
				"persists nothing: the template stores only the literal reference.")
	}

	stages, diags := types.ListValueFrom(ctx, types.StringType, nonNilStrings(out.VersionStages))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createdDate := ""
	if out.CreatedDate != nil {
		createdDate = out.CreatedDate.UTC().Format(time.RFC3339)
	}

	arn := aws.ToString(out.ARN)

	model.SuppressStateWarning = types.BoolValue(suppress)
	model.Value = types.StringValue(value)
	model.Arn = types.StringValue(arn)
	model.Name = types.StringValue(aws.ToString(out.Name))
	model.ResolvedVersionID = types.StringValue(aws.ToString(out.VersionId))
	model.VersionStages = stages
	model.CreatedDate = types.StringValue(createdDate)
	if arn != "" {
		model.Id = types.StringValue(arn)
	} else {
		model.Id = types.StringValue(secretID)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// nonNilStrings returns values with nothing removed but guaranteed non-nil, so
// an empty list renders as [] rather than null.
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// mapGetSecretValueError turns the errors a caller can actually act on into
// actionable diagnostics, and names the call for everything else.
func mapGetSecretValueError(secretID string, err error) error {
	var notFound *smtypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return fmt.Errorf(
			"the Secrets Manager secret %q does not exist in this account and region, or the requested "+
				"version does not exist (GetSecretValue returned ResourceNotFoundException). A secret in "+
				"another AWS account must be given as its full ARN, exactly as CloudFormation requires: %w",
			secretID, err)
	}
	var invalidRequest *smtypes.InvalidRequestException
	if errors.As(err, &invalidRequest) {
		return fmt.Errorf(
			"Secrets Manager rejected the request for %q as invalid (InvalidRequestException). This is what "+
				"it returns for a secret that is scheduled for deletion, and when `version_stage` and "+
				"`version_id` are both set but the stage is not attached to that version: %w", secretID, err)
	}
	return fmt.Errorf(
		"calling Secrets Manager GetSecretValue for %q (requires the secretsmanager:GetSecretValue "+
			"permission, and kms:Decrypt for a customer-managed key): %w", secretID, err)
}

// extractSecretJSONKey pulls one key out of a JSON secret, the way
// CloudFormation's json-key segment does.
func extractSecretJSONKey(secretString, jsonKey string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(secretString), &fields); err != nil {
		return "", fmt.Errorf(
			"could not parse SecretString JSON: `json_key` is set to %q, so the secret must be a JSON "+
				"object, and it is not (%w). CloudFormation fails with that same message. Remove `json_key` "+
				"to read the whole SecretString", jsonKey, err)
	}

	raw, ok := fields[jsonKey]
	if !ok {
		return "", fmt.Errorf(
			"could not find a value associated with JSONKey in SecretString: the secret's JSON has no key "+
				"%q. CloudFormation fails with that same message, as a resource-level failure at deploy "+
				"time", jsonKey)
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("the value of the JSON key %q could not be decoded: %w", jsonKey, err)
	}

	switch v := decoded.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		// Render the number as it appears in the secret rather than through
		// float formatting, so an integer stays an integer.
		return string(raw), nil
	default:
		return "", fmt.Errorf(
			"the value of the JSON key %q is a %s, and a dynamic reference substitutes a single string. "+
				"Point `json_key` at a string, number or boolean value", jsonKey, jsonValueKind(decoded))
	}
}

// jsonValueKind names a decoded JSON value's kind for a diagnostic.
func jsonValueKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "JSON object"
	case []any:
		return "JSON array"
	default:
		return fmt.Sprintf("%T", v)
	}
}
