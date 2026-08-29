// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure CustomResource satisfies the framework resource interfaces.
// Note: ResourceWithImportState is intentionally NOT implemented -- the
// CloudFormation custom resource protocol has no read/import operation, so
// state cannot be rebuilt from just a physical resource id.
var _ resource.Resource = &CustomResource{}
var _ resource.ResourceWithConfigure = &CustomResource{}
var _ resource.ResourceWithValidateConfig = &CustomResource{}

// NewCustomResource returns a new instance of the cfncompat_custom_resource
// resource.
func NewCustomResource() resource.Resource {
	return &CustomResource{}
}

// CustomResource faithfully emulates the CloudFormation ENGINE side of the
// CloudFormation custom resource protocol: it sends Create/Update/Delete
// request events to service_token (a Lambda function ARN or SNS topic ARN)
// and waits for the handler's response via a pre-signed S3 PUT URL.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/crpg-ref.html
type CustomResource struct {
	// providerData is nil until Configure has run with non-nil
	// req.ProviderData (e.g. during schema/validation-only requests).
	providerData *ProviderData
	// engine is nil until Configure has successfully built it. Create,
	// Update, and Delete must check for nil and surface a clear error
	// (this happens when the provider's AWS configuration could not be
	// resolved -- see ProviderData.ConfigErr).
	engine *customResourceEngine
}

// CustomResourceModel is the Terraform data model for
// cfncompat_custom_resource.
type CustomResourceModel struct {
	ServiceToken       types.String  `tfsdk:"service_token"`
	ResourceType       types.String  `tfsdk:"resource_type"`
	ResourceProperties types.Dynamic `tfsdk:"resource_properties"`
	LogicalResourceId  types.String  `tfsdk:"logical_resource_id"`
	StackId            types.String  `tfsdk:"stack_id"`
	ServiceTimeout     types.Int64   `tfsdk:"service_timeout"`
	ResponseBucket     types.String  `tfsdk:"response_bucket"`
	ResponseKeyPrefix  types.String  `tfsdk:"response_key_prefix"`
	Id                 types.String  `tfsdk:"id"`
	PhysicalResourceId types.String  `tfsdk:"physical_resource_id"`
	Data               types.Dynamic `tfsdk:"data"`
}

// ValidateConfig warns when stack_id is left unset. The schema default
// (customResourceDefaultStackID) keeps existing state churn-free, but it is a
// sentinel shared by every custom resource that omits the argument, which
// silently defeats the ownership-key guarantee handlers rely on -- so the
// omission is made loud rather than fatal.
//
// Only a *null* configured value warns: an unknown value (e.g. a stack_id
// derived from a not-yet-applied resource) is a perfectly good wiring that
// simply is not resolved yet.
func (r *CustomResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var stackID types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("stack_id"), &stackID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !stackID.IsNull() {
		return
	}

	resp.Diagnostics.AddAttributeWarning(
		path.Root("stack_id"),
		"stack_id not set",
		"stack_id is unset, so the request event's StackId field will be the shared sentinel "+
			customResourceDefaultStackID+" -- the same value every cfncompat_custom_resource in this "+
			"workspace that omits stack_id sends.\n\n"+
			"Custom resource handlers commonly use StackId as an ownership key: CDK's S3 notifications "+
			"handler prefixes every notification Id with \"{StackId}-\" and, on delete, removes exactly "+
			"the notifications carrying that prefix. Two stacks sharing the sentinel would therefore "+
			"claim and delete each other's objects.\n\n"+
			"Recommended wiring:\n\n"+
			"  data \"cfncompat_pseudo_parameters\" \"current\" {\n"+
			"    stack_name = \"MyApp-Prod\"\n"+
			"  }\n\n"+
			"  resource \"cfncompat_custom_resource\" \"example\" {\n"+
			"    stack_id = data.cfncompat_pseudo_parameters.current.stack_id\n"+
			"    # ...\n"+
			"  }\n\n"+
			"stack_name must be set on that data source: without it stack_id is null, which lands right "+
			"back on this sentinel.",
	)
}

