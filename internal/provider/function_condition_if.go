// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ConditionIfFunction satisfies the function.Function interface.
var _ function.Function = ConditionIfFunction{}

// NewConditionIfFunction returns a new instance of the
// provider::cfncompat::condition_if function.
func NewConditionIfFunction() function.Function {
	return ConditionIfFunction{}
}

// ConditionIfFunction implements CloudFormation's Fn::If intrinsic function:
// it returns one of two values depending on a condition.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html#intrinsic-function-reference-conditions-if
type ConditionIfFunction struct{}

func (f ConditionIfFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_if"
}

func (f ConditionIfFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Return one of two values depending on a condition",
		Description: "Returns value_if_true if condition is true, or value_if_false if condition is false. " +
			"value_if_true and value_if_false may be of different types.",
		MarkdownDescription: "Returns `value_if_true` if `condition` evaluates to `true`, or `value_if_false` if " +
			"`condition` evaluates to `false`.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::If`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html#intrinsic-function-reference-conditions-if) " +
			"intrinsic function.\n\n" +
			"**Deviation from `Fn::If`:** in a CloudFormation template, `Fn::If`'s first argument is the *name* of a " +
			"condition declared in the template's `Conditions` section (for example `!If [CreateNewSecurityGroup, ..., ...]`), " +
			"and CloudFormation resolves that name to a boolean by evaluating the referenced condition at stack " +
			"create/update time. This provider function has no template `Conditions` section to resolve names " +
			"against, so `condition` here is the already-evaluated **boolean value** of that condition — the CDK " +
			"Terrain synthesis backend is responsible for evaluating the named condition (typically itself built from " +
			"`provider::cfncompat::condition_*` function calls) and passing the resulting boolean through.\n\n" +
			"`value_if_true` and `value_if_false` accept any type and are not required to share a type with each " +
			"other, since only one of them is ever returned; the return type is dynamic to accommodate this. Only " +
			"the selected branch is validated (e.g. for unknown-ness) — the branch that is not selected is ignored " +
			"entirely, matching CloudFormation's lazy evaluation of `Fn::If` branches.",
		Parameters: []function.Parameter{
			function.BoolParameter{
				Name:                "condition",
				Description:         "The already-evaluated boolean value of the named condition Fn::If would reference in a template's Conditions section.",
				MarkdownDescription: "The already-evaluated boolean value of the named condition `Fn::If` would reference in a template's `Conditions` section.",
			},
			function.DynamicParameter{
				Name: "value_if_true",
				// AllowNullValue and AllowUnknownValues must both be enabled here: Terraform
				// core itself (not this function's Go code) rejects a null argument, or skips
				// calling Run entirely and returns an unknown result, for any parameter that
				// does not opt in -- regardless of which branch is ultimately selected. Without
				// these, the non-selected-branch-is-ignored behavior below (and the
				// AWS::NoValue null pass-through) could never be reached: Terraform would
				// short-circuit before Run was ever invoked whenever *either* branch was null
				// or unknown, not just the selected one.
				AllowNullValue:      true,
				AllowUnknownValues:  true,
				Description:         "The value to return if condition is true.",
				MarkdownDescription: "The value to return if `condition` is `true`.",
			},
			function.DynamicParameter{
				Name:                "value_if_false",
				AllowNullValue:      true,
				AllowUnknownValues:  true,
				Description:         "The value to return if condition is false.",
				MarkdownDescription: "The value to return if `condition` is `false`.",
			},
		},
		Return: function.DynamicReturn{},
	}
}

func (f ConditionIfFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var condition types.Bool
	var valueIfTrue, valueIfFalse types.Dynamic

	resp.Error = req.Arguments.Get(ctx, &condition, &valueIfTrue, &valueIfFalse)
	if resp.Error != nil {
		return
	}

	if condition.IsUnknown() {
		return
	}

	if condition.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "condition_if: condition must not be null")
		return
	}

	selected := valueIfFalse
	if condition.ValueBool() {
		selected = valueIfTrue
	}

	if selected.IsUnknown() || selected.IsUnderlyingValueUnknown() {
		return
	}

	resp.Error = resp.Result.Set(ctx, selected)
}
