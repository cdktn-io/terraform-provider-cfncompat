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

	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rtresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// newSsmParameterValueDataSource builds the data source with fakes in place
// of the AWS clients.
func newSsmParameterValueDataSource(ssmFake *fakeSSMParameterGetter, validationFake *fakeCFNValidationAPI) *SsmParameterValueDataSource {
	d := &SsmParameterValueDataSource{
		providerData: &ProviderData{Region: "us-east-1"},
		clients:      ssmDataSourceClients{SSM: ssmFake},
	}
	if validationFake != nil {
		d.clients.Validator = fakeValidator(validationFake)
	}
	return d
}

// ssmParameterValueConfig is the base config model for a read: every
// types.List field must carry its element type, since the framework rejects a
// list value whose element type is missing.
func ssmParameterValueConfig(name string) SsmParameterValueDataSourceModel {
	return SsmParameterValueDataSourceModel{
		Name:          types.StringValue(name),
		AllowedValues: types.ListNull(types.StringType),
	}
}

// diagnosticsText renders a Read's diagnostics for substring assertions.
func diagnosticsText(resp *datasource.ReadResponse) string {
	var b strings.Builder
	for _, d := range resp.Diagnostics {
		b.WriteString(d.Summary())
		b.WriteString(": ")
		b.WriteString(d.Detail())
		b.WriteString("\n")
	}
	return b.String()
}

func TestSsmParameterValueDataSourceRead(t *testing.T) {
	t.Parallel()

	ssmFake := &fakeSSMParameterGetter{
		parameter: ssmParameter("/app/config/url", ssmTypeString, "https://example.com", 7),
	}
	d := newSsmParameterValueDataSource(ssmFake, nil)

	state, resp := readDataSource(t, d, ssmParameterValueConfig("/app/config/url"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
	}

	if got := state.Value.ValueString(); got != "https://example.com" {
		t.Errorf("value = %q, want %q", got, "https://example.com")
	}
	if got := state.Type.ValueString(); got != ssmTypeString {
		t.Errorf("type = %q, want %q", got, ssmTypeString)
	}
	if got := state.DataType.ValueString(); got != "text" {
		t.Errorf("data_type = %q, want %q", got, "text")
	}
	if got := state.ResolvedVersion.ValueInt64(); got != 7 {
		t.Errorf("resolved_version = %d, want 7", got)
	}
	if got := state.LastModifiedDate.ValueString(); got != "2026-08-01T12:00:00Z" {
		t.Errorf("last_modified_date = %q, want an RFC 3339 UTC timestamp", got)
	}
	if !state.ValueType.IsNull() {
		t.Errorf("value_type = %q, want it to stay null: it has no default, and unset selects "+
			"dynamic-reference semantics", state.ValueType.ValueString())
	}
	if !state.Validate.ValueBool() {
		t.Error("validate should default to true")
	}
	if got, want := state.Id.ValueString(), state.Arn.ValueString(); got != want {
		t.Errorf("id = %q, want the parameter ARN %q", got, want)
	}
	if ssmFake.lastSelector != "/app/config/url" {
		t.Errorf("GetParameter selector = %q, want the bare name", ssmFake.lastSelector)
	}
	if !ssmFake.lastDecrypted {
		t.Error("GetParameter should always be called WithDecryption (SSM ignores it for String)")
	}
}

// TestSsmParameterValueDataSourceValueIsNotSensitive pins the deliberate
// divergence from hashicorp/aws: a non-secret value must not poison the
// attributes it flows into.
func TestSsmParameterValueDataSourceValueIsNotSensitive(t *testing.T) {
	t.Parallel()

	schema := dataSourceSchema(t, NewSsmParameterValueDataSource())
	if schema.Attributes["value"].IsSensitive() {
		t.Error("cfncompat_ssm_parameter_value.value must not be sensitive")
	}

	listSchema := dataSourceSchema(t, NewSsmParameterListValueDataSource())
	if listSchema.Attributes["values"].IsSensitive() {
		t.Error("cfncompat_ssm_parameter_list_value.values must not be sensitive")
	}

	secureSchema := dataSourceSchema(t, NewSsmSecureParameterValueDataSource())
	if !secureSchema.Attributes["value"].IsSensitive() {
		t.Error("cfncompat_ssm_secure_parameter_value.value must be sensitive")
	}

	secretSchema := dataSourceSchema(t, NewSecretsManagerSecretValueDataSource())
	if !secretSchema.Attributes["value"].IsSensitive() {
		t.Error("cfncompat_secretsmanager_secret_value.value must be sensitive")
	}
}

func TestSsmParameterValueDataSourceSelectors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		version types.Int64
		label   types.String
		want    string
	}{
		"unpinned": {types.Int64Null(), types.StringNull(), "/a/b"},
		"version":  {types.Int64Value(4), types.StringNull(), "/a/b:4"},
		"label":    {types.Int64Null(), types.StringValue("prod"), "/a/b:prod"},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a/b", ssmTypeString, "v", 4)}
			d := newSsmParameterValueDataSource(ssmFake, nil)

			config := ssmParameterValueConfig("/a/b")
			config.Version = testCase.version
			config.Label = testCase.label
			_, resp := readDataSource(t, d, config)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
			}
			if ssmFake.lastSelector != testCase.want {
				t.Fatalf("GetParameter selector = %q, want %q", ssmFake.lastSelector, testCase.want)
			}
		})
	}
}