func (r *CustomResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_resource"
}

func (r *CustomResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Faithfully emulates the CloudFormation Custom Resource deployment protocol: sends " +
			"Create/Update/Delete request events to service_token (a Lambda function ARN or SNS topic ARN) and " +
			"waits for the handler's response via a pre-signed S3 PUT URL, exactly as the real CloudFormation " +
			"engine does. This lets CDK Terrain's synthesis backend support CloudFormation custom resources " +
			"(including CDK's AwsCustomResource and the custom-resource provider framework, which are just " +
			"Lambdas behind a ServiceToken) with faithful lifecycle semantics.",
		MarkdownDescription: "Faithfully emulates the CloudFormation [Custom Resource deployment " +
			"protocol](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/crpg-ref.html): sends " +
			"`Create`/`Update`/`Delete` request events to `service_token` (a Lambda function ARN or SNS topic " +
			"ARN) and waits for the handler's response via a pre-signed S3 PUT URL, exactly as the real " +
			"CloudFormation engine does. This lets CDK Terrain's synthesis backend support CloudFormation custom " +
			"resources (including CDK's `AwsCustomResource` and the custom-resource provider framework, which " +
			"are just Lambdas behind a `ServiceToken`) with faithful lifecycle semantics.\n\n" +
			"There is no read/import operation in the CloudFormation custom resource protocol, so this resource " +
			"does not support `terraform import`.",
		Attributes: map[string]schema.Attribute{
			"service_token": schema.StringAttribute{
				Required: true,
				Description: "ARN of the Lambda function or SNS topic that implements the custom resource " +
					"handler (CloudFormation's ServiceToken).",
				MarkdownDescription: "ARN of the Lambda function or SNS topic that implements the custom " +
					"resource handler (CloudFormation's `ServiceToken`). Must be a Lambda function ARN " +
					"(`arn:*:lambda:...`, invoked asynchronously) or an SNS topic ARN (`arn:*:sns:...`, " +
					"published to).",
				Validators: []validator.String{customResourceServiceTokenValidator{}},
			},
			"resource_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(customResourceDefaultResourceType),
				Description: "CloudFormation resource type name reported in the request event's ResourceType " +
					"field. Defaults to \"AWS::CloudFormation::CustomResource\".",
				MarkdownDescription: "CloudFormation resource type name reported in the request event's " +
					"`ResourceType` field. Defaults to `\"AWS::CloudFormation::CustomResource\"`. May also be " +
					"set to `Custom::<name>` (matching CloudFormation's convention for named custom resource " +
					"types), where `<name>` matches `^[A-Za-z0-9_@-]{1,52}$` and the full value is at most 60 " +
					"characters. Changing this forces replacement: CloudFormation forbids changing a custom " +
					"resource's type in an update.",
				Validators: []validator.String{customResourceTypeValidator{}},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"resource_properties": schema.DynamicAttribute{
				Optional: true,
				Description: "Arbitrary user-defined properties passed to the handler as the request event's " +
					"ResourceProperties (CloudFormation merges ServiceToken into this map; this resource " +
					"replicates that).",
				MarkdownDescription: "Arbitrary user-defined properties passed to the handler as the request " +
					"event's `ResourceProperties`. May be an object/map (arbitrarily nested) or omitted " +
					"entirely. CloudFormation merges `ServiceToken` into this map when delivering it to the " +
					"handler; this resource replicates that behavior.",
			},
			"logical_resource_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(customResourceDefaultLogicalResourceID),
				Description: "CloudFormation-style logical resource id reported in the request event. " +
					"Typically set by a CDK Terrain synthesis backend to the synthesized CFN logical id.",
				MarkdownDescription: "CloudFormation-style logical resource id reported in the request " +
					"event's `LogicalResourceId` field. Typically set by a CDK Terrain synthesis backend to the " +
					"synthesized CloudFormation logical id; defaults to `\"CfncompatCustomResource\"`.",
			},
			"stack_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(customResourceDefaultStackID),
				Description: "CloudFormation-style stack identifier reported in the request event's StackId " +
					"field, passed through verbatim. Defaults to the shared sentinel " +
					"\"cfncompat/no-stack-id\", which every cfncompat_custom_resource that leaves this unset " +
					"shares -- handlers that use StackId as an ownership key need a real per-stack value, so " +
					"wire this to data.cfncompat_pseudo_parameters.<name>.stack_id with stack_name set. " +
					"Leaving it unset emits a warning.",
				MarkdownDescription: "CloudFormation-style stack identifier reported in the request event's " +
					"`StackId` field, passed through verbatim. Typically set by a CDK Terrain synthesis backend " +
					"to a stack identifier; defaults to `\"cfncompat/no-stack-id\"`.\n\n" +
					"~> That default is a **shared sentinel**: every `cfncompat_custom_resource` in the " +
					"workspace that leaves `stack_id` unset sends the same value. Handlers that treat " +
					"`StackId` as an ownership key then cannot tell one stack's objects from another's -- " +
					"CDK's S3 notifications handler, for instance, prefixes every notification `Id` with " +
					"`{StackId}-` and, on delete, removes exactly the notifications carrying that prefix, so " +
					"two stacks sharing the sentinel would delete each other's notifications. Wire this to " +
					"`data.cfncompat_pseudo_parameters.<name>.stack_id` with `stack_name` set (that value is " +
					"deterministic and stable across applies); leaving it unset emits a warning today and is planned to become an error in v1.0.",
			},
			"service_timeout": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(customResourceDefaultServiceTimeout),
				Description: "Seconds to wait for the handler's response before failing, mirroring " +
					"CloudFormation's ServiceTimeout. Must be between 1 and 3600 (CloudFormation's own range).",
				MarkdownDescription: "Seconds to wait for the handler's response before failing, mirroring " +
					"CloudFormation's `ServiceTimeout`. Must be between 1 and 3600 (CloudFormation's own " +
					"range). Defaults to `3600`.",
				Validators: []validator.Int64{
					customResourceInt64RangeValidator{min: 1, max: 3600},
				},
			},
			"response_bucket": schema.StringAttribute{
				Optional: true,
				Description: "S3 bucket used for this resource's response transport (the pre-signed PUT URL " +
					"the handler writes its response to). Falls back to the provider's custom_resource_bucket " +
					"if unset; it is an error at apply time if neither is set.",
				MarkdownDescription: "S3 bucket used for this resource's response transport (the pre-signed " +
					"PUT URL the handler writes its response to). Falls back to the provider's " +
					"`custom_resource_bucket` if unset; it is an error at apply time if neither is set.",
			},
			"response_key_prefix": schema.StringAttribute{
				Optional: true,
				Description: "Optional S3 key prefix for the response object. The full key is " +
					"\"<response_key_prefix>cfncompat/<RequestId>.json\".",
				MarkdownDescription: "Optional S3 key prefix for the response object. The full key is " +
					"`\"<response_key_prefix>cfncompat/<RequestId>.json\"` -- include a trailing `/` if you " +
					"want the prefix to behave like a folder.",
			},
			"id": schema.StringAttribute{
				Computed: true,
				Description: "Mirrors physical_resource_id. Not computed with UseStateForUnknown: the " +
					"physical resource id can legitimately change on update (a CloudFormation-style " +
					"replacement).",
				MarkdownDescription: "Mirrors `physical_resource_id`. Not computed with `UseStateForUnknown`: " +
					"the physical resource id can legitimately change on update (a CloudFormation-style " +
					"replacement).",
			},
			"physical_resource_id": schema.StringAttribute{
				Computed: true,
				Description: "The physical resource id returned by the handler (CloudFormation's " +
					"PhysicalResourceId). Defaults to the generated RequestId on Create, or is carried over " +
					"from the prior value on Update/Delete, when the handler's response omits it.",
				MarkdownDescription: "The physical resource id returned by the handler (CloudFormation's " +
					"`PhysicalResourceId`). Defaults to the generated `RequestId` on Create, or is carried " +
					"over from the prior value on Update/Delete, when the handler's response omits it.",
			},
			"data": schema.DynamicAttribute{
				Computed: true,
				Description: "The handler's response Data object, made available as resource attributes. " +
					"An empty object when the handler's response omits Data.",
				MarkdownDescription: "The handler's response `Data` object, made available as resource " +
					"attributes. An empty object when the handler's response omits `Data`. If the handler " +
					"sets `NoEcho: true`, a warning diagnostic is raised because Terraform state cannot " +
					"redact this data.",
			},
		},
	}
}

