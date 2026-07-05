// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// conditionAndMinConditions and conditionAndMaxConditions mirror the
// documented Fn::And arity: "the minimum number of conditions that you can
// include is 2, and the maximum is 10".
const (
	conditionAndMinConditions = 2
	conditionAndMaxConditions = 10
)

// Ensure ConditionAndFunction satisfies the function.Function interface.
var _ function.Function = ConditionAndFunction{}

// NewConditionAndFunction returns a new instance of the
// provider::cfncompat::condition_and function.
func NewConditionAndFunction() function.Function {
	return ConditionAndFunction{}
}

// ConditionAndFunction implements CloudFormation's Fn::And intrinsic
// function: it returns true if all the specified conditions evaluate to
// true, or false if any one of the conditions evaluates to false.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html
type ConditionAndFunction struct{}

func (f ConditionAndFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_and"
}

func (f ConditionAndFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Logical AND of 2 to 10 conditions",
		Description: "Returns true if all the specified conditions evaluate to true, or returns false if any one of " +
			"the conditions evaluates to false. Acts as an AND operator. The minimum number of conditions is 2, and " +
			"the maximum is 10.",
		MarkdownDescription: "Returns `true` if all the specified `conditions` evaluate to `true`, or returns " +
			"`false` if any one of the conditions evaluates to `false`. Acts as an AND operator.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::And`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html#intrinsic-function-reference-conditions-and) " +
			"intrinsic function (CDK binding: `Fn.conditionAnd`).\n\n" +
			"CloudFormation's `Fn::And` takes a list of condition expressions (references to named `Conditions`, or " +
			"nested condition functions) that CloudFormation evaluates lazily. This provider function instead takes " +
			"already-evaluated boolean values as its `conditions` — the synthesis backend is responsible for " +
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

func (f ConditionAndFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var conditions []types.Bool

	resp.Error = req.Arguments.Get(ctx, &conditions)
	if resp.Error != nil {
		return
	}

	if len(conditions) < conditionAndMinConditions || len(conditions) > conditionAndMaxConditions {
		resp.Error = function.NewFuncError(fmt.Sprintf(
			"condition_and: expected between %d and %d conditions, got %d",
			conditionAndMinConditions, conditionAndMaxConditions, len(conditions),
		))
		return
	}

	result := true

	for i, cond := range conditions {
		if cond.IsUnknown() {
			return
		}

		if cond.IsNull() {
			resp.Error = function.NewArgumentFuncError(int64(i), fmt.Sprintf("condition_and: conditions[%d] must not be null", i))
			return
		}

		if !cond.ValueBool() {
			result = false
		}
	}

	resp.Error = resp.Result.Set(ctx, result)
}