func TestSsmParameterValueDataSourceWrongParameterTypes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		parameterType string
		valueType     types.String
		wantContains  []string
	}{
		// Mode (a), value_type unset: dynamic-reference semantics. A
		// SecureString gets CloudFormation's {{resolve:ssm}} message.
		"SecureString in dynamic-reference mode": {
			parameterType: ssmTypeSecureString,
			valueType:     types.StringNull(),
			wantContains: []string{
				"Non-secure ssm prefix was used for secure parameter",
				"cfncompat_ssm_secure_parameter_value",
			},
		},
		// Mode (b), value_type set: typed template-parameter semantics, so
		// CloudFormation's parameter-type message instead.
		"SecureString in typed mode": {
			parameterType: ssmTypeSecureString,
			valueType:     types.StringValue(cfnValueTypeString),
			wantContains: []string{
				"types not supported by CloudFormation",
				"cfncompat_ssm_secure_parameter_value",
			},
		},
		// A StringList is only rejected in typed mode, with
		// CloudFormation's incompatible-types message.
		"StringList in typed mode": {
			parameterType: ssmTypeStringList,
			valueType:     types.StringValue(cfnValueTypeString),
			wantContains: []string{
				"defined in CFN template and SSM are incompatible",
				"cfncompat_ssm_parameter_list_value",
			},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a/b", testCase.parameterType, "x", 1)}
			d := newSsmParameterValueDataSource(ssmFake, nil)

			config := ssmParameterValueConfig("/a/b")
			config.ValueType = testCase.valueType
			_, resp := readDataSource(t, d, config)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected the read to fail")
			}
			text := diagnosticsText(resp)
			for _, want := range testCase.wantContains {
				if !strings.Contains(text, want) {
					t.Errorf("expected diagnostics to contain %q, got %s", want, text)
				}
			}
		})
	}
}

// TestSsmParameterValueDataSourceStringListInDynamicReferenceMode pins the
// asymmetry the live tests found: {{resolve:ssm:...}} on a StringList returns
// the raw comma-joined string, untrimmed, where the typed List<...> path
// trims each element.
func TestSsmParameterValueDataSourceStringListInDynamicReferenceMode(t *testing.T) {
	t.Parallel()

	ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a/list", ssmTypeStringList, "a,b, c ,d", 1)}
	d := newSsmParameterValueDataSource(ssmFake, nil)

	state, resp := readDataSource(t, d, ssmParameterValueConfig("/a/list"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("a StringList must be readable with value_type unset: %s", diagnosticsText(resp))
	}
	if got := state.Value.ValueString(); got != "a,b, c ,d" {
		t.Fatalf("value = %q, want the raw untrimmed stored string", got)
	}
	if got := state.Type.ValueString(); got != ssmTypeStringList {
		t.Errorf("type = %q, want %q", got, ssmTypeStringList)
	}
}

func TestSsmParameterValueDataSourceGetParameterErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err           error
		errorContains string
	}{
		"parameter not found": {
			&ssmtypes.ParameterNotFound{},
			"does not exist in this account and region",
		},
		"parameter version not found": {
			&ssmtypes.ParameterVersionNotFound{},
			"does not exist",
		},
		"anything else names the permission": {
			errors.New("AccessDeniedException"),
			"ssm:GetParameter",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newSsmParameterValueDataSource(&fakeSSMParameterGetter{err: testCase.err}, nil)
			_, resp := readDataSource(t, d, ssmParameterValueConfig("/a/b"))
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected the read to fail")
			}
			if text := diagnosticsText(resp); !strings.Contains(text, testCase.errorContains) {
				t.Fatalf("expected diagnostics to contain %q, got %s", testCase.errorContains, text)
			}
		})
	}
}

