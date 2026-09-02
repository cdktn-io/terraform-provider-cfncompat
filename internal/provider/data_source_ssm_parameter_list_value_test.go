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

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	rtresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func newSsmParameterListValueDataSource(ssmFake *fakeSSMParameterGetter, validationFake *fakeCFNValidationAPI) *SsmParameterListValueDataSource {
	d := &SsmParameterListValueDataSource{
		providerData: &ProviderData{Region: "us-east-1"},
		clients:      ssmDataSourceClients{SSM: ssmFake},
	}
	if validationFake != nil {
		d.clients.Validator = fakeValidator(validationFake)
	}
	return d
}

// ssmParameterListValueConfig is the base config model for a read.
func ssmParameterListValueConfig(name string) SsmParameterListValueDataSourceModel {
	return SsmParameterListValueDataSourceModel{
		Name:          types.StringValue(name),
		AllowedValues: types.ListNull(types.StringType),
		// Computed too: the test harness round-trips the whole model through
		// a tfsdk value, and every list needs its element type.
		Values: types.ListNull(types.StringType),
	}
}

// stateValues reads the resolved list out of the state model.
func stateValues(t *testing.T, list types.List) []string {
	t.Helper()
	var out []string
	if diags := list.ElementsAs(context.Background(), &out, false); diags.HasError() {
		t.Fatalf("unexpected diagnostics reading values: %v", diags)
	}
	return out
}

func TestSsmParameterListValueDataSourceRead(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		parameterType string
		raw           string
		want          []string
	}{
		"StringList": {
			ssmTypeStringList, "subnet-1,subnet-2,subnet-3",
			[]string{"subnet-1", "subnet-2", "subnet-3"},
		},
		// The live-test case: CloudFormation's typed List<...> resolution
		// trims each element ("a,b, c ,d" -> a|b|c|d), while the
		// dynamic-reference path does not.
		"each member string is whitespace-trimmed, as CloudFormation's typed list resolution does": {
			ssmTypeStringList, "a,b, c ,d", []string{"a", "b", "c", "d"},
		},
		"a single element is a one-element list": {
			ssmTypeStringList, "only", []string{"only"},
		},
		"an empty value is a one-element list containing the empty string": {
			ssmTypeStringList, "", []string{""},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ssmFake := &fakeSSMParameterGetter{
				parameter: ssmParameter("/net/subnets", testCase.parameterType, testCase.raw, 3),
			}
			d := newSsmParameterListValueDataSource(ssmFake, nil)

			state, resp := readDataSource(t, d, ssmParameterListValueConfig("/net/subnets"))
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
			}

			if got := stateValues(t, state.Values); !equalStrings(got, testCase.want) {
				t.Errorf("values = %v, want %v", got, testCase.want)
			}
			if got := state.RawValue.ValueString(); got != testCase.raw {
				t.Errorf("raw_value = %q, want the value exactly as stored (%q)", got, testCase.raw)
			}
			if got := state.Type.ValueString(); got != testCase.parameterType {
				t.Errorf("type = %q, want %q", got, testCase.parameterType)
			}
			if got := state.ValueType.ValueString(); got != cfnValueTypeListString {
				t.Errorf("value_type = %q, want the %q default", got, cfnValueTypeListString)
			}
			if got := state.ResolvedVersion.ValueInt64(); got != 3 {
				t.Errorf("resolved_version = %d, want 3", got)
			}
		})
	}
}