const (
	customResourceDefaultResourceType      = "AWS::CloudFormation::CustomResource"
	customResourceDefaultLogicalResourceID = "CfncompatCustomResource"
	customResourceDefaultStackID           = "cfncompat/no-stack-id"
	customResourceDefaultServiceTimeout    = 3600
)

// customResourceTypeNamePattern validates the "<name>" portion of a
// "Custom::<name>" resource_type value.
var customResourceTypeNamePattern = regexp.MustCompile(`^[A-Za-z0-9_@-]{1,52}$`)

// customResourceServiceTokenValidator validates that service_token is a
// Lambda function ARN or an SNS topic ARN.
type customResourceServiceTokenValidator struct{}

func (v customResourceServiceTokenValidator) Description(_ context.Context) string {
	return "must be a Lambda function ARN (arn:*:lambda:...) or an SNS topic ARN (arn:*:sns:...)"
}

func (v customResourceServiceTokenValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v customResourceServiceTokenValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	value := req.ConfigValue.ValueString()

	parsed, err := arn.Parse(value)
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid service_token",
			fmt.Sprintf("service_token must be a valid ARN: %s", err))
		return
	}

	if parsed.Service != "lambda" && parsed.Service != "sns" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid service_token",
			fmt.Sprintf("service_token must be a Lambda function ARN (arn:*:lambda:...) or an SNS topic ARN "+
				"(arn:*:sns:...), got an ARN for service %q", parsed.Service))
	}
}

