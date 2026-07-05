// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ConditionEachMemberInFunction satisfies the function.Function
// interface.
var _ function.Function = ConditionEachMemberInFunction{}

// NewConditionEachMemberInFunction returns a new instance of the
// provider::cfncompat::condition_each_member_in function.
func NewConditionEachMemberInFunction() function.Function {
	return ConditionEachMemberInFunction{}
}

// ConditionEachMemberInFunction implements CloudFormation's Fn::EachMemberIn
// rule-specific intrinsic function: it returns true if each member of
// strings_to_check is present in strings_to_match.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-rules.html#fn-eachmemberin
type ConditionEachMemberInFunction struct{}

func (f ConditionEachMemberInFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_each_member_in"
}

func (f ConditionEachMemberInFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Check that every member of a list of strings is present in a second list of strings",
		Description: "Returns true if each member in strings_to_check is present in strings_to_match, and false " +
			"otherwise. If strings_to_check is empty, returns true (vacuous truth: there are no members that fail " +
			"to match).",
		MarkdownDescription: "Returns `true` if each member in `strings_to_check` is present in `strings_to_match`, " +
			"and `false` otherwise.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::EachMemberIn`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-rules.html#fn-eachmemberin) " +
			"rule-specific intrinsic function.\n\n" +
			"**Empty `strings_to_check`:** the AWS documentation does not specify a result for an empty " +
			"`strings_to_check` list. This implementation returns `true` (vacuous truth: there are no members of " +
			"`strings_to_check` that fail to appear in `strings_to_match`), matching how universally-quantified " +
			"checks over an empty set are conventionally defined.\n\n" +
			"**Deviation from CloudFormation:** in a CloudFormation template, `Fn::EachMemberIn` is normally used " +
			"together with `Fn::ValueOfAll` and `Fn::RefAll` to compare attribute values across every parameter of " +
			"a given AWS-specific parameter type. Those two functions depend on template-wide `Parameters` " +
			"declarations and stack-account/Region context that this provider-defined function form has no access " +
			"to; callers must resolve `strings_to_check` and `strings_to_match` to concrete lists of strings " +
			"themselves before calling this function.",
		Parameters: []function.Parameter{
			function.ListParameter{
				Name:                "strings_to_check",
				ElementType:         types.StringType,
				Description:         "A list of strings. Every member of this list is checked for membership in strings_to_match.",
				MarkdownDescription: "A list of strings. Every member of this list is checked for membership in `strings_to_match`.",
			},
			function.ListParameter{
				Name:                "strings_to_match",
				ElementType:         types.StringType,
				Description:         "A list of strings that each member of strings_to_check is compared against.",
				MarkdownDescription: "A list of strings that each member of `strings_to_check` is compared against.",
			},
		},
		Return: function.BoolReturn{},
	}
}

func (f ConditionEachMemberInFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var stringsToCheck types.List
	var stringsToMatch types.List

	resp.Error = req.Arguments.Get(ctx, &stringsToCheck, &stringsToMatch)
	if resp.Error != nil {
		return
	}

	if stringsToCheck.IsUnknown() || stringsToMatch.IsUnknown() {
		return
	}

	if stringsToCheck.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "condition_each_member_in: strings_to_check must not be null")
		return
	}

	if stringsToMatch.IsNull() {
		resp.Error = function.NewArgumentFuncError(1, "condition_each_member_in: strings_to_match must not be null")
		return
	}

	checkValues, err := conditionEachMemberInStringSlice(stringsToCheck, 0, "strings_to_check")
	if err != nil {
		resp.Error = err
		return
	}
	if checkValues == nil {
		// An element is unknown; the overall result is not yet determinable.
		return
	}

	matchValues, err := conditionEachMemberInStringSlice(stringsToMatch, 1, "strings_to_match")
	if err != nil {
		resp.Error = err
		return
	}
	if matchValues == nil {
		return
	}

	matchSet := make(map[string]struct{}, len(matchValues))
	for _, v := range matchValues {
		matchSet[v] = struct{}{}
	}

	result := true
	for _, v := range checkValues {
		if _, ok := matchSet[v]; !ok {
			result = false
			break
		}
	}

	resp.Error = resp.Result.Set(ctx, result)
}

// conditionEachMemberInStringSlice extracts the string values of a
// types.List argument at position argPos (named argName for error messages),
// erroring if any element is null. It returns a nil slice (with a nil error)
// if any element is unknown, signaling that the result cannot yet be
// determined.
func conditionEachMemberInStringSlice(list types.List, argPos int64, argName string) ([]string, *function.FuncError) {
	elements := list.Elements()
	values := make([]string, 0, len(elements))

	for i, elem := range elements {
		strVal, ok := elem.(types.String)
		if !ok {
			return nil, function.NewArgumentFuncError(argPos, fmt.Sprintf(
				"condition_each_member_in: %s[%d] has unexpected element type %T", argName, i, elem,
			))
		}

		if strVal.IsUnknown() {
			return nil, nil
		}

		if strVal.IsNull() {
			return nil, function.NewArgumentFuncError(argPos, fmt.Sprintf(
				"condition_each_member_in: %s[%d] must not be null", argName, i,
			))
		}

		values = append(values, strVal.ValueString())
	}

	return values, nil
}
