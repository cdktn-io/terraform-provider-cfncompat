// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Shared plumbing for the three cfncompat_ssm_* data sources: one narrow SSM
// client interface, the GetParameter selector syntax, the CloudFormation-shaped
// error mapping, the hand-rolled schema validators, and the value constraints
// (allowed_pattern, allowed_values).

// SSM parameter type values, as returned by GetParameter.
const (
	ssmTypeString       = string(ssmtypes.ParameterTypeString)
	ssmTypeStringList   = string(ssmtypes.ParameterTypeStringList)
	ssmTypeSecureString = string(ssmtypes.ParameterTypeSecureString)
)

// ssmParameterGetter is the subset of the SSM API the parameter data sources
// use. Implemented by *ssm.Client; faked in tests so the data sources' logic
// runs without AWS.
type ssmParameterGetter interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// ssmParameterSelector builds the value of the GetParameter `Name` field.
// Systems Manager expresses version and label selection inside the name --
// "name:version" and "name:label" -- rather than as separate request fields.
func ssmParameterSelector(name string, version types.Int64, label types.String) string {
	switch {
	case !version.IsNull() && !version.IsUnknown():
		return name + ":" + strconv.FormatInt(version.ValueInt64(), 10)
	case !label.IsNull() && !label.IsUnknown() && label.ValueString() != "":
		return name + ":" + label.ValueString()
	default:
		return name
	}
}

// resolvedSSMParameter is the normalised GetParameter result the data sources
// build their state from.
type resolvedSSMParameter struct {
	ARN              string
	Name             string
	Type             string
	DataType         string
	Value            string
	Version          int64
	LastModifiedDate string
}

// getSSMParameter performs the GetParameter call and normalises both the
// response and the two errors CloudFormation surfaces as validation failures.
//
// withDecryption is always true: SSM documents the flag as ignored for String
// and StringList parameters, so passing it unconditionally keeps one code path
// and lets the secure data source share it. The non-secure data sources reject
// a SecureString result themselves.
func getSSMParameter(ctx context.Context, client ssmParameterGetter, selector string) (resolvedSSMParameter, error) {
	if client == nil {
		return resolvedSSMParameter{}, errors.New("no SSM client was configured (this is a bug in the cfncompat provider; please report it)")
	}

	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(selector),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return resolvedSSMParameter{}, mapSSMGetParameterError(selector, err)
	}
	if out == nil || out.Parameter == nil {
		return resolvedSSMParameter{}, fmt.Errorf("SSM GetParameter returned no parameter for %q", selector)
	}

	p := out.Parameter
	resolved := resolvedSSMParameter{
		ARN:      aws.ToString(p.ARN),
		Name:     aws.ToString(p.Name),
		Type:     string(p.Type),
		DataType: aws.ToString(p.DataType),
		Value:    aws.ToString(p.Value),
		Version:  p.Version,
	}
	if p.LastModifiedDate != nil {
		resolved.LastModifiedDate = p.LastModifiedDate.UTC().Format(time.RFC3339)
	}
	return resolved, nil
}

// mapSSMGetParameterError turns the two GetParameter errors CloudFormation
// reports as template validation failures into actionable diagnostics, and
// passes everything else through with the call named.
func mapSSMGetParameterError(selector string, err error) error {
	var notFound *ssmtypes.ParameterNotFound
	if errors.As(err, &notFound) {
		return fmt.Errorf(
			"the Systems Manager parameter %q does not exist in this account and region "+
				"(SSM GetParameter returned ParameterNotFound). CloudFormation fails a stack operation the "+
				"same way when a referenced parameter is missing. Note that parameter names are "+
				"case-sensitive, and that a parameter shared from another account must be given as its "+
				"full ARN: %w", selector, err)
	}
	var versionNotFound *ssmtypes.ParameterVersionNotFound
	if errors.As(err, &versionNotFound) {
		return fmt.Errorf(
			"the requested version or label of the Systems Manager parameter %q does not exist "+
				"(SSM GetParameter returned ParameterVersionNotFound). Parameter Store keeps a bounded "+
				"history, so a pinned `version` can age out; `label` must be a label that has been applied "+
				"to some version of this parameter: %w", selector, err)
	}
	return fmt.Errorf("calling SSM GetParameter for %q (requires the ssm:GetParameter permission): %w", selector, err)
}