// customResourceTypeValidator validates resource_type against
// CloudFormation's allowed custom resource type name forms.
type customResourceTypeValidator struct{}

func (v customResourceTypeValidator) Description(_ context.Context) string {
	return fmt.Sprintf(
		"must be %q, or \"Custom::<name>\" where <name> matches %q and the full value is at most 60 characters",
		customResourceDefaultResourceType, customResourceTypeNamePattern.String(),
	)
}

func (v customResourceTypeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v customResourceTypeValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	value := req.ConfigValue.ValueString()
	if value == customResourceDefaultResourceType {
		return
	}

	const customPrefix = "Custom::"
	if !strings.HasPrefix(value, customPrefix) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid resource_type", v.Description(ctx)+fmt.Sprintf(", got %q", value))
		return
	}

	if len(value) > 60 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid resource_type",
			fmt.Sprintf("resource_type must be at most 60 characters, got %d: %q", len(value), value))
		return
	}

	name := strings.TrimPrefix(value, customPrefix)
	if !customResourceTypeNamePattern.MatchString(name) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid resource_type",
			fmt.Sprintf("the \"<name>\" portion of a Custom::<name> resource_type must match %q, got %q",
				customResourceTypeNamePattern.String(), name))
	}
}

// customResourceInt64RangeValidator validates that an int64 attribute falls
// within [min, max] inclusive.
type customResourceInt64RangeValidator struct {
	min, max int64
}

func (v customResourceInt64RangeValidator) Description(_ context.Context) string {
	return fmt.Sprintf("must be between %d and %d", v.min, v.max)
}

func (v customResourceInt64RangeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v customResourceInt64RangeValidator) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	value := req.ConfigValue.ValueInt64()
	if value < v.min || value > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid service_timeout",
			fmt.Sprintf("service_timeout must be between %d and %d seconds, got %d", v.min, v.max, value))
	}
}

