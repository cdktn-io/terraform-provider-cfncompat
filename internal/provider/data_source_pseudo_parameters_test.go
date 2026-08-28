// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rtresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// fakeCallerIdentity is a callerIdentityGetter fake: it returns a canned
// account/ARN (or an error) and records how many times it was called, so
// tests can assert the "one data source, one STS request" property.
type fakeCallerIdentity struct {
	account *string
	arn     *string
	err     error

	calls int
}

func (f *fakeCallerIdentity) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &sts.GetCallerIdentityOutput{Account: f.account, Arn: f.arn}, nil
}

// newFakeCallerIdentity returns a fake STS client answering with the
// standard test account ID and caller ARN.
func newFakeCallerIdentity() *fakeCallerIdentity {
	return &fakeCallerIdentity{account: aws.String(testAccountID), arn: aws.String(testCallerARN)}
}

const (
	testAccountID  = "123456789012"
	testCallerARN  = "arn:aws:iam::123456789012:user/cfncompat-test"
	testRegionName = "eu-west-1"
)

// pseudoParametersTestSchema returns the cfncompat_pseudo_parameters schema.
func pseudoParametersTestSchema(t *testing.T) dsschema.Schema {
	t.Helper()

	d := &PseudoParametersDataSource{}
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics building schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// dataSourceConfig builds a tfsdk.Config from a data source model.
// tfsdk.Config is read-only, so the model is first written into a
// tfsdk.State (which shares the same value encoding) and its raw value is
// handed to the Config.
func dataSourceConfig(t *testing.T, schema dsschema.Schema, model any) tfsdk.Config {
	t.Helper()

	state := tfsdk.State{Schema: schema}
	diags := state.Set(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics setting config: %v", diags)
	}
	return tfsdk.Config{Schema: schema, Raw: state.Raw}
}

// dataSourceStateFromConfig returns the tfsdk.State the framework hands to a
// data source's Read: fwserver seeds ReadResponse.State from the
// configuration (so a Read that sets nothing echoes the config back), rather
// than from a wholly null value.
func dataSourceStateFromConfig(config tfsdk.Config) tfsdk.State {
	return tfsdk.State{Schema: config.Schema, Raw: config.Raw.Copy()}
}

// readPseudoParameters runs the data source's Read with the given config
// model and returns the resulting state model plus the diagnostics.
func readPseudoParameters(t *testing.T, d *PseudoParametersDataSource, config PseudoParametersDataSourceModel) (PseudoParametersDataSourceModel, *datasource.ReadResponse) {
	t.Helper()

	ctx := context.Background()
	schema := pseudoParametersTestSchema(t)

	cfg := dataSourceConfig(t, schema, &config)
	resp := &datasource.ReadResponse{State: dataSourceStateFromConfig(cfg)}
	d.Read(ctx, datasource.ReadRequest{Config: cfg}, resp)

	var out PseudoParametersDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(ctx, &out); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading state: %v", diags)
		}
	}
	return out, resp
}

// newTestPseudoParametersDataSource builds a data source wired to a fake STS
// client and a ProviderData with the given region, as Configure would.
func newTestPseudoParametersDataSource(stsClient callerIdentityGetter, region string) *PseudoParametersDataSource {
	return &PseudoParametersDataSource{
		providerData: &ProviderData{Region: region},
		stsClient:    stsClient,
	}
}

// nullPseudoParametersConfig returns a config model with every attribute
// null, i.e. `data "cfncompat_pseudo_parameters" "x" {}`.
func nullPseudoParametersConfig() PseudoParametersDataSourceModel {
	return PseudoParametersDataSourceModel{
		StackName:        types.StringNull(),
		NotificationArns: types.ListNull(types.StringType),
		AccountId:        types.StringNull(),
		Partition:        types.StringNull(),
		Region:           types.StringNull(),
		UrlSuffix:        types.StringNull(),
		StackId:          types.StringNull(),
		Id:               types.StringNull(),
	}
}

