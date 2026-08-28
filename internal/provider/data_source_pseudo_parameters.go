// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	// SHA-1 is mandated by RFC 9562 for name-based (version 5) UUIDs; it is
	// not used here as a security primitive.
	"crypto/sha1"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure PseudoParametersDataSource satisfies the framework data source
// interfaces.
var _ datasource.DataSource = &PseudoParametersDataSource{}
var _ datasource.DataSourceWithConfigure = &PseudoParametersDataSource{}

// pseudoParametersStackIDNamespace is the fixed UUID namespace under which
// AWS::StackId's UUID component is derived (a name-based UUID version 5, per
// RFC 9562 §5.5). It is itself the version-5 UUID of the DNS name
// "cfncompat.cdktn.io" in the standard DNS namespace
// (6ba7b810-9dad-11d1-80b4-00c04fd430c8), i.e.
//
//	uuidv5(NAMESPACE_DNS, "cfncompat.cdktn.io") = f726b759-9130-5fef-9b32-c492b1995fc4
//
// It is a permanent part of this provider's contract and must never change:
// stack_id has to stay stable across applies (see RFC 006 §2.3 -- CDK
// custom-resource handlers use it as an ownership key), and changing the
// namespace would change every stack_id this provider has ever produced.
var pseudoParametersStackIDNamespace = [16]byte{
	0xf7, 0x26, 0xb7, 0x59, 0x91, 0x30, 0x5f, 0xef,
	0x9b, 0x32, 0xc4, 0x92, 0xb1, 0x99, 0x5f, 0xc4,
}

// callerIdentityGetter is the subset of the STS API client used to resolve
// AWS::AccountId (and, from the caller ARN, to warn when the credentials'
// partition disagrees with the region's). Implemented by *sts.Client; faked
// in tests so the data source's logic runs without AWS.
type callerIdentityGetter interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// NewPseudoParametersDataSource returns a new instance of the
// cfncompat_pseudo_parameters data source.
func NewPseudoParametersDataSource() datasource.DataSource {
	return &PseudoParametersDataSource{}
}

// PseudoParametersDataSource resolves the CloudFormation AWS::* pseudo
// parameters in a single node: AWS::AccountId, AWS::Partition, AWS::Region,
// AWS::URLSuffix, AWS::StackName, AWS::StackId and AWS::NotificationARNs.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html
type PseudoParametersDataSource struct {
	// providerData is nil until Configure has run with non-nil
	// req.ProviderData (e.g. during schema/validation-only requests).
	providerData *ProviderData
	// stsClient is nil until Configure has built it, which only happens when
	// the provider resolved its AWS configuration (see ProviderData.ConfigErr).
	stsClient callerIdentityGetter
}

// PseudoParametersDataSourceModel is the Terraform data model for
// cfncompat_pseudo_parameters.
type PseudoParametersDataSourceModel struct {
	StackName        types.String `tfsdk:"stack_name"`
	NotificationArns types.List   `tfsdk:"notification_arns"`
	AccountId        types.String `tfsdk:"account_id"`
	Partition        types.String `tfsdk:"partition"`
	Region           types.String `tfsdk:"region"`
	UrlSuffix        types.String `tfsdk:"url_suffix"`
	StackId          types.String `tfsdk:"stack_id"`
	Id               types.String `tfsdk:"id"`
}

func (d *PseudoParametersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pseudo_parameters"
}