func (r *CustomResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// ProviderData may be nil, e.g. during validation-only requests
	// (ValidateResourceConfig) that occur before the provider has been
	// configured.
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *provider.ProviderData, got: %T. This is a bug in the cfncompat provider; please report it.", req.ProviderData),
		)
		return
	}

	if pd.ConfigErr != nil {
		resp.Diagnostics.AddError(
			"AWS Configuration Required",
			"cfncompat_custom_resource requires resolvable AWS credentials/configuration to invoke service_token "+
				"and transport responses via S3, but the provider could not resolve its AWS configuration: "+
				pd.ConfigErr.Error(),
		)
		return
	}

	r.providerData = pd

	lambdaClient := lambda.NewFromConfig(pd.AwsConfig, func(o *lambda.Options) {
		if pd.Endpoints.Lambda != "" {
			o.BaseEndpoint = aws.String(pd.Endpoints.Lambda)
		}
	})
	snsClient := sns.NewFromConfig(pd.AwsConfig, func(o *sns.Options) {
		if pd.Endpoints.SNS != "" {
			o.BaseEndpoint = aws.String(pd.Endpoints.SNS)
		}
	})
	s3Client := s3.NewFromConfig(pd.AwsConfig, func(o *s3.Options) {
		if pd.Endpoints.S3 != "" {
			o.BaseEndpoint = aws.String(pd.Endpoints.S3)
			o.UsePathStyle = true
		}
	})

	r.engine = &customResourceEngine{
		lambdaClient: lambdaClient,
		snsClient:    snsClient,
		store:        newS3ResponseStore(s3Client),
		pollInterval: crDefaultPollInterval,
	}
}

// Read is a no-op: the CloudFormation custom resource protocol has no read
// operation, so the prior state is simply kept as-is.
func (r *CustomResource) Read(_ context.Context, _ resource.ReadRequest, _ *resource.ReadResponse) {
}