func TestResolvePseudoParametersComputedValues(t *testing.T) {
	t.Parallel()

	fake := newFakeCallerIdentity()

	values, _, err := resolvePseudoParameters(context.Background(), fake, testRegionName, "")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if values.AccountID != testAccountID {
		t.Errorf("AccountID = %q, want %q", values.AccountID, testAccountID)
	}
	if values.Partition != "aws" {
		t.Errorf("Partition = %q, want %q", values.Partition, "aws")
	}
	if values.Region != testRegionName {
		t.Errorf("Region = %q, want %q", values.Region, testRegionName)
	}
	if values.URLSuffix != "amazonaws.com" {
		t.Errorf("URLSuffix = %q, want %q", values.URLSuffix, "amazonaws.com")
	}
	if values.StackID != "" {
		t.Errorf("StackID = %q, want empty (no stack_name)", values.StackID)
	}
	if want := "aws:" + testAccountID + ":" + testRegionName; values.ID != want {
		t.Errorf("ID = %q, want %q", values.ID, want)
	}
	if fake.calls != 1 {
		t.Errorf("GetCallerIdentity called %d times, want exactly 1", fake.calls)
	}
}

// TestResolvePseudoParametersPartitionPrecedence pins the rule from RFC 006
// §2.4: the STS caller ARN's partition field wins, and the region-prefix
// table is only the fallback when the ARN cannot be parsed.
func TestResolvePseudoParametersPartitionPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		region        string
		arn           *string
		wantPartition string
		wantURLSuffix string
	}{
		{
			name:          "caller ARN partition wins over region table",
			region:        "us-east-1", // the table would say "aws"
			arn:           aws.String("arn:aws-us-gov:iam::123456789012:user/x"),
			wantPartition: "aws-us-gov",
			wantURLSuffix: "amazonaws.com",
		},
		{
			name:          "china caller ARN",
			region:        "cn-north-1",
			arn:           aws.String("arn:aws-cn:iam::123456789012:user/x"),
			wantPartition: "aws-cn",
			wantURLSuffix: "amazonaws.com.cn",
		},
		{
			name:          "unparsable ARN falls back to the region table",
			region:        "cn-north-1",
			arn:           aws.String("not-an-arn"),
			wantPartition: "aws-cn",
			wantURLSuffix: "amazonaws.com.cn",
		},
		{
			name:          "absent ARN falls back to the region table",
			region:        "us-gov-west-1",
			arn:           nil,
			wantPartition: "aws-us-gov",
			wantURLSuffix: "amazonaws.com",
		},
		{
			name:          "empty ARN falls back to the region table",
			region:        "us-iso-east-1",
			arn:           aws.String(""),
			wantPartition: "aws-iso",
			wantURLSuffix: "c2s.ic.gov",
		},
		{
			// arn.Parse accepts an ARN whose partition field is empty; the
			// table must still win, since "" is not a partition.
			name:          "ARN with an empty partition field falls back to the region table",
			region:        "cn-northwest-1",
			arn:           aws.String("arn::iam::123456789012:user/x"),
			wantPartition: "aws-cn",
			wantURLSuffix: "amazonaws.com.cn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCallerIdentity{account: aws.String(testAccountID), arn: tt.arn}

			values, _, err := resolvePseudoParameters(context.Background(), fake, tt.region, "")
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if values.Partition != tt.wantPartition {
				t.Errorf("Partition = %q, want %q", values.Partition, tt.wantPartition)
			}
			if values.URLSuffix != tt.wantURLSuffix {
				t.Errorf("URLSuffix = %q, want %q", values.URLSuffix, tt.wantURLSuffix)
			}
		})
	}
}