func (d *PseudoParametersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Resolves the CloudFormation AWS::* pseudo parameters (AWS::AccountId, AWS::Partition, " +
			"AWS::Region, AWS::URLSuffix, AWS::StackName, AWS::StackId, AWS::NotificationARNs) in a single data " +
			"source and a single STS GetCallerIdentity call, so a synthesis targeting hashicorp/awscc plus " +
			"cfncompat needs no hashicorp/aws data sources. Requires resolvable AWS credentials and region.",
		MarkdownDescription: "Resolves the CloudFormation [`AWS::*` pseudo " +
			"parameters](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html) " +
			"-- `AWS::AccountId`, `AWS::Partition`, `AWS::Region`, `AWS::URLSuffix`, `AWS::StackName`, " +
			"`AWS::StackId` and `AWS::NotificationARNs` -- in a single data source and a single STS " +
			"`GetCallerIdentity` call, mirroring the way aws-cdk mints them all from one `Aws` accessor. A " +
			"synthesis targeting `hashicorp/awscc` plus `cfncompat` therefore needs no `hashicorp/aws` data " +
			"sources for them.\n\n" +
			"Unlike the provider-defined functions, this data source **requires resolvable AWS " +
			"credentials and region**.",
		Attributes: map[string]schema.Attribute{
			"stack_name": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "CloudFormation's AWS::StackName, echoed back unchanged. There is no real " +
					"CloudFormation stack behind a Terraform apply, so the synthesis backend supplies the name " +
					"it uses for the stack. Must be non-empty when set. Null when not set, in which case " +
					"stack_id is null too -- so a cfncompat_custom_resource wired to it falls back to the " +
					"shared \"cfncompat/no-stack-id\" sentinel.",
				MarkdownDescription: "CloudFormation's [`AWS::StackName`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html), " +
					"echoed back unchanged. There is no real CloudFormation stack behind a Terraform apply, so " +
					"the synthesis backend (e.g. CDK Terrain, passing `Stack.stackName`) supplies the name it " +
					"uses for the stack. Must be a non-empty string when set (an empty name would derive a " +
					"`stack_id` for a stack that cannot exist).\n\n" +
					"It is also the only input `stack_id` is derived from: leaving it null makes `stack_id` " +
					"null, and a `cfncompat_custom_resource.stack_id` fed from a null value falls back to that " +
					"resource's shared `\"cfncompat/no-stack-id\"` default -- which every stack in the " +
					"workspace shares. Set `stack_name` whenever a custom-resource handler keys on `StackId`.",
				Validators: []validator.String{pseudoParametersStackNameValidator{}},
			},
			"notification_arns": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Description: "CloudFormation's AWS::NotificationARNs, echoed back unchanged; defaults to an " +
					"empty list when not set. There is no CloudFormation stack to send notifications for, so " +
					"the synthesis backend supplies the list it would have passed to CreateStack.",
				MarkdownDescription: "CloudFormation's [`AWS::NotificationARNs`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html), " +
					"echoed back unchanged; defaults to `[]` when not set. There is no CloudFormation stack to " +
					"send notifications for, so the synthesis backend (e.g. CDK Terrain, passing " +
					"`StackProps.notificationArns`) supplies the list it would have passed to `CreateStack`.",
			},
			"account_id": schema.StringAttribute{
				Computed: true,
				Description: "CloudFormation's AWS::AccountId: the AWS account ID of the caller, from STS " +
					"GetCallerIdentity.",
				MarkdownDescription: "CloudFormation's [`AWS::AccountId`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html): " +
					"the AWS account ID of the caller, resolved with an STS `GetCallerIdentity` call using the " +
					"provider's credentials.",
			},
			"partition": schema.StringAttribute{
				Computed: true,
				Description: "CloudFormation's AWS::Partition, e.g. \"aws\", \"aws-cn\" or \"aws-us-gov\". " +
					"Derived from the resolved region with a region-name prefix table, exactly as url_suffix " +
					"is. A STS caller ARN naming a different partition only raises a warning.",
				MarkdownDescription: "CloudFormation's [`AWS::Partition`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html), " +
					"e.g. `aws`, `aws-cn` or `aws-us-gov`. Derived from the resolved `region` with a " +
					"region-name prefix table (mirroring aws-cdk's `RegionInfo.get(region).partition`), " +
					"exactly as `url_suffix` is -- CloudFormation resolves both from the region the stack is " +
					"deployed to, so `partition`, `url_suffix`, `stack_id` and `id` can never disagree with " +
					"`region`.\n\n" +
					"When the STS caller ARN names a different partition -- credentials from one partition and " +
					"a region from another -- the region still wins and a warning names both partitions.",
			},
			"region": schema.StringAttribute{
				Computed: true,
				Description: "CloudFormation's AWS::Region: the region the provider resolved, from the " +
					"provider `region` argument, the AWS_REGION/AWS_DEFAULT_REGION environment variables, a " +
					"shared config file, or IMDS. An error when no region could be resolved.",
				MarkdownDescription: "CloudFormation's [`AWS::Region`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html): " +
					"the region the provider resolved, from the provider `region` argument, the " +
					"`AWS_REGION`/`AWS_DEFAULT_REGION` environment variables, a shared config file, or the EC2 " +
					"Instance Metadata Service. Reading this data source is an error when no region could be " +
					"resolved.",
			},
			"url_suffix": schema.StringAttribute{
				Computed: true,
				Description: "CloudFormation's AWS::URLSuffix: the DNS suffix of the partition, e.g. " +
					"\"amazonaws.com\" or \"amazonaws.com.cn\". No AWS API returns it, so it comes from a " +
					"static partition table.",
				MarkdownDescription: "CloudFormation's [`AWS::URLSuffix`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html): " +
					"the DNS suffix of the partition, e.g. `amazonaws.com` or `amazonaws.com.cn`. No AWS API " +
					"returns it, so -- exactly like `hashicorp/aws`'s `aws_partition.dns_suffix` -- it comes " +
					"from a static table, here mirrored from aws-cdk's `RegionInfo.get(region).domainSuffix`, " +
					"keyed by the region-derived `partition`.",
			},
			"stack_id": schema.StringAttribute{
				Computed: true,
				Description: "CloudFormation's AWS::StackId, shaped like a real stack ARN: " +
					"arn:<partition>:cloudformation:<region>:<account_id>:stack/<stack_name>/<uuid>. Null when " +
					"stack_name is not set. Deterministic: the UUID is a version 5 (name-based) UUID of the " +
					"ARN prefix, so the value is stable across applies and needs no state.",
				MarkdownDescription: "CloudFormation's [`AWS::StackId`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/pseudo-parameter-reference.html), " +
					"shaped like a real stack ARN:\n\n" +
					"    arn:<partition>:cloudformation:<region>:<account_id>:stack/<stack_name>/<uuid>\n\n" +
					"`null` when `stack_name` is not set (there is no stack identity to derive).\n\n" +
					"The value is a **pure function** of `(partition, region, account_id, stack_name)`: the " +
					"UUID component is a version 5 (name-based, SHA-1) UUID of the ARN prefix under a fixed " +
					"cfncompat namespace. It is therefore stable across applies and survives " +
					"`terraform state rm`/re-import -- which matters because CDK custom-resource handlers use " +
					"`StackId` as an ownership key (the S3 notifications handler prefixes every notification " +
					"id with it). Pass it to `cfncompat_custom_resource.stack_id`.\n\n" +
					"~> Wiring this attribute into `cfncompat_custom_resource.stack_id` while `stack_name` is " +
					"unset passes `null`, and that resource then falls back to its shared " +
					"`\"cfncompat/no-stack-id\"` default, which every stack in the workspace shares -- so " +
					"handlers that key on `StackId` can collide across stacks. Set `stack_name`.",
			},
			"id": schema.StringAttribute{
				Computed: true,
				Description: "Identifier for the resolved AWS environment: " +
					"\"<partition>:<account_id>:<region>\"; independent of stack_name.",
				MarkdownDescription: "Identifier for the resolved AWS environment: " +
					"`<partition>:<account_id>:<region>`; independent of `stack_name`.",
			},
		},
	}
}