func TestSsmParameterValueDataSourceValueTypeValidation(t *testing.T) {
	t.Parallel()

	t.Run("valid AMI id with an existence check", func(t *testing.T) {
		t.Parallel()

		ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/ami", ssmTypeString, "ami-12345678", 1)}
		validationFake := &fakeCFNValidationAPI{images: []string{"ami-12345678"}}
		d := newSsmParameterValueDataSource(ssmFake, validationFake)

		config := ssmParameterValueConfig("/ami")
		config.ValueType = types.StringValue(cfnValueTypeImageID)
		state, resp := readDataSource(t, d, config)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
		}
		if state.Value.ValueString() != "ami-12345678" {
			t.Errorf("value = %q", state.Value.ValueString())
		}
		if validationFake.calls["DescribeImages"] != 1 {
			t.Errorf("expected one DescribeImages call, got %v", validationFake.calls)
		}
	})

	t.Run("syntactically wrong value fails before any API call", func(t *testing.T) {
		t.Parallel()

		ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/ami", ssmTypeString, "not-an-ami", 1)}
		validationFake := &fakeCFNValidationAPI{}
		d := newSsmParameterValueDataSource(ssmFake, validationFake)

		config := ssmParameterValueConfig("/ami")
		config.ValueType = types.StringValue(cfnValueTypeImageID)
		_, resp := readDataSource(t, d, config)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if len(validationFake.calls) != 0 {
			t.Errorf("expected no existence check after a syntax failure, got %v", validationFake.calls)
		}
	})

	t.Run("validate = false skips the existence check", func(t *testing.T) {
		t.Parallel()

		ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/ami", ssmTypeString, "ami-99999999", 1)}
		validationFake := &fakeCFNValidationAPI{} // knows about no images at all
		d := newSsmParameterValueDataSource(ssmFake, validationFake)

		config := ssmParameterValueConfig("/ami")
		config.ValueType = types.StringValue(cfnValueTypeImageID)
		config.Validate = types.BoolValue(false)
		state, resp := readDataSource(t, d, config)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
		}
		if state.Validate.ValueBool() {
			t.Error("validate should be echoed back as false")
		}
		if len(validationFake.calls) != 0 {
			t.Errorf("expected no API calls with validate=false, got %v", validationFake.calls)
		}
	})

	t.Run("unknown value_type", func(t *testing.T) {
		t.Parallel()

		d := newSsmParameterValueDataSource(&fakeSSMParameterGetter{}, nil)
		config := ssmParameterValueConfig("/a")
		config.ValueType = types.StringValue("List<String>")
		_, resp := readDataSource(t, d, config)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected a list value_type to be rejected by the scalar data source")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "Invalid value_type") {
			t.Fatalf("expected an Invalid value_type diagnostic, got %s", text)
		}
	})
}

func TestSsmParameterValueDataSourceConstraints(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value         string
		model         func(m *SsmParameterValueDataSourceModel)
		errorContains string
	}{
		"allowed_pattern matches": {
			value: "abc",
			model: func(m *SsmParameterValueDataSourceModel) { m.AllowedPattern = types.StringValue("[a-z]+") },
		},
		"allowed_pattern fails": {
			value:         "abc1",
			model:         func(m *SsmParameterValueDataSourceModel) { m.AllowedPattern = types.StringValue("[a-z]+") },
			errorContains: "does not match `allowed_pattern`",
		},
		"allowed_values fails": {
			value:         "c",
			model:         func(m *SsmParameterValueDataSourceModel) { m.AllowedValues = stringListForTest("a", "b") },
			errorContains: "not one of `allowed_values`",
		},
		"invalid allowed_pattern is a config error": {
			value:         "abc",
			model:         func(m *SsmParameterValueDataSourceModel) { m.AllowedPattern = types.StringValue("[a-z") },
			errorContains: "not a valid regular expression",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a", ssmTypeString, testCase.value, 1)}
			d := newSsmParameterValueDataSource(ssmFake, nil)

			model := ssmParameterValueConfig("/a")
			testCase.model(&model)

			_, resp := readDataSource(t, d, model)
			if testCase.errorContains == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
				}
				return
			}
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected the read to fail")
			}
			if text := diagnosticsText(resp); !strings.Contains(text, testCase.errorContains) {
				t.Fatalf("expected diagnostics to contain %q, got %s", testCase.errorContains, text)
			}
		})
	}
}