// TestResolvePseudoParametersPartitionMismatchWarning covers the diagnostic
// side of the precedence rule: when the caller ARN's partition disagrees
// with the one the region-prefix table would pick, the ARN still wins but
// both partitions are named in a warning.
func TestResolvePseudoParametersPartitionMismatchWarning(t *testing.T) {
	t.Parallel()

	t.Run("mismatch warns and keeps the ARN partition", func(t *testing.T) {
		t.Parallel()

		fake := &fakeCallerIdentity{
			account: aws.String(testAccountID),
			arn:     aws.String("arn:aws-us-gov:iam::123456789012:user/x"),
		}

		values, diags, err := resolvePseudoParameters(context.Background(), fake, "us-east-1", "")
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if diags.HasError() {
			t.Fatalf("a partition mismatch must not be an error: %v", diags)
		}
		if diags.WarningsCount() != 1 {
			t.Fatalf("got %d warnings, want 1 (diags: %v)", diags.WarningsCount(), diags)
		}
		// Both partitions must be named, so the operator can tell which of
		// the region and the credentials is wrong.
		if !diagnosticsContain(diags, "aws-us-gov") || !diagnosticsContain(diags, `"aws"`) {
			t.Errorf("warning %v does not name both partitions", diags)
		}
		// Values are unchanged: the ARN wins, as it does without a warning.
		if values.Partition != "aws-us-gov" {
			t.Errorf("Partition = %q, want %q", values.Partition, "aws-us-gov")
		}
		if values.URLSuffix != "amazonaws.com" {
			t.Errorf("URLSuffix = %q, want %q", values.URLSuffix, "amazonaws.com")
		}
	})

	t.Run("agreement is quiet", func(t *testing.T) {
		t.Parallel()

		fake := &fakeCallerIdentity{
			account: aws.String(testAccountID),
			arn:     aws.String("arn:aws-cn:iam::123456789012:user/x"),
		}

		_, diags, err := resolvePseudoParameters(context.Background(), fake, "cn-north-1", "")
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if len(diags) != 0 {
			t.Errorf("expected no diagnostics when the ARN and the region agree, got: %v", diags)
		}
	})

	t.Run("no ARN is quiet", func(t *testing.T) {
		t.Parallel()

		fake := &fakeCallerIdentity{account: aws.String(testAccountID)}

		_, diags, err := resolvePseudoParameters(context.Background(), fake, "us-east-1", "")
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if len(diags) != 0 {
			t.Errorf("expected no diagnostics without a caller ARN, got: %v", diags)
		}
	})
}

// TestPseudoParametersStackNameValidator pins the non-empty stack_name rule:
// an empty name would derive a stack_id ARN for a stack that cannot exist,
// and handlers would then key on it.
func TestPseudoParametersStackNameValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   types.String
		wantErr bool
	}{
		{name: "empty string is rejected", value: types.StringValue(""), wantErr: true},
		{name: "non-empty name is accepted", value: types.StringValue("MyApp-Prod")},
		{name: "null (argument omitted) is accepted", value: types.StringNull()},
		{name: "unknown is accepted", value: types.StringUnknown()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := validator.StringRequest{Path: path.Root("stack_name"), ConfigValue: tt.value}
			resp := &validator.StringResponse{}
			pseudoParametersStackNameValidator{}.ValidateString(context.Background(), req, resp)
			if got := resp.Diagnostics.HasError(); got != tt.wantErr {
				t.Errorf("HasError() = %v, want %v (diags: %v)", got, tt.wantErr, resp.Diagnostics)
			}
		})
	}
}

// TestPseudoParametersDataSourceConfigure mirrors TestCustomResourceConfigure:
// Configure must tolerate a nil ProviderData, reject a wrong type, keep the
// STS client nil when the AWS configuration failed to resolve, and build one
// otherwise.
func TestPseudoParametersDataSourceConfigure(t *testing.T) {
	t.Parallel()

	t.Run("nil ProviderData is a no-op", func(t *testing.T) {
		t.Parallel()

		d := &PseudoParametersDataSource{}
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: nil}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.providerData != nil || d.stsClient != nil {
			t.Error("expected the data source to stay unconfigured when ProviderData is nil")
		}
	})

	t.Run("unexpected ProviderData type errors", func(t *testing.T) {
		t.Parallel()

		d := &PseudoParametersDataSource{}
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-provider-data"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic for an unexpected ProviderData type")
		}
		if d.stsClient != nil {
			t.Error("expected no STS client to be built for an unexpected ProviderData type")
		}
	})

	t.Run("ConfigErr is deferred to Read", func(t *testing.T) {
		t.Parallel()

		d := &PseudoParametersDataSource{}
		resp := &datasource.ConfigureResponse{}
		pd := &ProviderData{ConfigErr: errors.New("no credentials found")}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: pd}, resp)
		// Not an error here: a configuration that never reads this data
		// source must keep working with an unconfigured provider.
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.providerData != pd {
			t.Error("expected ProviderData to be retained so Read can surface ConfigErr")
		}
		if d.stsClient != nil {
			t.Error("expected no STS client to be built when ConfigErr is set")
		}
	})

	t.Run("builds an STS client", func(t *testing.T) {
		t.Parallel()

		d := &PseudoParametersDataSource{}
		resp := &datasource.ConfigureResponse{}
		pd := &ProviderData{
			Region:    testRegionName,
			Endpoints: EndpointsConfig{STS: "http://localhost:4566"},
		}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: pd}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.providerData != pd {
			t.Error("expected ProviderData to be retained")
		}
		if d.stsClient == nil {
			t.Error("expected an STS client to be built")
		}
	})
}