func (d *PseudoParametersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// ProviderData may be nil, e.g. during validation-only requests that
	// occur before the provider has been configured.
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

	// A failed AWS configuration is not an error here: it is surfaced by
	// Read, so that a configuration that never reads this data source keeps
	// working with an unconfigured provider (see ProviderData.ConfigErr).
	if pd.ConfigErr != nil {
		return
	}

	d.stsClient = sts.NewFromConfig(pd.AwsConfig, func(o *sts.Options) {
		if pd.Endpoints.STS != "" {
			o.BaseEndpoint = aws.String(pd.Endpoints.STS)
		}
	})
}

func (d *PseudoParametersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model PseudoParametersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if d.providerData == nil {
		resp.Diagnostics.AddError(
			"cfncompat_pseudo_parameters Not Configured",
			"the cfncompat provider was not configured, so cfncompat_pseudo_parameters cannot resolve the "+
				"AWS::* pseudo parameters. This is a bug in the cfncompat provider; please report it.",
		)
		return
	}

	if d.providerData.ConfigErr != nil {
		resp.Diagnostics.AddError(
			"AWS Configuration Required",
			"cfncompat_pseudo_parameters requires resolvable AWS credentials/configuration to call STS "+
				"GetCallerIdentity, but the provider could not resolve its AWS configuration: "+
				d.providerData.ConfigErr.Error(),
		)
		return
	}

	values, diags, err := resolvePseudoParameters(ctx, d.stsClient, d.providerData.Region, model.StackName.ValueString())
	resp.Diagnostics.Append(diags...)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Resolve Pseudo Parameters", err.Error())
		return
	}

	// notification_arns is Optional + Computed with no framework default
	// (data source schemas have none), so the empty-list default is applied
	// here; any configured value is echoed back unchanged.
	if model.NotificationArns.IsNull() || model.NotificationArns.IsUnknown() {
		model.NotificationArns = types.ListValueMust(types.StringType, nil)
	}

	model.AccountId = types.StringValue(values.AccountID)
	model.Partition = types.StringValue(values.Partition)
	model.Region = types.StringValue(values.Region)
	model.UrlSuffix = types.StringValue(values.URLSuffix)
	model.StackId = types.StringNull()
	if values.StackID != "" {
		model.StackId = types.StringValue(values.StackID)
	}
	model.Id = types.StringValue(values.ID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// pseudoParameterValues holds the computed AWS::* pseudo parameter values.
// StackID is empty when stack_name was not set, which the data source maps
// onto a null attribute.
type pseudoParameterValues struct {
	AccountID string
	Partition string
	Region    string
	URLSuffix string
	StackID   string
	ID        string
}

// resolvePseudoParameters computes every AWS::* pseudo parameter value from
// the resolved region, the optional stack name, and one STS
// GetCallerIdentity call. It holds all of the data source's logic, taking
// the STS client as a narrow interface so it can be unit tested with a fake.
func resolvePseudoParameters(ctx context.Context, stsClient callerIdentityGetter, region, stackName string) (pseudoParameterValues, diag.Diagnostics, error) {
	var diags diag.Diagnostics

	if region == "" {
		return pseudoParameterValues{}, diags, errors.New(
			"no AWS region could be resolved, but AWS::Region must have a value: set `region` on the " +
				"cfncompat provider, or the AWS_REGION/AWS_DEFAULT_REGION environment variable, or a region " +
				"in the shared AWS config file",
		)
	}

	if stsClient == nil {
		return pseudoParameterValues{}, diags, errors.New("no STS client was configured (this is a bug in the cfncompat provider; please report it)")
	}

	out, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return pseudoParameterValues{}, diags, fmt.Errorf("calling STS GetCallerIdentity to resolve AWS::AccountId: %w", err)
	}
	if out == nil || out.Account == nil || *out.Account == "" {
		return pseudoParameterValues{}, diags, errors.New("STS GetCallerIdentity returned no account ID")
	}

	values := pseudoParameterValues{
		AccountID: *out.Account,
		Region:    region,
	}

	// AWS::Partition is derived from the region, exactly as AWS::URLSuffix
	// is: CloudFormation resolves both from the region the stack is deployed
	// to, so the region-prefix table is the single source for partition,
	// url_suffix, stack_id and id, and the four can never disagree with
	// AWS::Region.
	values.Partition = partitionForRegion(region)

	// The STS caller ARN carries a partition too. It never overrides the
	// region-derived value, but a disagreement means the credentials and the
	// region belong to different partitions (or the region prefix is one this
	// provider's table does not know), which is worth saying out loud.
	if out.Arn != nil {
		if parsed, parseErr := arn.Parse(*out.Arn); parseErr == nil && parsed.Partition != "" && parsed.Partition != values.Partition {
			diags.AddWarning(
				"AWS Partition Mismatch",
				fmt.Sprintf(
					"The region %q belongs to the %q partition according to this provider's region-prefix "+
						"table, but the STS caller ARN says %q. AWS::Partition follows the region, so it is "+
						"%q and AWS::URLSuffix, AWS::StackId and id are derived from it. Check that the "+
						"provider's region and credentials belong to the same AWS partition.",
					region, values.Partition, parsed.Partition, values.Partition,
				),
			)
		}
	}

	values.URLSuffix = urlSuffixForPartition(values.Partition)
	values.ID = fmt.Sprintf("%s:%s:%s", values.Partition, values.AccountID, values.Region)

	if stackName != "" {
		values.StackID = pseudoParametersStackID(values.Partition, values.Region, values.AccountID, stackName)
	}

	return values, diags, nil
}

