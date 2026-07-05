// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"math/big"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	tfversion "github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// conditionIfStringList is a small test helper that builds a types.List of
// types.String values, used to model CloudFormation's "conditional array
// values" Fn::If doc example (differently-sized subnet import lists).
func conditionIfStringList(values ...string) types.List {
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}

	return types.ListValueMust(types.StringType, elems)
}

func TestConditionIfFunction(t *testing.T) {
	t.Parallel()

	// Models the "UpdatePolicy" doc example's AutoScalingRollingUpdate object,
	// to confirm arbitrary structural (object) values pass through unchanged.
	rollingUpdateObject := types.ObjectValueMust(
		map[string]attr.Type{
			"max_batch_size":           types.NumberType,
			"min_instances_in_service": types.NumberType,
			"pause_time":               types.StringType,
		},
		map[string]attr.Value{
			"max_batch_size":           types.NumberValue(big.NewFloat(2)),
			"min_instances_in_service": types.NumberValue(big.NewFloat(2)),
			"pause_time":               types.StringValue("PT0M30S"),
		},
	)

	tests := map[string]struct {
		condition     types.Bool
		valueIfTrue   types.Dynamic
		valueIfFalse  types.Dynamic
		expected      types.Dynamic
		expectUnknown bool
		expectError   string
	}{
		// Official AWS doc example ("Conditionally choosing a resource" /
		// sample template): InstanceType is c5.xlarge when CreateProdResources
		// is true, t3.small otherwise.
		"doc example: instance type, condition true selects value_if_true": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicValue(types.StringValue("c5.xlarge")),
			valueIfFalse: types.DynamicValue(types.StringValue("t3.small")),
			expected:     types.DynamicValue(types.StringValue("c5.xlarge")),
		},
		"doc example: instance type, condition false selects value_if_false": {
			condition:    types.BoolValue(false),
			valueIfTrue:  types.DynamicValue(types.StringValue("c5.xlarge")),
			valueIfFalse: types.DynamicValue(types.StringValue("t3.small")),
			expected:     types.DynamicValue(types.StringValue("t3.small")),
		},
		// Official AWS doc example ("Conditional properties and property
		// values"): AllocatedStorage is 100 when IsProduction is true, 20
		// otherwise.
		"doc example: allocated storage, condition true selects 100": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicValue(types.NumberValue(big.NewFloat(100))),
			valueIfFalse: types.DynamicValue(types.NumberValue(big.NewFloat(20))),
			expected:     types.DynamicValue(types.NumberValue(big.NewFloat(100))),
		},
		"doc example: allocated storage, condition false selects 20": {
			condition:    types.BoolValue(false),
			valueIfTrue:  types.DynamicValue(types.NumberValue(big.NewFloat(100))),
			valueIfFalse: types.DynamicValue(types.NumberValue(big.NewFloat(20))),
			expected:     types.DynamicValue(types.NumberValue(big.NewFloat(20))),
		},
		// Official AWS doc example ("Conditional array values"): three
		// subnets when MoreThan2AZs is true, two otherwise.
		"doc example: conditional array values, condition true selects 3-element list": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicValue(conditionIfStringList("PublicSubnet01", "PublicSubnet02", "PublicSubnet03")),
			valueIfFalse: types.DynamicValue(conditionIfStringList("PublicSubnet01", "PublicSubnet02")),
			expected:     types.DynamicValue(conditionIfStringList("PublicSubnet01", "PublicSubnet02", "PublicSubnet03")),
		},
		"doc example: conditional array values, condition false selects 2-element list": {
			condition:    types.BoolValue(false),
			valueIfTrue:  types.DynamicValue(conditionIfStringList("PublicSubnet01", "PublicSubnet02", "PublicSubnet03")),
			valueIfFalse: types.DynamicValue(conditionIfStringList("PublicSubnet01", "PublicSubnet02")),
			expected:     types.DynamicValue(conditionIfStringList("PublicSubnet01", "PublicSubnet02")),
		},
		// Official AWS doc example ("Conditional update policies"): an
		// AutoScalingRollingUpdate object, showing arbitrary structural
		// (object) values are passed through unchanged.
		"doc example: update policy object, condition true selects the object": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicValue(rollingUpdateObject),
			valueIfFalse: types.DynamicValue(types.StringNull()),
			expected:     types.DynamicValue(rollingUpdateObject),
		},
		// The two branch values need not share a type, since only one is
		// ever returned (this is why the function's return type is dynamic).
		"branch values of different types, condition true selects the string": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicValue(types.StringValue("a")),
			valueIfFalse: types.DynamicValue(types.NumberValue(big.NewFloat(5))),
			expected:     types.DynamicValue(types.StringValue("a")),
		},
		"branch values of different types, condition false selects the number": {
			condition:    types.BoolValue(false),
			valueIfTrue:  types.DynamicValue(types.StringValue("a")),
			valueIfFalse: types.DynamicValue(types.NumberValue(big.NewFloat(5))),
			expected:     types.DynamicValue(types.NumberValue(big.NewFloat(5))),
		},
		"boolean branch values": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicValue(types.BoolValue(true)),
			valueIfFalse: types.DynamicValue(types.BoolValue(false)),
			expected:     types.DynamicValue(types.BoolValue(true)),
		},
		// A null selected branch models CloudFormation's AWS::NoValue idiom
		// (Fn::If used to remove a property); condition_if simply passes the
		// already-resolved value through, whatever it is, including null.
		"null value_if_true is returned as-is when selected": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicNull(),
			valueIfFalse: types.DynamicValue(types.StringValue("existing-sg")),
			expected:     types.DynamicNull(),
		},
		"null value_if_false is returned as-is when selected": {
			condition:    types.BoolValue(false),
			valueIfTrue:  types.DynamicValue(types.StringValue("new-sg")),
			valueIfFalse: types.DynamicNull(),
			expected:     types.DynamicNull(),
		},
		// The non-selected branch is never evaluated/validated: it may be
		// null or unknown without affecting the result.
		"unknown value_if_false does not affect selecting value_if_true": {
			condition:    types.BoolValue(true),
			valueIfTrue:  types.DynamicValue(types.StringValue("selected")),
			valueIfFalse: types.DynamicUnknown(),
			expected:     types.DynamicValue(types.StringValue("selected")),
		},
		"unknown value_if_true does not affect selecting value_if_false": {
			condition:    types.BoolValue(false),
			valueIfTrue:  types.DynamicUnknown(),
			valueIfFalse: types.DynamicValue(types.StringValue("selected")),
			expected:     types.DynamicValue(types.StringValue("selected")),
		},
		// When the selected branch itself is unknown, the result is unknown
		// (matches Terraform's usual unknown propagation).
		"unknown value_if_true is unknown when selected": {
			condition:     types.BoolValue(true),
			valueIfTrue:   types.DynamicUnknown(),
			valueIfFalse:  types.DynamicValue(types.StringValue("known")),
			expectUnknown: true,
		},
		"unknown value_if_false is unknown when selected": {
			condition:     types.BoolValue(false),
			valueIfTrue:   types.DynamicValue(types.StringValue("known")),
			valueIfFalse:  types.DynamicUnknown(),
			expectUnknown: true,
		},
		// An unknown condition means we don't know which branch will be
		// selected at all, so the whole result is unknown.
		"unknown condition is unknown regardless of branch values": {
			condition:     types.BoolUnknown(),
			valueIfTrue:   types.DynamicValue(types.StringValue("true-branch")),
			valueIfFalse:  types.DynamicValue(types.StringValue("false-branch")),
			expectUnknown: true,
		},
		"null condition is an error": {
			condition:    types.BoolNull(),
			valueIfTrue:  types.DynamicValue(types.StringValue("true-branch")),
			valueIfFalse: types.DynamicValue(types.StringValue("false-branch")),
			expectError:  "condition_if: condition must not be null",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := function.RunRequest{
				Arguments: function.NewArgumentsData([]attr.Value{tc.condition, tc.valueIfTrue, tc.valueIfFalse}),
			}
			resp := function.RunResponse{
				Result: function.NewResultData(types.DynamicUnknown()),
			}

			NewConditionIfFunction().Run(context.Background(), req, &resp)

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

			got, ok := resp.Result.Value().(types.Dynamic)
			if !ok {
				t.Fatalf("expected result of type types.Dynamic, got %T", resp.Result.Value())
			}

			if tc.expectUnknown {
				if !got.IsUnknown() {
					t.Fatalf("expected unknown result, got %#v", got)
				}
				return
			}

			if !got.Equal(tc.expected) {
				t.Fatalf("expected %#v, got %#v", tc.expected, got)
			}
		})
	}
}

// TestAccConditionIfFunction is an acceptance test exercising
// provider::cfncompat::condition_if through a real Terraform CLI run. It is
// gated by TF_ACC=1 and is not run as part of `make test`; it is registered
// here for the batch acceptance run after provider.go wiring.
func TestAccConditionIfFunction(t *testing.T) {
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_if(true, "c5.xlarge", "t3.small")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "c5.xlarge"),
				),
			},
			{
				Config: `
				output "test" {
				  value = provider::cfncompat::condition_if(false, "c5.xlarge", "t3.small")
				}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("test", "t3.small"),
				),
			},
		},
	})
}
