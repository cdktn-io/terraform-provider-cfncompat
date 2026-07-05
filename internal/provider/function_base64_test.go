// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	tfversion "github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestBase64Function(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		content     string
		expected    string
		expectError bool
	}{
		// Official AWS documentation example:
		// { "Fn::Base64" : "AWS CloudFormation" }
		"doc example": {
			content:  "AWS CloudFormation",
			expected: "QVdTIENsb3VkRm9ybWF0aW9u",
		},
		"empty string": {
			content:  "",
			expected: "",
		},
		"simple ascii": {
			content:  "hello",
			expected: "aGVsbG8=",
		},
		"unicode / utf-8": {
			content:  "héllo wörld 🚀",
			expected: base64.StdEncoding.EncodeToString([]byte("héllo wörld 🚀")),
		},
		"newlines and whitespace": {
			content:  "line1\nline2\ttab",
			expected: base64.StdEncoding.EncodeToString([]byte("line1\nline2\ttab")),
		},
		"json-like userdata": {
			content:  `#!/bin/bash\necho "hi"`,
			expected: base64.StdEncoding.EncodeToString([]byte(`#!/bin/bash\necho "hi"`)),
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{
					types.StringValue(testCase.content),
				}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.StringUnknown()),
			}

			NewBase64Function().Run(context.Background(), req, &resp)

			if testCase.expectError {
				if resp.Error == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error)
			}

			got, ok := resp.Result.Value().(types.String)
			if !ok {
				t.Fatalf("unexpected result value type %T", resp.Result.Value())
			}

			if got.ValueString() != testCase.expected {
				t.Errorf("expected %q, got %q", testCase.expected, got.ValueString())
			}
		})
	}
}

// TestAccBase64Function verifies the cfncompat::base64 provider-defined
// function end-to-end against a real Terraform CLI. Gated by TF_ACC=1.
func TestAccBase64Function(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::cfncompat::base64("AWS CloudFormation")
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "QVdTIENsb3VkRm9ybWF0aW9u"),
				),
			},
			{
				Config: `
output "test" {
  value = provider::cfncompat::base64("")
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", ""),
				),
			},
		},
	})
}
