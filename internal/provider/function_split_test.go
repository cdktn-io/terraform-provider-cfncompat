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

func TestSplitFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		delimiter     string
		source        string
		expected      []string
		expectError   bool
		errorContains string
	}{
		// Official AWS doc example: "Simple list".
		"doc example: simple list": {
			delimiter: "|",
			source:    "a|b|c",
			expected:  []string{"a", "b", "c"},
		},
		// Official AWS doc example: "List with empty string values".
		"doc example: consecutive and trailing delimiters": {
			delimiter: "|",
			source:    "a||c|",
			expected:  []string{"a", "", "c", ""},
		},
		// Official AWS doc example context: comma-delimited subnet IDs.
		"doc example: comma delimited subnet ids": {
			delimiter: ",",
			source:    "subnet-1,subnet-2,subnet-3",
			expected:  []string{"subnet-1", "subnet-2", "subnet-3"},
		},
		"no delimiter occurrences returns single element list": {
			delimiter: ",",
			source:    "abc",
			expected:  []string{"abc"},
		},
		"empty source string returns single empty element": {
			delimiter: ",",
			source:    "",
			expected:  []string{""},
		},
		"delimiter at start produces leading empty element": {
			delimiter: ",",
			source:    ",a,b",
			expected:  []string{"", "a", "b"},
		},
		"delimiter at end produces trailing empty element": {
			delimiter: ",",
			source:    "a,b,",
			expected:  []string{"a", "b", ""},
		},
		"source entirely delimiters produces all empty elements": {
			delimiter: ",",
			source:    ",,",
			expected:  []string{"", "", ""},
		},
		"multi-character delimiter": {
			delimiter: "::",
			source:    "a::b::c",
			expected:  []string{"a", "b", "c"},
		},
		"delimiter not present in source returns whole source": {
			delimiter: ";",
			source:    "a,b,c",
			expected:  []string{"a,b,c"},
		},
		"empty delimiter is an error": {
			delimiter:     "",
			source:        "abc",
			expectError:   true,
			errorContains: "delimiter must not be an empty string",
		},
		"empty delimiter with empty source is still an error": {
			delimiter:     "",
			source:        "",
			expectError:   true,
			errorContains: "delimiter must not be an empty string",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{
					types.StringValue(testCase.delimiter),
					types.StringValue(testCase.source),
				}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.ListNull(types.StringType)),
			}

			NewSplitFunction().Run(context.Background(), req, &resp)

			if testCase.expectError {
				if resp.Error == nil {
					t.Fatalf("expected error, got none")
				}
				if testCase.errorContains != "" && !strings.Contains(resp.Error.Error(), testCase.errorContains) {
					t.Fatalf("expected error to contain %q, got %q", testCase.errorContains, resp.Error.Error())
				}
				return
			}

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Error())
			}

			resultList, ok := resp.Result.Value().(types.List)
			if !ok {
				t.Fatalf("expected result value to be types.List, got %T", resp.Result.Value())
			}

			var got []string
			diags := resultList.ElementsAs(context.Background(), &got, false)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics converting result: %s", diags)
			}

			if len(got) != len(testCase.expected) {
				t.Fatalf("expected %d elements %v, got %d elements %v", len(testCase.expected), testCase.expected, len(got), got)
			}
			for i := range got {
				if got[i] != testCase.expected[i] {
					t.Fatalf("expected element %d to be %q, got %q (full expected=%v got=%v)", i, testCase.expected[i], got[i], testCase.expected, got)
				}
			}
		})
	}
}

// TestAccSplitFunction is gated by TF_ACC=1 and exercises provider::cfncompat::split
// through real Terraform CLI runs. It is not run by this agent (the function is not
// yet registered in provider.go); it is run centrally after registration.
func TestAccSplitFunction(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				  output "simple_list" {
				    value = provider::cfncompat::split("|", "a|b|c")
				  }

				  output "consecutive_delimiters" {
				    value = provider::cfncompat::split("|", "a||c|")
				  }
				`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("simple_list", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("a"),
						knownvalue.StringExact("b"),
						knownvalue.StringExact("c"),
					})),
					statecheck.ExpectKnownOutputValue("consecutive_delimiters", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("a"),
						knownvalue.StringExact(""),
						knownvalue.StringExact("c"),
						knownvalue.StringExact(""),
					})),
				},
			},
		},
	})
}