// TestSsmParameterListValueDataSourceRejectsNonStringList pins the strict
// type check CloudFormation performs: it compares the parameter's *declared*
// Systems Manager type against the template's shape and ignores the content,
// so a String parameter cannot satisfy a list-shaped type however many commas
// its value holds.
func TestSsmParameterListValueDataSourceRejectsNonStringList(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		parameterType string
		value         string
		wantContains  []string
	}{
		"a String parameter, even one holding commas": {
			ssmTypeString, "x,y,z",
			[]string{"defined in CFN template and SSM are incompatible", "cfncompat_ssm_parameter_value"},
		},
		"a SecureString": {
			ssmTypeSecureString, "a,b",
			[]string{"cfncompat_ssm_secure_parameter_value"},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a", testCase.parameterType, testCase.value, 1)}
			d := newSsmParameterListValueDataSource(ssmFake, nil)

			_, resp := readDataSource(t, d, ssmParameterListValueConfig("/a"))
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

func TestSsmParameterListValueDataSourceValueTypes(t *testing.T) {
	t.Parallel()

	t.Run("List<AWS::EC2::Subnet::Id> validates every element in one call", func(t *testing.T) {
		t.Parallel()

		ssmFake := &fakeSSMParameterGetter{
			parameter: ssmParameter("/net/subnets", ssmTypeStringList, "subnet-11111111,subnet-22222222", 1),
		}
		validationFake := &fakeCFNValidationAPI{subnets: []string{"subnet-11111111", "subnet-22222222"}}
		d := newSsmParameterListValueDataSource(ssmFake, validationFake)

		config := ssmParameterListValueConfig("/net/subnets")
		config.ValueType = types.StringValue("List<AWS::EC2::Subnet::Id>")
		state, resp := readDataSource(t, d, config)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
		}
		if got := len(stateValues(t, state.Values)); got != 2 {
			t.Errorf("values has %d elements, want 2", got)
		}
		if validationFake.calls["DescribeSubnets"] != 1 {
			t.Errorf("expected one batched DescribeSubnets call, got %v", validationFake.calls)
		}
	})

	t.Run("a missing element fails the read", func(t *testing.T) {
		t.Parallel()

		ssmFake := &fakeSSMParameterGetter{
			parameter: ssmParameter("/net/subnets", ssmTypeStringList, "subnet-11111111,subnet-22222222", 1),
		}
		validationFake := &fakeCFNValidationAPI{subnets: []string{"subnet-11111111"}}
		d := newSsmParameterListValueDataSource(ssmFake, validationFake)

		config := ssmParameterListValueConfig("/net/subnets")
		config.ValueType = types.StringValue("List<AWS::EC2::Subnet::Id>")
		_, resp := readDataSource(t, d, config)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected the read to fail")
		}
		if text := diagnosticsText(resp); !strings.Contains(text, "subnet-22222222") {
			t.Fatalf("expected the diagnostic to name the missing subnet, got %s", text)
		}
	})

	t.Run("CommaDelimitedList applies no type validation", func(t *testing.T) {
		t.Parallel()

		ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a", ssmTypeStringList, "x,y", 1)}
		validationFake := &fakeCFNValidationAPI{}
		d := newSsmParameterListValueDataSource(ssmFake, validationFake)

		config := ssmParameterListValueConfig("/a")
		config.ValueType = types.StringValue(cfnValueTypeCommaDelimitedList)
		state, resp := readDataSource(t, d, config)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %s", diagnosticsText(resp))
		}
		if got := state.ValueType.ValueString(); got != cfnValueTypeCommaDelimitedList {
			t.Errorf("value_type = %q", got)
		}
		if len(validationFake.calls) != 0 {
			t.Errorf("expected no API calls, got %v", validationFake.calls)
		}
	})

	t.Run("a scalar value_type is rejected", func(t *testing.T) {
		t.Parallel()

		d := newSsmParameterListValueDataSource(&fakeSSMParameterGetter{}, nil)
		config := ssmParameterListValueConfig("/a")
		config.ValueType = types.StringValue(cfnValueTypeString)
		_, resp := readDataSource(t, d, config)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected a scalar value_type to be rejected by the list data source")
		}
	})

	t.Run("List<AWS::EC2::KeyPair::KeyName> does not exist in CloudFormation", func(t *testing.T) {
		t.Parallel()

		d := newSsmParameterListValueDataSource(&fakeSSMParameterGetter{}, nil)
		config := ssmParameterListValueConfig("/a")
		config.ValueType = types.StringValue("List<AWS::EC2::KeyPair::KeyName>")
		_, resp := readDataSource(t, d, config)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected List<AWS::EC2::KeyPair::KeyName> to be rejected")
		}
	})
}