// pseudoParametersStackNameValidator rejects an empty stack_name. An empty
// name is never what a synthesis backend means -- CloudFormation stack names
// are non-empty -- and it would silently derive a stack_id ARN with an empty
// stack path segment (a stack that cannot exist), which handlers would then
// use as an ownership key.
//
// It is hand-rolled rather than taken from
// terraform-plugin-framework-validators to keep the dependency set as it is;
// resource_custom_resource.go's own validators follow the same convention.
type pseudoParametersStackNameValidator struct{}

func (v pseudoParametersStackNameValidator) Description(_ context.Context) string {
	return "must be a non-empty string"
}

func (v pseudoParametersStackNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v pseudoParametersStackNameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid stack_name",
			"stack_name must be a non-empty string. Omit the argument entirely to leave AWS::StackName "+
				"(and therefore AWS::StackId) null.",
		)
	}
}

// pseudoParametersStackID builds the deterministic AWS::StackId ARN. The
// UUID component is a version 5 (name-based) UUID of the ARN prefix under
// pseudoParametersStackIDNamespace, making stack_id a pure function of its
// four inputs -- see RFC 006 §2.3.
func pseudoParametersStackID(partition, region, accountID, stackName string) string {
	prefix := fmt.Sprintf("arn:%s:cloudformation:%s:%s:stack/%s", partition, region, accountID, stackName)
	return prefix + "/" + uuidV5(pseudoParametersStackIDNamespace, prefix)
}

// uuidV5 returns the RFC 9562 §5.5 name-based UUID (version 5) of name in
// namespace, formatted in the canonical 8-4-4-4-12 hexadecimal form.
//
// It is implemented here rather than pulled in from a UUID library because
// the algorithm is a dozen lines and the provider has no other need for one
// (custom_resource_engine.go generates its v4 RequestIds the same way).
func uuidV5(namespace [16]byte, name string) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)

	var b [16]byte
	copy(b[:], sum[:16])
	// Version 5 in the high nibble of octet 6, RFC 4122 variant in the two
	// high bits of octet 8.
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