// stringListForTest is stringList without a *testing.T, for use in table
// literals.
func stringListForTest(values ...string) types.List {
	list, diags := types.ListValueFrom(context.Background(), types.StringType, values)
	if diags.HasError() {
		panic(diags)
	}
	return list
}

func TestSsmParameterValueDataSourceVersionLabelConflict(t *testing.T) {
	t.Parallel()

	d := NewSsmParameterValueDataSource().(*SsmParameterValueDataSource)
	schema := dataSourceSchema(t, d)
	conflicting := ssmParameterValueConfig("/a")
	conflicting.Version = types.Int64Value(3)
	conflicting.Label = types.StringValue("prod")
	config := dataSourceConfig(t, schema, &conflicting)

	resp := &datasource.ValidateConfigResponse{}
	d.ValidateConfig(context.Background(), datasource.ValidateConfigRequest{Config: config}, resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected version + label to be rejected")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "Conflicting Arguments") {
		t.Fatalf("unexpected diagnostic: %v", resp.Diagnostics)
	}
}

func TestSsmParameterValueDataSourceNotConfigured(t *testing.T) {
	t.Parallel()

	t.Run("provider never configured", func(t *testing.T) {
		t.Parallel()

		d := &SsmParameterValueDataSource{}
		_, resp := readDataSource(t, d, ssmParameterValueConfig("/a"))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "Not Configured") {
			t.Fatalf("expected a Not Configured diagnostic, got %s", text)
		}
	})

	t.Run("AWS configuration failed", func(t *testing.T) {
		t.Parallel()

		d := &SsmParameterValueDataSource{
			providerData: &ProviderData{ConfigErr: errors.New("no credentials")},
		}
		_, resp := readDataSource(t, d, ssmParameterValueConfig("/a"))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		text := diagnosticsText(resp)
		if !strings.Contains(text, "AWS Configuration Required") || !strings.Contains(text, "no credentials") {
			t.Fatalf("expected the ConfigErr to be surfaced, got %s", text)
		}
	})

	t.Run("no SSM client", func(t *testing.T) {
		t.Parallel()

		d := &SsmParameterValueDataSource{providerData: &ProviderData{}}
		_, resp := readDataSource(t, d, ssmParameterValueConfig("/a"))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "bug in the cfncompat provider") {
			t.Fatalf("expected a provider-bug diagnostic, got %s", text)
		}
	})

	t.Run("GetParameter returned no parameter", func(t *testing.T) {
		t.Parallel()

		d := newSsmParameterValueDataSource(&fakeSSMParameterGetter{nilParameter: true}, nil)
		_, resp := readDataSource(t, d, ssmParameterValueConfig("/a"))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "returned no parameter") {
			t.Fatalf("expected a malformed-response diagnostic, got %s", text)
		}
	})
}

func TestSsmParameterValueDataSourceConfigure(t *testing.T) {
	t.Parallel()

	t.Run("nil ProviderData is not an error", func(t *testing.T) {
		t.Parallel()

		d := &SsmParameterValueDataSource{}
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.providerData != nil {
			t.Error("providerData should stay nil")
		}
	})

	t.Run("wrong ProviderData type is a provider bug", func(t *testing.T) {
		t.Parallel()

		d := &SsmParameterValueDataSource{}
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "nope"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error")
		}
		if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "bug in the cfncompat provider") {
			t.Fatalf("unexpected diagnostic: %v", resp.Diagnostics)
		}
	})

	t.Run("ConfigErr defers client construction to Read", func(t *testing.T) {
		t.Parallel()

		d := &SsmParameterValueDataSource{}
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{
			ProviderData: &ProviderData{ConfigErr: errors.New("boom")},
		}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Configure must not fail on ConfigErr: %v", resp.Diagnostics)
		}
		if d.clients.SSM != nil {
			t.Error("no SSM client should have been built")
		}
	})
}

