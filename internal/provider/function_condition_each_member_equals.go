// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ConditionEachMemberEqualsFunction satisfies the function.Function
// interface.
var _ function.Function = ConditionEachMemberEqualsFunction{}

// NewConditionEachMemberEqualsFunction returns a new instance of the
// provider::cfncompat::condition_each_member_equals function.
func NewConditionEachMemberEqualsFunction() function.Function {
	return ConditionEachMemberEqualsFunction{}
}

// ConditionEachMemberEqualsFunction implements CloudFormation's rule-specific
// Fn::EachMemberEquals intrinsic function: it returns true if every member of
// a list of strings equals a given string.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-rules.html#fn-eachmemberequals
type ConditionEachMemberEqualsFunction struct{}

func (f ConditionEachMemberEqualsFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_each_member_equals"
}

func (f ConditionEachMemberEqualsFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Check that every member of a list of strings equals a given value",
		Description: "Returns true if every member of list_of_strings equals value, mirroring CloudFormation's " +
			"rule-specific Fn::EachMemberEquals intrinsic function. An empty list_of_strings returns true " +
			"(vacuous truth), since AWS's documentation does not specify empty-list behavior.",
		MarkdownDescription: "Returns `true` if every member of `list_of_strings` equals `value`, mirroring " +
			"CloudFormation's rule-specific " +
			"[`Fn::EachMemberEquals`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-rules.html#fn-eachmemberequals) " +
			"intrinsic function.\n\n" +
			"AWS's documentation does not specify the behavior when `list_of_strings` is empty. This provider " +
			"chooses vacuous truth: an empty list returns `true`, since \"every member equals value\" is trivially " +
			"satisfied when there are no members.\n\n" +
			"In CloudFormation, `Fn::EachMemberEquals` is a Rules-section-only function typically used with " +
			"`Fn::ValueOfAll` to validate that a tag or attribute is consistent across every AWS-specific " +
			"parameter of a given type; this provider-function form operates directly on an already-resolved " +
			"list of strings rather than on template Rules-section constructs like `Fn::ValueOfAll`.",
		Parameters: []function.Parameter{
			function.ListParameter{
				ElementType:         types.StringType,
				Name:                "list_of_strings",
				Description:         "The list of strings to compare against value. Every member must equal value for the function to return true. An empty list returns true.",
				MarkdownDescription: "The list of strings to compare against `value`. Every member must equal `value` for the function to return `true`. An empty list returns `true`.",
			},
			function.StringParameter{
				Name:                "value",
				Description:         "The string value that every member of list_of_strings is compared against.",
				MarkdownDescription: "The string value that every member of `list_of_strings` is compared against.",
			},
		},
		Return: function.BoolReturn{},
	}
}

func (f ConditionEachMemberEqualsFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var listOfStrings types.List
	var value types.String

	resp.Error = req.Arguments.Get(ctx, &listOfStrings, &value)
	if resp.Error != nil {
		return
	}

	if listOfStrings.IsUnknown() || value.IsUnknown() {
		return
	}

	if listOfStrings.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "condition_each_member_equals: list_of_strings must not be null")
		return
	}

	if value.IsNull() {
		resp.Error = function.NewArgumentFuncError(1, "condition_each_member_equals: value must not be null")
		return
	}

	members, err := conditionEachMemberEqualsElements(listOfStrings)
	if err != nil {
		resp.Error = err
		return
	}

	if members == nil {
		// An element is unknown; the overall result is not yet determinable.
		return
	}

	result := conditionEachMemberEqualsAllEqual(members, value.ValueString())

	resp.Error = resp.Result.Set(ctx, result)
}

// conditionEachMemberEqualsElements extracts the plain string members of
// list_of_strings, returning an ArgumentFuncError if any member is null. It
// returns a nil slice (with a nil error) if any element is unknown,
// signaling that the result cannot yet be determined.
func conditionEachMemberEqualsElements(list types.List) ([]string, *function.FuncError) {
	elements := list.Elements()
	members := make([]string, 0, len(elements))

	for i, elem := range elements {
		strVal, ok := elem.(types.String)
		if !ok {
			return nil, function.NewArgumentFuncError(0, "condition_each_member_equals: list_of_strings must contain only string values")
		}

		if strVal.IsUnknown() {
			return nil, nil
		}

		if strVal.IsNull() {
			return nil, function.NewArgumentFuncError(0, "condition_each_member_equals: list_of_strings["+strconv.Itoa(i)+"] must not be null")
		}

		members = append(members, strVal.ValueString())
	}

	return members, nil
}

// conditionEachMemberEqualsAllEqual returns true if every member of members
// equals value. An empty members slice returns true (vacuous truth).
func conditionEachMemberEqualsAllEqual(members []string, value string) bool {
	for _, member := range members {
		if member != value {
			return false
		}
	}

	return true
}
