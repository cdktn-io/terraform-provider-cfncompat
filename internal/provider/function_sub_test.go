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
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestSubFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		template  string
		variables map[string]attr.Value
		expected  string
		wantErr   string
	}{
		// Doc example 1: "Use Fn::Sub without a key-value map". In a real
		// CloudFormation template ${AWS::StackName} is resolved automatically
		// via the pseudo parameter; here the caller must supply it explicitly
		// as a variables map entry.
		"doc example without key-value map": {
			template: "SSH security group for ${AWS::StackName}",
			variables: map[string]attr.Value{
				"AWS::StackName": types.StringValue("VPC-EC2-ALB-Stack"),
			},
			expected: "SSH security group for VPC-EC2-ALB-Stack",
		},
		// Doc example 2: "Use Fn::Sub with a key-value map".
		"doc example with key-value map": {
			template: "www.${Domain}",
			variables: map[string]attr.Value{
				"Domain": types.StringValue("mydomain.com"),
			},
			expected: "www.mydomain.com",
		},
		// Doc example 3: "Use multiple variables to construct ARNs".
		"doc example multiple variables ARN": {
			template: "arn:aws:ec2:${AWS::Region}:${AWS::AccountId}:vpc/${vpc}",
			variables: map[string]attr.Value{
				"AWS::Region":    types.StringValue("us-east-1"),
				"AWS::AccountId": types.StringValue("123456789012"),
				"vpc":            types.StringValue("vpc-1a2b3c4d"),
			},
			expected: "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-1a2b3c4d",
		},
		// Doc example 4: "Pass parameter values in user data scripts" (single line extracted).
		"doc example user data script line": {
			template: "/opt/aws/bin/cfn-init -v --stack ${AWS::StackName} --resource LaunchConfig --configsets wordpress_install --region ${AWS::Region}",
			variables: map[string]attr.Value{
				"AWS::StackName": types.StringValue("wordpress-stack"),
				"AWS::Region":    types.StringValue("us-west-2"),
			},
			expected: "/opt/aws/bin/cfn-init -v --stack wordpress-stack --resource LaunchConfig --configsets wordpress_install --region us-west-2",
		},
		// Doc example 5: "Specify conditional values using mappings" (Fn::FindInMap
		// result pre-resolved into the variables map by the caller).
		"doc example conditional values using mappings": {
			template: "cloud_watch_${log_group_name}",
			variables: map[string]attr.Value{
				"log_group_name": types.StringValue("test_log_group"),
			},
			expected: "cloud_watch_test_log_group",
		},
		// Escape syntax: "${!Literal}" resolves to the literal text "${Literal}".
		"escape produces literal dollar-brace text": {
			template:  "${!Literal}",
			variables: map[string]attr.Value{},
			expected:  "${Literal}",
		},
		"escape mixed with real substitution": {
			template: "${!Foo} and ${Bar}",
			variables: map[string]attr.Value{
				"Bar": types.StringValue("baz"),
			},
			expected: "${Foo} and baz",
		},
		"multiple escapes in sequence": {
			template:  "${!A}${!B}",
			variables: map[string]attr.Value{},
			expected:  "${A}${B}",
		},
		"escape with dots in name": {
			template:  "${!MyInstance.PublicIp}",
			variables: map[string]attr.Value{},
			expected:  "${MyInstance.PublicIp}",
		},
		// No recursive substitution: substituted values are not re-scanned.
		"substituted value is not re-scanned for further substitution": {
			template: "${X}",
			variables: map[string]attr.Value{
				"X": types.StringValue("${Y}"),
			},
			expected: "${Y}",
		},
		// Plain text edge cases.
		"empty template": {
			template:  "",
			variables: map[string]attr.Value{},
			expected:  "",
		},
		"plain text with no placeholders": {
			template:  "plain text, nothing to substitute",
			variables: map[string]attr.Value{"unused": types.StringValue("x")},
			expected:  "plain text, nothing to substitute",
		},
		"dollar sign not followed by open brace is literal": {
			template:  "cost is $5, not a placeholder",
			variables: map[string]attr.Value{},
			expected:  "cost is $5, not a placeholder",
		},
		"empty variables map with no placeholders used": {
			template:  "no vars here",
			variables: map[string]attr.Value{},
			expected:  "no vars here",
		},
		"empty string variable value": {
			template: "prefix-${Suffix}",
			variables: map[string]attr.Value{
				"Suffix": types.StringValue(""),
			},
			expected: "prefix-",
		},
		"placeholder at start and end of template": {
			template: "${A}-middle-${B}",
			variables: map[string]attr.Value{
				"A": types.StringValue("start"),
				"B": types.StringValue("end"),
			},
			expected: "start-middle-end",
		},
		// Error conditions.
		"error when variable not found in map": {
			template:  "hello ${Name}",
			variables: map[string]attr.Value{},
			wantErr:   `template references variable "Name" which is not present in variables`,
		},
		"error when only some variables are resolvable": {
			template: "${Known} ${Unknown}",
			variables: map[string]attr.Value{
				"Known": types.StringValue("ok"),
			},
			wantErr: `template references variable "Unknown" which is not present in variables`,
		},
		"error on unterminated substitution": {
			template:  "hello ${Name",
			variables: map[string]attr.Value{},
			wantErr:   "unterminated substitution",
		},
		"error on empty placeholder name": {
			template:  "hello ${}",
			variables: map[string]attr.Value{},
			wantErr:   "empty substitution",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			variablesValue := types.MapValueMust(types.StringType, tc.variables)

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{
					types.StringValue(tc.template),
					variablesValue,
				}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.StringUnknown()),
			}

			NewSubFunction().Run(context.Background(), req, &resp)

			if tc.wantErr != "" {
				if resp.Error == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(resp.Error.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, resp.Error.Error())
				}
				return
			}

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Error())
			}

			got, ok := resp.Result.Value().(types.String)
			if !ok {
				t.Fatalf("unexpected result value type %T", resp.Result.Value())
			}

			if got.ValueString() != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got.ValueString())
			}
		})
	}
}

func TestAccSubFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				  output "test" {
				    value = provider::cfncompat::sub(
				      "arn:aws:ec2:$${AWS::Region}:$${AWS::AccountId}:vpc/$${vpc}",
				      {
				        "AWS::Region"    = "us-east-1"
				        "AWS::AccountId" = "123456789012"
				        "vpc"            = "vpc-1a2b3c4d"
				      }
				    )
				  }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-1a2b3c4d"),
				),
			},
			{
				Config: `
				  output "test" {
				    value = provider::cfncompat::sub("literal is $${!Escaped}", {})
				  }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "literal is ${Escaped}"),
				),
			},
		},
	})
}
