// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	tfversion "github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestConditionNotFunction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		condition   types.Bool
		expected    bool
		expectError string
	}{
		// Official AWS doc example: MyNotCondition evaluates to true when the
		// negated condition (EnvironmentType == prod) is false, i.e. when
		// EnvironmentType is NOT prod. Fn::Not [Fn::Equals] is pre-resolved by
		// the synthesis backend to the boolean result of the inner Fn::Equals
		// before being passed to this provider function.
		"doc example: Fn::Not negates a false Fn::Equals result to true": {
			condition: types.BoolValue(false),
			expected:  true,
		},
		"doc example: Fn::Not negates a true Fn::Equals result to false": {
			condition: types.BoolValue(true),
			expected:  false,
		},
		"negating true returns false": {
			condition: types.BoolValue(true),
			expected:  false,
		},
		"negating false returns true": {
			condition: types.BoolValue(false),
			expected:  true,
		},
		"null condition is an error": {
			condition:   types.BoolNull(),
			expectError: "condition_not: condition must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.condition}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.BoolUnknown()),
			}

			NewConditionNotFunction().Run(context.Background(), req, &resp)

			if tc.expectError != "" {
				if resp.Error == nil {
					t.Fatalf("expected error %q, got nil", tc.expectError)
				}
				if resp.Error.Text != tc.expectError {
					t.Fatalf("expected error %q, got %q", tc.expectError, resp.Error.Text)
				}
				return
			}

			if resp.Error != nil {
				t.Fatalf("unexpected error: %s", resp.Error.Text)
			}

			got, ok := resp.Result.Value().(types.Bool)
			if !ok {
				t.Fatalf("expected result of type types.Bool, got %T", resp.Result.Value())
			}

			if got.ValueBool() != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got.ValueBool())
			}
		})
	}
}

// unknownConditionResultsInUnknown verifies that an unknown condition value
// (e.g. known only after apply) short-circuits to an unknown, error-free
// result rather than being evaluated.
func TestConditionNotFunction_unknownCondition(t *testing.T) {
	t.Parallel()

	req := function.RunRequest{
		Arguments: function.NewArgumentsData([]attr.Value{types.BoolUnknown()}),
	}
	resp := function.RunResponse{
		Result: function.NewResultData(types.BoolUnknown()),
	}

	NewConditionNotFunction().Run(context.Background(), req, &resp)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Text)
	}

	got, ok := resp.Result.Value().(types.Bool)
	if !ok {
		t.Fatalf("expected result of type types.Bool, got %T", resp.Result.Value())
	}

	if !got.IsUnknown() {
		t.Fatalf("expected unknown result, got %v", got)
	}
}

// TestAccConditionNotFunction is an acceptance test exercising
// provider::cfncompat::condition_not through a real Terraform CLI run. It is
// gated by TF_ACC=1 and is not run as part of `make test`; it is registered
// here for the batch acceptance run after provider.go wiring.
func TestAccConditionNotFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_not(false)
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "true"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_not(true)
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "false"),
				),
			},
		},
	})
}