func TestSsmParameterListValueDataSourceConstraintsArePerElement(t *testing.T) {
	t.Parallel()

	ssmFake := &fakeSSMParameterGetter{parameter: ssmParameter("/a", ssmTypeStringList, "a,b,zz", 1)}
	d := newSsmParameterListValueDataSource(ssmFake, nil)

	config := ssmParameterListValueConfig("/a")
	config.AllowedValues = stringListForTest("a", "b")
	_, resp := readDataSource(t, d, config)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected the third element to violate allowed_values")
	}
	if text := diagnosticsText(resp); !strings.Contains(text, "at index 2") {
		t.Fatalf("expected the diagnostic to name the failing element, got %s", text)
	}
}

func TestSsmParameterListValueDataSourceVersionLabelConflict(t *testing.T) {
	t.Parallel()

	d, ok := NewSsmParameterListValueDataSource().(*SsmParameterListValueDataSource)
	if !ok {
		t.Fatal("NewSsmParameterListValueDataSource did not return a *SsmParameterListValueDataSource")
	}
	conflicting := ssmParameterListValueConfig("/a")
	conflicting.Version = types.Int64Value(3)
	conflicting.Label = types.StringValue("prod")
	config := dataSourceConfig(t, dataSourceSchema(t, d), &conflicting)

	resp := &datasource.ValidateConfigResponse{}
	d.ValidateConfig(context.Background(), datasource.ValidateConfigRequest{Config: config}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected version + label to be rejected")
	}
}

func TestSsmParameterListValueDataSourceConfigErr(t *testing.T) {
	t.Parallel()

	d := &SsmParameterListValueDataSource{providerData: &ProviderData{ConfigErr: errors.New("no credentials")}}
	_, resp := readDataSource(t, d, ssmParameterListValueConfig("/a"))
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected the read to fail")
	}
	if text := diagnosticsText(resp); !strings.Contains(text, "no credentials") {
		t.Fatalf("expected the ConfigErr to be surfaced, got %s", text)
	}
}

// TestAccSsmParameterListValueDataSource has two halves. The fixture-free one
// asserts the strict type check against the public AMI parameter, which is a
// String: CloudFormation rejects a String behind a list-shaped parameter type
// (live test T4b/T4c) and so does this data source. The positive half needs a
// StringList parameter, which AWS publishes none of, so the operator names one:
//
//	aws ssm put-parameter --name /cfncompat/acctest/list --type StringList \
//	  --value 'a,b, c ,d'
//	aws ssm delete-parameter --name /cfncompat/acctest/list
func TestAccSsmParameterListValueDataSource(t *testing.T) {
	if os.Getenv("CFNCOMPAT_TEST_AWS") != "1" {
		t.Skip("set CFNCOMPAT_TEST_AWS=1 (with TF_ACC=1 and AWS credentials) to run this acceptance test")
	}

	steps := []rtresource.TestStep{
		{
			Config: `
data "cfncompat_ssm_parameter_list_value" "not_a_string_list" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}
`,
			ExpectError: regexp.MustCompile(`defined in CFN template and SSM are incompatible`),
		},
	}

	if name := os.Getenv("CFNCOMPAT_TEST_SSM_STRINGLIST_NAME"); name != "" {
		steps = append(steps, rtresource.TestStep{
			Config: `
data "cfncompat_ssm_parameter_list_value" "list" {
  name = "` + name + `"
}
`,
			Check: rtresource.ComposeAggregateTestCheckFunc(
				rtresource.TestCheckResourceAttr("data.cfncompat_ssm_parameter_list_value.list", "type", "StringList"),
				rtresource.TestCheckResourceAttr("data.cfncompat_ssm_parameter_list_value.list", "value_type", "List<String>"),
				rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_parameter_list_value.list", "values.0"),
				rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_parameter_list_value.list", "raw_value"),
				rtresource.TestCheckResourceAttrSet("data.cfncompat_ssm_parameter_list_value.list", "arn"),
			),
		})
	}

	rtresource.Test(t, rtresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    steps,
	})
}
