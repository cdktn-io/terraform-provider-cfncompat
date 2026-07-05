// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"math/big"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure ConditionEqualsFunction satisfies the function.Function interface.
var _ function.Function = ConditionEqualsFunction{}

// NewConditionEqualsFunction returns a new instance of the
// provider::cfncompat::condition_equals function.
func NewConditionEqualsFunction() function.Function {
	return ConditionEqualsFunction{}
}

// ConditionEqualsFunction implements CloudFormation's Fn::Equals intrinsic
// function: it compares two primitive values (string, number, or boolean)
// and returns true if they are equal.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html#intrinsic-function-reference-conditions-equals
type ConditionEqualsFunction struct{}

func (f ConditionEqualsFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "condition_equals"
}

func (f ConditionEqualsFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Compare two values for equality",
		Description: "Returns true if value_1 and value_2 are equal, or false if they aren't.",
		MarkdownDescription: "Compares if two values are equal. Returns `true` if the two values are equal, or `false` " +
			"if they aren't.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Equals`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-conditions.html#intrinsic-function-reference-conditions-equals) " +
			"intrinsic function.\n\n" +
			"**Coercion rule:** CloudFormation template values are fundamentally strings, so `value_1` and `value_2` " +
			"are compared by their canonical string representation rather than by native type: a string compares " +
			"as-is, a number compares by its canonical decimal representation (so `1`, `1.0`, and `\"1\"` are all " +
			"equal, and `-0` is equal to `0`), and a boolean compares as the literal `true` or `false`. `value_1` and " +
			"`value_2` must each be a string, number, or boolean, and must not be null; non-primitive arguments " +
			"(list, set, map/object, or tuple) and null arguments produce an error.",
		Parameters: []function.Parameter{
			function.DynamicParameter{
				Name:                "value_1",
				Description:         "The first value to compare. Must be a string, number, or boolean.",
				MarkdownDescription: "The first value to compare. Must be a string, number, or boolean.",
			},
			function.DynamicParameter{
				Name:                "value_2",
				Description:         "The second value to compare. Must be a string, number, or boolean.",
				MarkdownDescription: "The second value to compare. Must be a string, number, or boolean.",
			},
		},
		Return: function.BoolReturn{},
	}
}

func (f ConditionEqualsFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var value1, value2 types.Dynamic

	resp.Error = req.Arguments.Get(ctx, &value1, &value2)
	if resp.Error != nil {
		return
	}

	if value1.IsUnknown() || value1.IsUnderlyingValueUnknown() || value2.IsUnknown() || value2.IsUnderlyingValueUnknown() {
		return
	}

	str1, err := conditionEqualsCanonicalString(value1, int64(0), "value_1")
	if err != nil {
		resp.Error = err
		return
	}

	str2, err := conditionEqualsCanonicalString(value2, int64(1), "value_2")
	if err != nil {
		resp.Error = err
		return
	}

	resp.Error = resp.Result.Set(ctx, str1 == str2)
}

// conditionEqualsCanonicalString converts the underlying value of a dynamic
// condition_equals argument to its canonical string representation: a
// string as-is, a number as its canonical decimal representation, and a
// boolean as the literal "true" or "false". Non-primitive underlying types
// (list, set, map/object, tuple) and null values produce an error.
func conditionEqualsCanonicalString(value types.Dynamic, argPos int64, argName string) (string, *function.FuncError) {
	if value.IsNull() || value.IsUnderlyingValueNull() {
		return "", function.NewArgumentFuncError(argPos, fmt.Sprintf("condition_equals: %s must not be null", argName))
	}

	underlying := value.UnderlyingValue()

	switch val := underlying.(type) {
	case types.String:
		return val.ValueString(), nil
	case types.Bool:
		if val.ValueBool() {
			return "true", nil
		}
		return "false", nil
	case types.Number:
		bf := val.ValueBigFloat()
		if bf == nil {
			return "", function.NewArgumentFuncError(argPos, fmt.Sprintf("condition_equals: %s must not be null", argName))
		}
		return conditionEqualsCanonicalNumber(bf), nil
	default:
		return "", function.NewArgumentFuncError(argPos, fmt.Sprintf(
			"condition_equals: %s must be a string, number, or boolean; got %s", argName, conditionEqualsTypeDescription(underlying),
		))
	}
}

// conditionEqualsTypeDescription returns a short, human-readable name for
// the kind of a non-primitive underlying value, for use in error messages.
func conditionEqualsTypeDescription(value attr.Value) string {
	switch value.(type) {
	case types.List:
		return "list"
	case types.Set:
		return "set"
	case types.Map:
		return "map"
	case types.Object:
		return "object"
	case types.Tuple:
		return "tuple"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// conditionEqualsCanonicalNumber renders a big.Float as its canonical
// decimal representation: fixed-point (never scientific notation), the
// shortest decimal representation that round-trips (no trailing zeros), and
// a normalized "0" for any zero value (so "-0" compares equal to "0").
func conditionEqualsCanonicalNumber(bf *big.Float) string {
	if bf.Sign() == 0 {
		return "0"
	}

	return bf.Text('f', -1)
}