// ssmListSplit splits a Parameter Store StringList (or a String holding a
// comma-delimited list) into its elements the way CloudFormation splits a
// CommaDelimitedList: on commas, with the surrounding whitespace of each
// member string trimmed.
//
// The degenerate case is CloudFormation's too: splitting "" yields a
// single-element list containing the empty string, not an empty list.
func ssmListSplit(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// cfnParameterConstraints holds the optional value constraints the non-secure
// data sources apply to a resolved value; a zero value applies none.
//
// Shaped like a CloudFormation template Parameter's AllowedPattern and
// AllowedValues, but a cfncompat extension rather than a polyfill: those
// constrain the parameter *name* (RFCs/dynamic-ssm/live-test-results.md, T2),
// which a synthesis backend already holds at synth time. MinLength/MaxLength
// are not offered because, with that analogy gone, they are a bare string
// length check HCL can express itself.
type cfnParameterConstraints struct {
	AllowedPattern string
	AllowedValues  []string
}

// hasAny reports whether any constraint is set.
func (c cfnParameterConstraints) hasAny() bool {
	return c.AllowedPattern != "" || c.AllowedValues != nil
}

// validate applies the constraints to each resolved value -- and, for a list,
// to every member.
func (c cfnParameterConstraints) validate(values []string) error {
	if !c.hasAny() {
		return nil
	}

	var pattern *regexp.Regexp
	if c.AllowedPattern != "" {
		// CloudFormation anchors AllowedPattern: the whole value must match,
		// not a substring of it.
		p, err := regexp.Compile("^(?:" + c.AllowedPattern + ")$")
		if err != nil {
			return fmt.Errorf("`allowed_pattern` %q is not a valid regular expression: %w", c.AllowedPattern, err)
		}
		pattern = p
	}

	for i, value := range values {
		where := describeValueAt(values, i)

		if pattern != nil && !pattern.MatchString(value) {
			return fmt.Errorf("the resolved value %s does not match `allowed_pattern` %q", where, c.AllowedPattern)
		}
		if c.AllowedValues != nil {
			allowed := false
			for _, av := range c.AllowedValues {
				if av == value {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("the resolved value %s is not one of `allowed_values` (%s)", where, quoteAll(c.AllowedValues))
			}
		}
	}
	return nil
}

// cfnParameterConstraintsFrom reads the constraint arguments shared by the
// String and List data sources out of their models.
func cfnParameterConstraintsFrom(ctx context.Context, allowedPattern types.String, allowedValues types.List) (cfnParameterConstraints, error) {
	c := cfnParameterConstraints{AllowedPattern: allowedPattern.ValueString()}

	if !allowedValues.IsNull() && !allowedValues.IsUnknown() {
		var vals []string
		if diags := allowedValues.ElementsAs(ctx, &vals, false); diags.HasError() {
			return c, fmt.Errorf("reading `allowed_values`: %v", diags)
		}
		c.AllowedValues = vals
	}
	return c, nil
}

// --- CloudFormation-shaped type-mismatch diagnostics ------------------------
//
// The messages below mirror CloudFormation's own, captured live in
// RFCs/dynamic-ssm/live-test-results.md, so an operator who knows the
// CloudFormation error recognises the cfncompat one.

// errSSMTypeIncompatible is CloudFormation's synchronous rejection when a
// typed template Parameter's shape does not match the SSM parameter's declared
// Type (T4b/T4c/T4d). CloudFormation compares the declared Type only -- the
// content is irrelevant, so a String parameter holding commas cannot satisfy a
// List-shaped Parameter.
func errSSMTypeIncompatible(name, actualType, wantShape, valueType string) error {
	return fmt.Errorf(
		"the Systems Manager parameter %s has type %s, and `value_type = %q` is %s. CloudFormation "+
			"rejects that combination synchronously with \"Types for SSM parameters [%s] defined in CFN "+
			"template and SSM are incompatible\": it compares the declared Systems Manager type against the "+
			"template's shape and ignores the content, so a %s parameter cannot satisfy it however its value "+
			"is punctuated",
		name, actualType, valueType, wantShape, name, actualType)
}

// errSSMSecureThroughPlainSSM is CloudFormation's rejection of a plain
// {{resolve:ssm:...}} reference against a SecureString (T8d).
func errSSMSecureThroughPlainSSM(name string) error {
	return fmt.Errorf(
		"the Systems Manager parameter %s is a SecureString. CloudFormation rejects a plain "+
			"{{resolve:ssm:...}} reference against one with \"Non-secure ssm prefix was used for secure "+
			"parameter %s\". Read it through the cfncompat_ssm_secure_parameter_value data source, which "+
			"marks `value` sensitive -- it is the polyfill for CloudFormation's {{resolve:ssm-secure:...}} "+
			"dynamic reference", name, name)
}

// errSSMSecureAsTemplateParameter is CloudFormation's rejection of a
// SecureString behind an AWS::SSM::Parameter::Value<...> template parameter
// type (T3e).
func errSSMSecureAsTemplateParameter(name string) error {
	return fmt.Errorf(
		"the Systems Manager parameter %s is a SecureString, and setting `value_type` selects "+
			"AWS::SSM::Parameter::Value<...> semantics. CloudFormation rejects that synchronously with "+
			"\"Parameters [%s] referenced by template have types not supported by CloudFormation\". Read it "+
			"through the cfncompat_ssm_secure_parameter_value data source, the polyfill for "+
			"{{resolve:ssm-secure:...}}", name, name)
}

// --- hand-rolled validators -------------------------------------------------
//
// terraform-plugin-framework-validators is deliberately not a dependency of
// this provider (see CLAUDE.md); validators are hand-rolled the way
// pseudoParametersStackNameValidator is.

// stringNotEmptyValidator rejects an empty string argument.
type stringNotEmptyValidator struct {
	argument string
}

func (v stringNotEmptyValidator) Description(_ context.Context) string {
	return fmt.Sprintf("%s must not be an empty string", v.argument)
}

func (v stringNotEmptyValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v stringNotEmptyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			fmt.Sprintf("Invalid %s", v.argument),
			fmt.Sprintf("%s must not be an empty string.", v.argument),
		)
	}
}