func (r *CustomResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan CustomResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.requireEngine(&resp.Diagnostics) {
		return
	}

	bucket, err := r.resolveResponseBucket(plan.ResponseBucket)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("response_bucket"), "Missing response_bucket", err.Error())
		return
	}

	props, err := customResourcePropertiesToJSON(plan.ResourceProperties)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("resource_properties"), "Invalid resource_properties", err.Error())
		return
	}

	requestID, err := newCustomResourceRequestID()
	if err != nil {
		resp.Diagnostics.AddError("Failed to Generate RequestId", err.Error())
		return
	}

	inv := crInvocation{
		RequestType:        "Create",
		ServiceToken:       plan.ServiceToken.ValueString(),
		StackId:            plan.StackId.ValueString(),
		RequestId:          requestID,
		ResourceType:       plan.ResourceType.ValueString(),
		LogicalResourceId:  plan.LogicalResourceId.ValueString(),
		ResourceProperties: props,
		ServiceTimeout:     time.Duration(plan.ServiceTimeout.ValueInt64()) * time.Second,
		ResponseBucket:     bucket,
		ResponseKeyPrefix:  plan.ResponseKeyPrefix.ValueString(),
	}

	result, err := r.engine.Invoke(ctx, inv)
	if err != nil {
		// Intentionally leave resp.State untouched (it starts out null for
		// Create): a Create failure means CloudFormation never marks the
		// resource created, so Terraform must not persist any state either
		// (equivalent to CFN's CREATE_FAILED short-circuit -- no Delete is
		// ever issued for a resource that was never created).
		resp.Diagnostics.AddError("Custom Resource Create Failed", err.Error())
		return
	}

	r.warnIfNoEcho(resp.Diagnostics.AddWarning, result.NoEcho)

	plan.Id = types.StringValue(result.PhysicalResourceId)
	plan.PhysicalResourceId = types.StringValue(result.PhysicalResourceId)

	dataValue, err := customResourceDataToDynamic(result.Data)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Convert Custom Resource Data", err.Error())
		return
	}
	plan.Data = dataValue

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CustomResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan CustomResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	var state CustomResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.requireEngine(&resp.Diagnostics) {
		return
	}

	bucket, err := r.resolveResponseBucket(plan.ResponseBucket)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("response_bucket"), "Missing response_bucket", err.Error())
		return
	}

	newProps, err := customResourcePropertiesToJSON(plan.ResourceProperties)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("resource_properties"), "Invalid resource_properties", err.Error())
		return
	}
	oldProps, err := customResourcePropertiesToJSON(state.ResourceProperties)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("resource_properties"), "Invalid prior resource_properties", err.Error())
		return
	}

	requestID, err := newCustomResourceRequestID()
	if err != nil {
		resp.Diagnostics.AddError("Failed to Generate RequestId", err.Error())
		return
	}

	inv := crInvocation{
		RequestType:           "Update",
		ServiceToken:          plan.ServiceToken.ValueString(),
		StackId:               plan.StackId.ValueString(),
		RequestId:             requestID,
		ResourceType:          plan.ResourceType.ValueString(),
		LogicalResourceId:     plan.LogicalResourceId.ValueString(),
		PhysicalResourceId:    state.PhysicalResourceId.ValueString(),
		ResourceProperties:    newProps,
		OldResourceProperties: oldProps,
		// OldServiceToken is the PRIOR state's service_token: CloudFormation's
		// OldResourceProperties reflects the previous template's properties
		// verbatim (including whatever ServiceToken was active then), which
		// may differ from the current/plan service_token above if this
		// update also changes the service token (service_token has no
		// RequiresReplace).
		OldServiceToken:   state.ServiceToken.ValueString(),
		ServiceTimeout:    time.Duration(plan.ServiceTimeout.ValueInt64()) * time.Second,
		ResponseBucket:    bucket,
		ResponseKeyPrefix: plan.ResponseKeyPrefix.ValueString(),
	}

	result, err := r.engine.Invoke(ctx, inv)
	if err != nil {
		// Intentionally leave resp.State untouched: the framework
		// pre-populates UpdateResponse.State from the prior state, so an
		// Update failure automatically keeps the resource's last-known-good
		// state, matching CloudFormation's UPDATE_FAILED behavior (the
		// stack rolls back to the old resource, it is never deleted).
		resp.Diagnostics.AddError("Custom Resource Update Failed", err.Error())
		return
	}

	r.warnIfNoEcho(resp.Diagnostics.AddWarning, result.NoEcho)

	oldPhysicalResourceID := state.PhysicalResourceId.ValueString()
	replaced := result.PhysicalResourceId != oldPhysicalResourceID

	plan.Id = types.StringValue(result.PhysicalResourceId)
	plan.PhysicalResourceId = types.StringValue(result.PhysicalResourceId)

	dataValue, err := customResourceDataToDynamic(result.Data)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Convert Custom Resource Data", err.Error())
		return
	}
	plan.Data = dataValue

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !replaced {
		return
	}

	// CloudFormation's UPDATE_COMPLETE_CLEANUP phase: the Update response
	// carried a different PhysicalResourceId, meaning this was a
	// replacement. Send a Delete event for the OLD physical resource,
	// carrying the OLD ResourceProperties, using the (current/plan)
	// service_token -- the currently active handler is responsible for
	// cleaning up what the previous configuration created.
	cleanupRequestID, err := newCustomResourceRequestID()
	if err != nil {
		resp.Diagnostics.AddWarning(
			"Custom Resource Replacement Cleanup Skipped",
			fmt.Sprintf("could not generate a RequestId for the replacement cleanup Delete event of old "+
				"physical_resource_id %q: %s", oldPhysicalResourceID, err),
		)
		return
	}

	cleanupInv := crInvocation{
		RequestType:        "Delete",
		ServiceToken:       plan.ServiceToken.ValueString(),
		StackId:            plan.StackId.ValueString(),
		RequestId:          cleanupRequestID,
		ResourceType:       plan.ResourceType.ValueString(),
		LogicalResourceId:  plan.LogicalResourceId.ValueString(),
		PhysicalResourceId: oldPhysicalResourceID,
		ResourceProperties: oldProps,
		ServiceTimeout:     time.Duration(plan.ServiceTimeout.ValueInt64()) * time.Second,
		ResponseBucket:     bucket,
		ResponseKeyPrefix:  plan.ResponseKeyPrefix.ValueString(),
	}

	// A failed or timed-out cleanup delete is a warning, not an error: the
	// Update itself already succeeded and its new state is already
	// recorded above.
	if _, err := r.engine.Invoke(ctx, cleanupInv); err != nil {
		resp.Diagnostics.AddWarning(
			"Custom Resource Replacement Cleanup Delete Failed",
			fmt.Sprintf("update succeeded with new physical_resource_id %q (replacing %q), but the "+
				"CloudFormation-style cleanup Delete event for the old physical resource failed or timed "+
				"out, so it may still exist: %s", result.PhysicalResourceId, oldPhysicalResourceID, err),
		)
	}
}