func TestResolvePseudoParametersErrors(t *testing.T) {
	t.Parallel()

	t.Run("empty region", func(t *testing.T) {
		t.Parallel()

		fake := newFakeCallerIdentity()

		_, _, err := resolvePseudoParameters(context.Background(), fake, "", "")
		if err == nil {
			t.Fatal("expected an error for an unresolvable region, got none")
		}
		if !strings.Contains(err.Error(), "region") {
			t.Errorf("error %q does not mention the region", err)
		}
		if fake.calls != 0 {
			t.Errorf("GetCallerIdentity called %d times, want 0 (region is checked first)", fake.calls)
		}
	})

	t.Run("STS failure", func(t *testing.T) {
		t.Parallel()

		fake := &fakeCallerIdentity{err: errors.New("boom")}

		_, _, err := resolvePseudoParameters(context.Background(), fake, testRegionName, "")
		if err == nil {
			t.Fatal("expected an error when GetCallerIdentity fails, got none")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Errorf("error %q does not wrap the underlying failure", err)
		}
	})

	t.Run("no account id returned", func(t *testing.T) {
		t.Parallel()

		fake := &fakeCallerIdentity{account: nil, arn: aws.String(testCallerARN)}

		_, _, err := resolvePseudoParameters(context.Background(), fake, testRegionName, "")
		if err == nil {
			t.Fatal("expected an error when GetCallerIdentity returns no account, got none")
		}
	})

	t.Run("nil STS client", func(t *testing.T) {
		t.Parallel()

		_, _, err := resolvePseudoParameters(context.Background(), nil, testRegionName, "")
		if err == nil {
			t.Fatal("expected an error with no STS client, got none")
		}
	})
}

