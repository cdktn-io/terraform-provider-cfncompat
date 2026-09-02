// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	tfversion "github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// runParseDynamicReference drives the function the way Terraform does and
// returns the resulting object, or the error.
func runParseDynamicReference(t *testing.T, reference string) (types.Object, *function.FuncError) {
	t.Helper()

	req := function.RunRequest{
		Arguments: function.NewArgumentsData([]attr.Value{types.StringValue(reference)}),
	}
	resp := function.RunResponse{
		Result: function.NewResultData(types.ObjectNull(parseDynamicReferenceAttributeTypes)),
	}

	NewParseDynamicReferenceFunction().Run(context.Background(), req, &resp)
	if resp.Error != nil {
		return types.ObjectNull(parseDynamicReferenceAttributeTypes), resp.Error
	}

	object, ok := resp.Result.Value().(types.Object)
	if !ok {
		t.Fatalf("expected result value to be types.Object, got %T", resp.Result.Value())
	}
	return object, nil
}

func TestParseDynamicReferenceFunction(t *testing.T) {
	t.Parallel()

	// want maps attribute name -> expected value; a missing key means null.
	tests := map[string]struct {
		reference string
		want      map[string]string
	}{
		"ssm without version": {
			reference: "{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64}}",
			want: map[string]string{
				"service": "ssm",
				"name":    "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64",
			},
		},
		"ssm with version": {
			reference: "{{resolve:ssm:golden-ami:2}}",
			want:      map[string]string{"service": "ssm", "name": "golden-ami", "version": "2"},
		},
		"ssm-secure with version": {
			reference: "{{resolve:ssm-secure:IAMUserPassword:10}}",
			want:      map[string]string{"service": "ssm-secure", "name": "IAMUserPassword", "version": "10"},
		},
		"secretsmanager whole secret": {
			reference: "{{resolve:secretsmanager:MySecret}}",
			want:      map[string]string{"service": "secretsmanager", "name": "MySecret"},
		},
		"secretsmanager json key of a previous stage": {
			reference: "{{resolve:secretsmanager:MySecret:SecretString:password:AWSPREVIOUS}}",
			want: map[string]string{
				"service": "secretsmanager", "name": "MySecret",
				"secret_string": "SecretString", "json_key": "password", "version_stage": "AWSPREVIOUS",
			},
		},
		"secretsmanager cross-account ARN": {
			reference: "{{resolve:secretsmanager:arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret:SecretString:username}}",
			want: map[string]string{
				"service":       "secretsmanager",
				"name":          "arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret",
				"secret_string": "SecretString",
				"json_key":      "username",
			},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			object, funcErr := runParseDynamicReference(t, testCase.reference)
			if funcErr != nil {
				t.Fatalf("unexpected error: %s", funcErr.Error())
			}

			attributes := object.Attributes()
			if len(attributes) != len(parseDynamicReferenceAttributeTypes) {
				t.Fatalf("result has %d attributes, want the fixed set of %d", len(attributes), len(parseDynamicReferenceAttributeTypes))
			}

			for attribute := range parseDynamicReferenceAttributeTypes {
				value, ok := attributes[attribute].(types.String)
				if !ok {
					t.Fatalf("attribute %q is %T, want types.String", attribute, attributes[attribute])
				}
				want, present := testCase.want[attribute]
				if !present {
					if !value.IsNull() {
						t.Errorf("attribute %q = %q, want null", attribute, value.ValueString())
					}
					continue
				}
				if value.IsNull() {
					t.Errorf("attribute %q is null, want %q", attribute, want)
					continue
				}
				if value.ValueString() != want {
					t.Errorf("attribute %q = %q, want %q", attribute, value.ValueString(), want)
				}
			}
		})
	}
}

func TestParseDynamicReferenceFunctionErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		reference     string
		errorContains string
	}{
		"not a reference":      {reference: "just-a-string", errorContains: "is not a CloudFormation dynamic reference"},
		"embedded in text":     {reference: "ami-{{resolve:ssm:/a}}", errorContains: "synthesis backend's job"},
		"two references":       {reference: "{{resolve:ssm:/a}}{{resolve:ssm:/b}}", errorContains: "more than one dynamic reference"},
		"unknown service":      {reference: "{{resolve:s3:bucket}}", errorContains: "not a CloudFormation dynamic reference service"},
		"ssm label":            {reference: "{{resolve:ssm:/a:prod}}", errorContains: "labels are not supported"},
		"truncated secret ARN": {reference: "{{resolve:secretsmanager:arn:aws:secretsmanager:us-west-2:1:secret}}", errorContains: "not a complete Secrets Manager secret ARN"},
		"stage and id both given": {reference: "{{resolve:secretsmanager:S:SecretString:k:AWSCURRENT:v1}}",
			errorContains: "don't specify the other"},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, funcErr := runParseDynamicReference(t, testCase.reference)
			if funcErr == nil {
				t.Fatalf("expected an error for %q, got none", testCase.reference)
			}
			if !strings.Contains(funcErr.Error(), testCase.errorContains) {
				t.Fatalf("expected error to contain %q, got %q", testCase.errorContains, funcErr.Error())
			}
			// Every failure blames the one argument the function takes.
			if funcErr.FunctionArgument == nil || *funcErr.FunctionArgument != 0 {
				t.Errorf("expected the error to be attributed to argument 0, got %v", funcErr.FunctionArgument)
			}
		})
	}
}

func TestIsDynamicReferenceFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		reference string
		want      bool
	}{
		"whole ssm reference":            {reference: "{{resolve:ssm:/a/b}}", want: true},
		"whole ssm-secure reference":     {reference: "{{resolve:ssm-secure:/a/b:3}}", want: true},
		"whole secretsmanager reference": {reference: "{{resolve:secretsmanager:MySecret:SecretString:password}}", want: true},
		"plain string":                   {reference: "/a/b", want: false},
		"embedded reference":             {reference: "x{{resolve:ssm:/a/b}}", want: false},
		"empty string":                   {reference: "", want: false},
		"malformed":                      {reference: "{{resolve:ssm:}}", want: false},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{types.StringValue(testCase.reference)}),
			}
			resp := function.RunResponse{Result: function.NewResultData(types.BoolNull())}

			NewIsDynamicReferenceFunction().Run(context.Background(), req, &resp)
			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Error())
			}

			got, ok := resp.Result.Value().(types.Bool)
			if !ok {
				t.Fatalf("expected result value to be types.Bool, got %T", resp.Result.Value())
			}
			if got.ValueBool() != testCase.want {
				t.Fatalf("is_dynamic_reference(%q) = %v, want %v", testCase.reference, got.ValueBool(), testCase.want)
			}
		})
	}
}

// TestAccParseDynamicReferenceFunction exercises both dynamic-reference
// functions through real Terraform CLI runs. It needs no AWS credentials:
// both functions are pure.
func TestAccParseDynamicReferenceFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
output "ssm" {
  value = provider::cfncompat::parse_dynamic_reference("{{resolve:ssm:golden-ami:2}}")
}

output "secret" {
  value = provider::cfncompat::parse_dynamic_reference("{{resolve:secretsmanager:arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret:SecretString:password}}")
}

output "is_ref" {
  value = provider::cfncompat::is_dynamic_reference("{{resolve:ssm-secure:/db/password:4}}")
}

output "is_not_ref" {
  value = provider::cfncompat::is_dynamic_reference("plain-string")
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("ssm", knownvalue.ObjectExact(map[string]knownvalue.Check{
						"service":       knownvalue.StringExact("ssm"),
						"name":          knownvalue.StringExact("golden-ami"),
						"version":       knownvalue.StringExact("2"),
						"secret_string": knownvalue.Null(),
						"json_key":      knownvalue.Null(),
						"version_stage": knownvalue.Null(),
						"version_id":    knownvalue.Null(),
					})),
					statecheck.ExpectKnownOutputValue("secret", knownvalue.ObjectExact(map[string]knownvalue.Check{
						"service":       knownvalue.StringExact("secretsmanager"),
						"name":          knownvalue.StringExact("arn:aws:secretsmanager:us-west-2:123456789012:secret:MySecret"),
						"version":       knownvalue.Null(),
						"secret_string": knownvalue.StringExact("SecretString"),
						"json_key":      knownvalue.StringExact("password"),
						"version_stage": knownvalue.Null(),
						"version_id":    knownvalue.Null(),
					})),
					statecheck.ExpectKnownOutputValue("is_ref", knownvalue.Bool(true)),
					statecheck.ExpectKnownOutputValue("is_not_ref", knownvalue.Bool(false)),
				},
			},
		},
	})
}