func (r *CustomResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CustomResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.requireEngine(&resp.Diagnostics) {
		return
	}

	bucket, err := r.resolveResponseBucket(state.ResponseBucket)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("response_bucket"), "Missing response_bucket", err.Error())
		return
	}

	props, err := customResourcePropertiesToJSON(state.ResourceProperties)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("resource_properties"), "Invalid resource_properties", err.Error())
		return
	}

	requestID, err := newCustomResourceRequestID()
	if err != nil {
		resp.Diagnostics.AddError("Failed to Generate RequestId", err.Error())
		return
	}

	inv := crInvocation{
		RequestType:        "Delete",
		ServiceToken:       state.ServiceToken.ValueString(),
		StackId:            state.StackId.ValueString(),
		RequestId:          requestID,
		ResourceType:       state.ResourceType.ValueString(),
		LogicalResourceId:  state.LogicalResourceId.ValueString(),
		PhysicalResourceId: state.PhysicalResourceId.ValueString(),
		ResourceProperties: props,
		ServiceTimeout:     time.Duration(state.ServiceTimeout.ValueInt64()) * time.Second,
		ResponseBucket:     bucket,
		ResponseKeyPrefix:  state.ResponseKeyPrefix.ValueString(),
	}

	if _, err := r.engine.Invoke(ctx, inv); err != nil {
		// The framework only clears resp.State (removing the resource from
		// Terraform state) when Delete returns no error diagnostics, so
		// returning an error here -- without touching resp.State -- keeps
		// the resource tracked, matching CloudFormation's DELETE_FAILED
		// behavior (the resource stays in the stack).
		resp.Diagnostics.AddError("Custom Resource Delete Failed", err.Error())
	}
}

// requireEngine adds a clear error diagnostic and returns false when the
// resource was never successfully Configured with AWS clients (e.g.
// Configure was never called, or bailed out early due to a nil ProviderData
// or a non-nil ProviderData.ConfigErr, which already added its own
// diagnostic).
func (r *CustomResource) requireEngine(diags *diag.Diagnostics) bool {
	if r.engine != nil {
		return true
	}

	diags.AddError(
		"cfncompat_custom_resource Not Configured",
		"the cfncompat provider was not configured with usable AWS credentials/configuration, so "+
			"cfncompat_custom_resource cannot invoke service_token or transport responses via S3. See the "+
			"provider Configure diagnostics for details.",
	)
	return false
}

// warnIfNoEcho raises a warning diagnostic when the handler set NoEcho:
// true, since (unlike CloudFormation) Terraform state cannot redact
// resource data.
func (r *CustomResource) warnIfNoEcho(addWarning func(string, string), noEcho bool) {
	if !noEcho {
		return
	}

	addWarning(
		"Custom Resource NoEcho Data Cannot Be Redacted",
		"the custom resource handler set NoEcho=true in its response, but Terraform state has no equivalent "+
			"redaction mechanism: the response Data is stored in Terraform state (and may appear in plan/apply "+
			"output) in full. Treat this resource's state as sensitive.",
	)
}

// resolveResponseBucket returns the S3 bucket to use for response
// transport: the resource's own response_bucket if set, else the
// provider's custom_resource_bucket, else an error.
func (r *CustomResource) resolveResponseBucket(responseBucket types.String) (string, error) {
	if !responseBucket.IsNull() && responseBucket.ValueString() != "" {
		return responseBucket.ValueString(), nil
	}

	if r.providerData != nil && r.providerData.CustomResourceBucket != "" {
		return r.providerData.CustomResourceBucket, nil
	}

	return "", errors.New(
		"no S3 bucket configured for custom resource response transport: set response_bucket on this " +
			"cfncompat_custom_resource, or custom_resource_bucket on the provider",
	)
}