// TestPseudoParametersStackIDDeterminism pins the RFC 006 §2.3 contract:
// stack_id is a pure function of (partition, region, account_id,
// stack_name), so CDK custom-resource handlers can use it as a stable
// ownership key across applies.
func TestPseudoParametersStackIDDeterminism(t *testing.T) {
	t.Parallel()

	const stackName = "MyApp-Prod"

	first := pseudoParametersStackID("aws", testRegionName, testAccountID, stackName)
	second := pseudoParametersStackID("aws", testRegionName, testAccountID, stackName)
	if first != second {
		t.Errorf("stack_id is not deterministic: %q then %q", first, second)
	}

	wantShape := regexp.MustCompile(`^arn:aws:cloudformation:eu-west-1:123456789012:stack/MyApp-Prod/[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !wantShape.MatchString(first) {
		t.Errorf("stack_id %q does not match the expected ARN + UUID v5 shape %s", first, wantShape)
	}

	// Every input participates in the derivation.
	differing := map[string]string{
		"stack name": pseudoParametersStackID("aws", testRegionName, testAccountID, "MyApp-Dev"),
		"region":     pseudoParametersStackID("aws", "us-east-1", testAccountID, stackName),
		"account":    pseudoParametersStackID("aws", testRegionName, "210987654321", stackName),
		"partition":  pseudoParametersStackID("aws-cn", testRegionName, testAccountID, stackName),
	}
	for what, got := range differing {
		if got == first {
			t.Errorf("stack_id did not change when the %s changed: %q", what, got)
		}
	}
}

// TestUUIDV5 checks the version-5 UUID implementation against the worked
// example in RFC 9562 Appendix A.4 (namespace DNS, name "www.example.com").
func TestUUIDV5(t *testing.T) {
	t.Parallel()

	namespaceDNS := [16]byte{
		0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1,
		0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8,
	}

	const want = "2ed6657d-e927-568b-95e1-2665a8aea6a2"
	if got := uuidV5(namespaceDNS, "www.example.com"); got != want {
		t.Errorf("uuidV5(NAMESPACE_DNS, %q) = %q, want %q", "www.example.com", got, want)
	}
}

func TestPseudoParametersDataSourceReadStackID(t *testing.T) {
	t.Parallel()

	t.Run("null without stack_name", func(t *testing.T) {
		t.Parallel()

		d := newTestPseudoParametersDataSource(newFakeCallerIdentity(), testRegionName)

		state, resp := readPseudoParameters(t, d, nullPseudoParametersConfig())
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if !state.StackId.IsNull() {
			t.Errorf("stack_id = %v, want null when stack_name is unset", state.StackId)
		}
		if !state.StackName.IsNull() {
			t.Errorf("stack_name = %v, want null when unset", state.StackName)
		}
	})

	t.Run("derived from stack_name", func(t *testing.T) {
		t.Parallel()

		d := newTestPseudoParametersDataSource(newFakeCallerIdentity(), testRegionName)

		config := nullPseudoParametersConfig()
		config.StackName = types.StringValue("MyApp-Prod")

		state, resp := readPseudoParameters(t, d, config)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if state.StackName.ValueString() != "MyApp-Prod" {
			t.Errorf("stack_name = %q, want it echoed back", state.StackName.ValueString())
		}
		want := pseudoParametersStackID("aws", testRegionName, testAccountID, "MyApp-Prod")
		if state.StackId.ValueString() != want {
			t.Errorf("stack_id = %q, want %q", state.StackId.ValueString(), want)
		}
	})
}

func TestPseudoParametersDataSourceReadNotificationArns(t *testing.T) {
	t.Parallel()

	t.Run("defaults to an empty list", func(t *testing.T) {
		t.Parallel()

		d := newTestPseudoParametersDataSource(newFakeCallerIdentity(), testRegionName)

		state, resp := readPseudoParameters(t, d, nullPseudoParametersConfig())
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if state.NotificationArns.IsNull() {
			t.Fatal("notification_arns is null, want an empty list")
		}
		if got := len(state.NotificationArns.Elements()); got != 0 {
			t.Errorf("notification_arns has %d elements, want 0", got)
		}
	})

	t.Run("echoes the configured value", func(t *testing.T) {
		t.Parallel()

		d := newTestPseudoParametersDataSource(newFakeCallerIdentity(), testRegionName)

		arns := []string{
			"arn:aws:sns:eu-west-1:123456789012:stack-events",
			"arn:aws:sns:eu-west-1:123456789012:stack-alarms",
		}
		list, diags := types.ListValueFrom(context.Background(), types.StringType, arns)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics building list: %v", diags)
		}

		config := nullPseudoParametersConfig()
		config.NotificationArns = list

		state, resp := readPseudoParameters(t, d, config)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		var got []string
		if diags := state.NotificationArns.ElementsAs(context.Background(), &got, false); diags.HasError() {
			t.Fatalf("unexpected diagnostics reading notification_arns: %v", diags)
		}
		if len(got) != len(arns) || got[0] != arns[0] || got[1] != arns[1] {
			t.Errorf("notification_arns = %v, want %v", got, arns)
		}
	})
}

func TestPseudoParametersDataSourceReadErrors(t *testing.T) {
	t.Parallel()

	t.Run("surfaces ConfigErr", func(t *testing.T) {
		t.Parallel()

		fake := newFakeCallerIdentity()
		d := &PseudoParametersDataSource{
			providerData: &ProviderData{
				Region:    testRegionName,
				ConfigErr: errors.New("no valid credential sources found"),
			},
			stsClient: fake,
		}

		_, resp := readPseudoParameters(t, d, nullPseudoParametersConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when ProviderData.ConfigErr is set")
		}
		if !diagnosticsContain(resp.Diagnostics, "no valid credential sources found") {
			t.Errorf("diagnostics %v do not surface the underlying ConfigErr", resp.Diagnostics)
		}
		if fake.calls != 0 {
			t.Errorf("GetCallerIdentity called %d times, want 0 when the config failed to resolve", fake.calls)
		}
	})

	t.Run("surfaces an unresolvable region", func(t *testing.T) {
		t.Parallel()

		d := newTestPseudoParametersDataSource(newFakeCallerIdentity(), "")

		_, resp := readPseudoParameters(t, d, nullPseudoParametersConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when no region could be resolved")
		}
		if !diagnosticsContain(resp.Diagnostics, "AWS_REGION") {
			t.Errorf("diagnostics %v do not explain how to set a region", resp.Diagnostics)
		}
	})

	t.Run("errors when the provider was never configured", func(t *testing.T) {
		t.Parallel()

		d := &PseudoParametersDataSource{}

		_, resp := readPseudoParameters(t, d, nullPseudoParametersConfig())
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error diagnostic when Configure never ran")
		}
	})
}

// TestAccPseudoParametersDataSource reads cfncompat_pseudo_parameters
// against real AWS: it calls STS GetCallerIdentity only -- it creates
// nothing. The assertions assume the commercial partition.
//
// Like TestAccCustomResource it is opt-in on top of TF_ACC, because CI runs
// the acceptance suite with TF_ACC=1 and no AWS credentials: set
// CFNCOMPAT_TEST_AWS=1 with credentials resolvable the usual way (e.g.
// aws-vault exec).
func TestAccPseudoParametersDataSource(t *testing.T) {
	if os.Getenv("CFNCOMPAT_TEST_AWS") != "1" {
		t.Skip("set CFNCOMPAT_TEST_AWS=1 (with TF_ACC=1 and AWS credentials) to run this acceptance test")
	}

	rtresource.Test(t, rtresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []rtresource.TestStep{
			{
				// No stack_name: stack_id must be null, notification_arns
				// must default to [].
				Config: `
data "cfncompat_pseudo_parameters" "current" {}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestMatchResourceAttr("data.cfncompat_pseudo_parameters.current", "account_id", regexp.MustCompile(`^[0-9]{12}$`)),
					rtresource.TestCheckResourceAttr("data.cfncompat_pseudo_parameters.current", "partition", "aws"),
					rtresource.TestMatchResourceAttr("data.cfncompat_pseudo_parameters.current", "region", regexp.MustCompile(`^[a-z0-9-]+$`)),
					rtresource.TestCheckResourceAttr("data.cfncompat_pseudo_parameters.current", "url_suffix", "amazonaws.com"),
					rtresource.TestCheckNoResourceAttr("data.cfncompat_pseudo_parameters.current", "stack_id"),
					rtresource.TestCheckResourceAttr("data.cfncompat_pseudo_parameters.current", "notification_arns.#", "0"),
					rtresource.TestMatchResourceAttr("data.cfncompat_pseudo_parameters.current", "id", regexp.MustCompile(`^aws:[0-9]{12}:[a-z0-9-]+$`)),
				),
			},
			{
				// With stack_name: stack_id is a full CloudFormation stack
				// ARN, and notification_arns is echoed back.
				Config: `
data "cfncompat_pseudo_parameters" "current" {
  stack_name        = "test-stack"
  notification_arns = ["arn:aws:sns:us-east-1:123456789012:cfncompat-acc-test"]
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestCheckResourceAttr("data.cfncompat_pseudo_parameters.current", "stack_name", "test-stack"),
					rtresource.TestMatchResourceAttr(
						"data.cfncompat_pseudo_parameters.current", "stack_id",
						regexp.MustCompile(`^arn:aws:cloudformation:[a-z0-9-]+:[0-9]{12}:stack/test-stack/[0-9a-f-]{36}$`),
					),
					rtresource.TestCheckResourceAttr("data.cfncompat_pseudo_parameters.current", "notification_arns.#", "1"),
					rtresource.TestCheckResourceAttr(
						"data.cfncompat_pseudo_parameters.current", "notification_arns.0",
						"arn:aws:sns:us-east-1:123456789012:cfncompat-acc-test",
					),
				),
			},
		},
	})
}
