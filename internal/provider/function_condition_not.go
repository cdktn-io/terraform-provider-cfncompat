// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ConditionNotFunction satisfies the function.Function interface.
var _ function.Function = ConditionNotFunction{}

// NewConditionNotFunction returns a new instance of the
// provider::cfncompat::condition_not function.
func NewConditionNotFunction() function.Function {
	return ConditionNotFunction{}
}

// ConditionNotFunction implements CloudFormation's Fn::Not intrinsic
// function: it negates a single condition, returning true for a condition
// that evaluates to false and false for a condition that evaluates to true.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html
type ConditionNotFunction struct{}

func (f ConditionNotFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_not"
}

func (f ConditionNotFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Negate a boolean condition",
		Description: "Returns true for a condition that evaluates to false, or false for a condition that evaluates to true.",
		MarkdownDescription: "Returns `true` for a `condition` that evaluates to `false`, or `false` for a `condition` " +
			"that evaluates to `true`. `Fn::Not` acts as a NOT operator.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Not`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html#intrinsic-function-reference-conditions-not) " +
			"intrinsic function.\n\n" +
			"**Deviation from CloudFormation:** `Fn::Not` takes a single-element list containing the condition " +
			"(`\"Fn::Not\": [{{condition}}]`). As a provider-defined function, which does not support single-element " +
			"list arguments distinct from scalars, `condition_not` instead takes the condition directly as a single " +
			"boolean argument.",
		Parameters: []function.Parameter{
			function.BoolParameter{
				Name:                "condition",
				Description:         "A condition that evaluates to true or false.",
				MarkdownDescription: "A condition that evaluates to `true` or `false`.",
			},
		},
		Return: function.BoolReturn{},
	}
}

func (f ConditionNotFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var condition types.Bool

	resp.Error = req.Arguments.Get(ctx, &condition)
	if resp.Error != nil {
		return
	}

	if condition.IsUnknown() {
		return
	}

	if condition.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "condition_not: condition must not be null")
		return
	}

	resp.Error = resp.Result.Set(ctx, !condition.ValueBool())
}