// TestAccSsmParameterValueDataSource reads a public Systems Manager parameter
// that exists in every commercial region, so it needs no fixture setup: the
// Amazon Linux 2023 AMI id. It exercises the AWS::EC2::Image::Id value type
// with the existence check on, i.e. a real ec2:DescribeImages call, and both
// value_type modes.
//
// It deliberately does not exercise `version`: AWS's public /aws/service/...
// parameters report a version but reject a version selector against it
// (GetParameter answers ParameterVersionNotFound), so pinning is covered by
// the unit tests and by the operator-fixtured tests instead.
//
// Opt-in on top of TF_ACC, like the other real-AWS acceptance tests.
func TestAccSsmParameterValueDataSource(t *testing.T) {
	if os.Getenv("CFNCOMPAT_TEST_AWS") != "1" {
		t.Skip("set CFNCOMPAT_TEST_AWS=1 (with TF_ACC=1 and AWS credentials) to run this acceptance test")
	}

	rtresource.Test(t, rtresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []rtresource.TestStep{
			{
				Config: `
data "cfncompat_ssm_parameter_value" "ami" {
  name       = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
  value_type = "AWS::EC2::Image::Id"
}

data "cfncompat_ssm_parameter_value" "ami_no_validation" {
  name       = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
  value_type = "AWS::EC2::Image::Id"
  validate   = false
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestMatchResourceAttr("data.cfncompat_ssm_parameter_value.ami", "value",
						regexp.MustCompile(`^ami-[0-9a-f]{8,17}$`)),
					rtresource.TestCheckResourceAttr("data.cfncompat_ssm_parameter_value.ami", "type", "String"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_parameter_value.ami", "arn"),
					rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_parameter_value.ami", "resolved_version"),
					rtresource.TestMatchResourceAttr("data.cfncompat_ssm_parameter_value.ami", "last_modified_date",
						regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)),
					rtresource.TestCheckResourceAttrPair(
						"data.cfncompat_ssm_parameter_value.ami", "id",
						"data.cfncompat_ssm_parameter_value.ami", "arn",
					),
					// Both reads resolve the same value; only the validation differs.
					rtresource.TestCheckResourceAttrPair(
						"data.cfncompat_ssm_parameter_value.ami", "value",
						"data.cfncompat_ssm_parameter_value.ami_no_validation", "value",
					),
					rtresource.TestCheckResourceAttr("data.cfncompat_ssm_parameter_value.ami_no_validation", "validate", "false"),
				),
			},
			{
				// value_type unset is dynamic-reference mode, which also
				// accepts a StringList; the public parameter is a String, so
				// both modes resolve the same value here.
				Config: `
data "cfncompat_ssm_parameter_value" "dynamic_reference" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}
`,
				Check: rtresource.ComposeAggregateTestCheckFunc(
					rtresource.TestMatchResourceAttr("data.cfncompat_ssm_parameter_value.dynamic_reference", "value",
						regexp.MustCompile(`^ami-[0-9a-f]{8,17}$`)),
					rtresource.TestCheckResourceAttr("data.cfncompat_ssm_parameter_value.dynamic_reference", "type", "String"),
					rtresource.TestCheckNoResourceAttr("data.cfncompat_ssm_parameter_value.dynamic_reference", "value_type"),
				),
			},
			{
				// The value_type existence check is real: a syntactically
				// valid AMI id that does not exist fails, exactly as
				// CloudFormation fails it (live test T3c).
				Config: `
data "cfncompat_ssm_parameter_value" "wrong_type" {
  name       = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
  value_type = "AWS::EC2::Subnet::Id"
  validate   = false
}
`,
				ExpectError: regexp.MustCompile(`Resolved Value Is Not a Valid AWS::EC2::Subnet::Id`),
			},
			{
				// A missing parameter is a clear, actionable failure.
				Config: `
data "cfncompat_ssm_parameter_value" "missing" {
  name = "/cfncompat/acctest/definitely-does-not-exist"
}
`,
				ExpectError: regexp.MustCompile(`does not exist in this account and region`),
			},
		},
	})
}