// stringOneOfValidator restricts a string argument to a fixed set of values,
// which is what the value_type arguments need and the only reason the
// validators module would otherwise be pulled in.
type stringOneOfValidator struct {
	argument string
	allowed  []string
}

func (v stringOneOfValidator) Description(_ context.Context) string {
	return fmt.Sprintf("%s must be one of: %s", v.argument, strings.Join(v.allowed, ", "))
}

func (v stringOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v stringOneOfValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	for _, a := range v.allowed {
		if a == value {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(
		req.Path,
		fmt.Sprintf("Invalid %s", v.argument),
		fmt.Sprintf("%q is not a CloudFormation-supplied parameter type valid here. Valid values are: %s.",
			value, strings.Join(v.allowed, ", ")),
	)
}

// int64AtLeastValidator rejects an integer argument below a minimum.
type int64AtLeastValidator struct {
	argument string
	min      int64
}

func (v int64AtLeastValidator) Description(_ context.Context) string {
	return fmt.Sprintf("%s must be at least %d", v.argument, v.min)
}

func (v int64AtLeastValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v int64AtLeastValidator) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueInt64() < v.min {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			fmt.Sprintf("Invalid %s", v.argument),
			fmt.Sprintf("%s must be at least %d, got %d.", v.argument, v.min, req.ConfigValue.ValueInt64()),
		)
	}
}

// validateVersionLabelExclusive is the whole ValidateConfig implementation of
// the three SSM data sources. It reads `version` and `label` off the config
// directly rather than through each data source's own model type, so the three
// share one implementation.
func validateVersionLabelExclusive(ctx context.Context, config tfsdk.Config, diags *diag.Diagnostics) {
	var version types.Int64
	var label types.String
	diags.Append(config.GetAttribute(ctx, path.Root("version"), &version)...)
	diags.Append(config.GetAttribute(ctx, path.Root("label"), &label)...)
	if diags.HasError() {
		return
	}

	versionSet := !version.IsNull() && !version.IsUnknown()
	labelSet := !label.IsNull() && !label.IsUnknown() && label.ValueString() != ""
	if !versionSet || !labelSet {
		return
	}
	diags.AddAttributeError(
		path.Root("label"),
		"Conflicting Arguments: version and label",
		"`version` and `label` both select a specific version of a Systems Manager parameter, and "+
			"Systems Manager expresses both through the same `name:selector` syntax, so only one may be "+
			"set. Remove `version` to select by label, or `label` to select by version number.",
	)
}

// ssmValidateFlag resolves the optional-and-computed `validate` argument,
// which defaults to true: an unset AWS-specific value type is checked for
// existence, as CloudFormation checks it.
func ssmValidateFlag(validate types.Bool) bool {
	if validate.IsNull() || validate.IsUnknown() {
		return true
	}
	return validate.ValueBool()
}

