// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ConditionOrFunction satisfies the function.Function interface.
var _ function.Function = ConditionOrFunction{}

// NewConditionOrFunction returns a new instance of the
// provider::cfncompat::condition_or function.
func NewConditionOrFunction() function.Function {
	return ConditionOrFunction{}
}

// ConditionOrFunction implements CloudFormation's Fn::Or intrinsic function:
// it returns true if any one of the specified conditions evaluates to true,
// or false if all the conditions evaluate to false.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html
type ConditionOrFunction struct{}

func (f ConditionOrFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_or"
}

func (f ConditionOrFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Logical OR of 2 to 10 conditions",
		Description: "Returns true if any one of the specified conditions evaluates to true, or returns false if " +
			"all the conditions evaluate to false. Acts as an OR operator. The minimum number of conditions is 2, " +
			"and the maximum is 10.",
		MarkdownDescription: "Returns `true` if any one of the specified `conditions` evaluates to `true`, or " +
			"returns `false` if all the conditions evaluate to `false`. Acts as an OR operator.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Or`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html#intrinsic-function-reference-conditions-or) " +
			"intrinsic function (CDK binding: `Fn.conditionOr`).\n\n" +
			"CloudFormation's `Fn::Or` takes a list of condition expressions (references to named `Conditions`, or " +
			"nested condition functions) that CloudFormation evaluates lazily. This provider function instead takes " +
			"already-evaluated boolean values as its `conditions` - the synthesis backend is responsible for " +
			"evaluating any nested condition expressions to booleans before calling this function.\n\n" +
			"The minimum number of `conditions` you can include is 2, and the maximum is 10.",
		VariadicParameter: function.BoolParameter{
			Name:                "conditions",
			Description:         "A condition that evaluates to true or false. Minimum 2, maximum 10.",
			MarkdownDescription: "A condition that evaluates to `true` or `false`. Minimum 2, maximum 10.",
		},
		Return: function.BoolReturn{},
	}
}

func (f ConditionOrFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var conditions []types.Bool

	resp.Error = req.Arguments.Get(ctx, &conditions)
	if resp.Error != nil {
		return
	}

	if len(conditions) < 2 || len(conditions) > 10 {
		resp.Error = function.NewFuncError(fmt.Sprintf(
			"condition_or: expected between 2 and 10 conditions, got %d", len(conditions),
		))
		return
	}

	result := false

	for i, cond := range conditions {
		if cond.IsUnknown() {
			return
		}

		if cond.IsNull() {
			resp.Error = function.NewArgumentFuncError(int64(i), fmt.Sprintf("condition_or: conditions[%d] must not be null", i))
			return
		}

		if cond.ValueBool() {
			result = true
		}
	}

	resp.Error = resp.Result.Set(ctx, result)
}
