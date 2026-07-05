// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ConditionContainsFunction satisfies the function.Function interface.
var _ function.Function = ConditionContainsFunction{}

// NewConditionContainsFunction returns a new instance of the
// provider::cfncompat::condition_contains function.
func NewConditionContainsFunction() function.Function {
	return ConditionContainsFunction{}
}

// ConditionContainsFunction implements CloudFormation's Fn::Contains
// rule-specific intrinsic function: it returns true if a specified string
// matches at least one value in a list of strings.
//
// Fn::Contains is a rule function, only valid in the "RuleCondition" or
// "Assert" fields of a template's Rules section (not a general-purpose
// intrinsic usable in Resources/Outputs). See:
// https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-rules.html#fn-contains
// https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/rules-section-structure.html#template-constraint-rules-syntax
type ConditionContainsFunction struct{}

func (f ConditionContainsFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_contains"
}

func (f ConditionContainsFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Check whether a list of strings contains a value",
		Description: "Returns true if value matches at least one member of list_of_strings.",
		MarkdownDescription: "Returns `true` if `value` matches at least one member of `list_of_strings`.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Contains`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-rules.html#fn-contains) " +
			"function. CloudFormation classifies `Fn::Contains` as a " +
			"[rule-specific intrinsic function](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/rules-section-structure.html#template-constraint-rules-syntax): " +
			"in a template it is only valid within the `RuleCondition` or `Assert` fields of the template's `Rules` " +
			"section, not as a general-purpose intrinsic used in `Resources` or `Outputs`. As a " +
			"`provider::cfncompat::*` function it has no such placement restriction and can be called anywhere " +
			"a boolean value is needed.\n\n" +
			"`list_of_strings` must not be null and must not contain null entries.",
		Parameters: []function.Parameter{
			function.ListParameter{
				Name:                "list_of_strings",
				ElementType:         types.StringType,
				Description:         "The list of strings to search.",
				MarkdownDescription: "The list of strings to search.",
			},
			function.StringParameter{
				Name:                "value",
				Description:         "The string to compare against the list of strings.",
				MarkdownDescription: "The string to compare against the list of strings.",
			},
		},
		Return: function.BoolReturn{},
	}
}

func (f ConditionContainsFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var listOfStrings types.List
	var value types.String

	resp.Error = req.Arguments.Get(ctx, &listOfStrings, &value)
	if resp.Error != nil {
		return
	}

	if listOfStrings.IsUnknown() {
		return
	}

	if listOfStrings.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "condition_contains: list_of_strings must not be null")
		return
	}

	if value.IsUnknown() {
		return
	}

	if value.IsNull() {
		resp.Error = function.NewArgumentFuncError(1, "condition_contains: value must not be null")
		return
	}

	elements := listOfStrings.Elements()

	for i, elem := range elements {
		strVal, ok := elem.(types.String)
		if !ok {
			resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf("condition_contains: list_of_strings[%d] has unexpected element type %T", i, elem))
			return
		}

		if strVal.IsUnknown() {
			return
		}

		if strVal.IsNull() {
			resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf("condition_contains: list_of_strings[%d] must not be null", i))
			return
		}
	}

	resp.Error = resp.Result.Set(ctx, conditionContainsMatch(elements, value.ValueString()))
}

// conditionContainsMatch reports whether value equals at least one of the
// (already null/unknown-checked) string elements.
func conditionContainsMatch(elements []attr.Value, value string) bool {
	for _, elem := range elements {
		strVal, ok := elem.(types.String)
		if !ok {
			continue
		}

		if strVal.ValueString() == value {
			return true
		}
	}

	return false
}