// errUnexpectedSSMType covers the Systems Manager parameter types a data
// source has no branch for. GetParameter has only ever returned the three, so
// reaching it means the SDK grew a fourth.
func errUnexpectedSSMType(name, parameterType string) error {
	return fmt.Errorf("the Systems Manager parameter %q has type %q, which this provider does not know how to "+
		"resolve. This is a bug in the cfncompat provider; please report it.", name, parameterType)
}

// --- client construction ----------------------------------------------------

// configuredProviderData unwraps a data source Configure request. The returned
// *ProviderData is kept even when ok is false, because Read reports an
// unconfigured provider and a failed AWS configuration with its own diagnostic
// (see ProviderData.ConfigErr); ok reports only whether AWS clients can be
// built now.
func configuredProviderData(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) (*ProviderData, bool) {
	// ProviderData is nil during validation-only requests, which happen
	// before the provider is configured.
	if req.ProviderData == nil {
		return nil, false
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *provider.ProviderData, got: %T. This is a bug in the cfncompat provider; please report it.", req.ProviderData),
		)
		return nil, false
	}
	return pd, pd.ConfigErr == nil
}

// ssmDataSourceClients holds the AWS clients an SSM parameter data source
// needs: always the SSM client, and -- for the AWS-specific value types --
// the EC2/Route 53 clients the existence checks call.
type ssmDataSourceClients struct {
	SSM       ssmParameterGetter
	Validator *cfnTypeValidator
}

// newSSMDataSourceClients builds the clients from resolved provider data,
// honouring the ssm/ec2/route53 endpoint overrides. The EC2 and Route 53
// clients are built eagerly but only ever called by a value type that has an
// existence check, so a configuration that only reads plain Strings never
// touches them.
func newSSMDataSourceClients(pd *ProviderData) ssmDataSourceClients {
	return ssmDataSourceClients{
		SSM:       newSSMClient(pd),
		Validator: newCFNTypeValidator(pd),
	}
}

// newSSMClient builds the SSM client on its own, for the secure data source,
// which has no value_type and so never runs an existence check.
func newSSMClient(pd *ProviderData) ssmParameterGetter {
	return ssm.NewFromConfig(pd.AwsConfig, func(o *ssm.Options) {
		if pd.Endpoints.SSM != "" {
			o.BaseEndpoint = aws.String(pd.Endpoints.SSM)
		}
	})
}

// newCFNTypeValidator builds the EC2 and Route 53 clients the AWS-specific
// value-type existence checks use.
func newCFNTypeValidator(pd *ProviderData) *cfnTypeValidator {
	return &cfnTypeValidator{
		EC2: ec2.NewFromConfig(pd.AwsConfig, func(o *ec2.Options) {
			if pd.Endpoints.EC2 != "" {
				o.BaseEndpoint = aws.String(pd.Endpoints.EC2)
			}
		}),
		Route53: route53.NewFromConfig(pd.AwsConfig, func(o *route53.Options) {
			if pd.Endpoints.Route53 != "" {
				o.BaseEndpoint = aws.String(pd.Endpoints.Route53)
			}
		}),
	}
}

// ssmDataSourceID is the `id` every SSM data source reports: the resolved
// parameter ARN, falling back to its name when the API returned no ARN.
func ssmDataSourceID(p resolvedSSMParameter) string {
	if p.ARN != "" {
		return p.ARN
	}
	return p.Name
}

// awsDataSourceReady performs the two checks every cfncompat data source that
// calls AWS makes at the top of Read: the provider was configured at all, and
// its AWS configuration resolved. It reports whether Read may continue.
func awsDataSourceReady(pd *ProviderData, dataSourceName, action string, diags interface{ AddError(string, string) }) bool {
	if pd == nil {
		diags.AddError(
			dataSourceName+" Not Configured",
			"the cfncompat provider was not configured, so "+dataSourceName+" cannot "+action+
				". This is a bug in the cfncompat provider; please report it.",
		)
		return false
	}
	if pd.ConfigErr != nil {
		diags.AddError(
			"AWS Configuration Required",
			dataSourceName+" requires resolvable AWS credentials/configuration to "+action+
				", but the provider could not resolve its AWS configuration: "+pd.ConfigErr.Error(),
		)
		return false
	}
	return true
}

// markdownList renders values as a Markdown bullet list of inline code spans.
func markdownList(values []string) string {
	var b strings.Builder
	for _, v := range values {
		b.WriteString("- `" + v + "`\n")
	}
	return b.String()
}