// customResourcePropertiesToJSON converts the resource_properties dynamic
// attribute into a plain map[string]any suitable for JSON encoding in the
// request event. A null/absent value converts to an empty map.
func customResourcePropertiesToJSON(v types.Dynamic) (map[string]any, error) {
	converted, unknown, err := toJsonStringConvert(v, "resource_properties")
	if err != nil {
		return nil, err
	}
	if unknown {
		return nil, errors.New("resource_properties contains unknown values; all values must be known when applying")
	}
	if converted == nil {
		return map[string]any{}, nil
	}

	m, ok := converted.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resource_properties must be an object/map, got %T", converted)
	}

	return m, nil
}

// customResourceDataToDynamic converts a decoded response Data object (or
// nil, when the handler omitted Data) into the schema's data DynamicAttribute
// value: an empty object when there is no data.
func customResourceDataToDynamic(data map[string]any) (types.Dynamic, error) {
	if data == nil {
		data = map[string]any{}
	}

	val, err := customResourceJSONToAttrValue(data)
	if err != nil {
		return types.Dynamic{}, err
	}

	return types.DynamicValue(val), nil
}

// customResourceJSONToAttrValue converts a plain Go value tree, as produced
// by decoding JSON with json.Decoder.UseNumber (map[string]any, []any,
// json.Number, string, bool, or nil), into a framework attr.Value. Nested
// object/tuple members are wrapped as types.Dynamic so heterogeneous and
// arbitrarily-shaped JSON round-trips through the schema's DynamicAttribute
// without requiring the caller to know the shape ahead of time.
func customResourceJSONToAttrValue(v any) (attr.Value, error) {
	switch val := v.(type) {
	case nil:
		return types.StringNull(), nil
	case string:
		return types.StringValue(val), nil
	case bool:
		return types.BoolValue(val), nil
	case json.Number:
		f, _, err := big.ParseFloat(val.String(), 10, 0, big.ToNearestEven)
		if err != nil {
			return nil, fmt.Errorf("data: could not parse number %q: %w", val.String(), err)
		}
		return types.NumberValue(f), nil
	case float64:
		return types.NumberValue(big.NewFloat(val)), nil
	case []any:
		elemTypes := make([]attr.Type, 0, len(val))
		elems := make([]attr.Value, 0, len(val))
		for i, e := range val {
			child, err := customResourceJSONToAttrValue(e)
			if err != nil {
				return nil, fmt.Errorf("data[%d]: %w", i, err)
			}
			elemTypes = append(elemTypes, types.DynamicType)
			elems = append(elems, types.DynamicValue(child))
		}
		tuple, diags := types.TupleValue(elemTypes, elems)
		if diags.HasError() {
			return nil, customResourceDiagsToError(diags)
		}
		return tuple, nil
	case map[string]any:
		attrTypes := make(map[string]attr.Type, len(val))
		attrs := make(map[string]attr.Value, len(val))
		for k, e := range val {
			child, err := customResourceJSONToAttrValue(e)
			if err != nil {
				return nil, fmt.Errorf("data.%s: %w", k, err)
			}
			attrTypes[k] = types.DynamicType
			attrs[k] = types.DynamicValue(child)
		}
		obj, diags := types.ObjectValue(attrTypes, attrs)
		if diags.HasError() {
			return nil, customResourceDiagsToError(diags)
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("data: unsupported decoded JSON value type %T", v)
	}
}

// customResourceDiagsToError flattens error diagnostics into a single error.
func customResourceDiagsToError(diags diag.Diagnostics) error {
	msgs := make([]string, 0, len(diags))
	for _, d := range diags.Errors() {
		msgs = append(msgs, fmt.Sprintf("%s: %s", d.Summary(), d.Detail()))
	}
	return errors.New(strings.Join(msgs, "; "))
}
