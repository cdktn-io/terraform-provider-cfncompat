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

// Ensure LengthFunction satisfies the function.Function interface.
var _ function.Function = LengthFunction{}

// NewLengthFunction returns a new instance of the provider::cfncompat::length
// function.
func NewLengthFunction() function.Function {
	return LengthFunction{}
}

// LengthFunction implements CloudFormation's Fn::Length intrinsic function:
// it returns the number of elements within an array.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-length.html
type LengthFunction struct{}

func (f LengthFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "length"
}

func (f LengthFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Return the number of elements in an array",
		Description: "Returns the number of elements within an array. value must be a list, tuple, or set; any other type is an error.",
		MarkdownDescription: "Returns the number of elements within an array.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Length`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-length.html) " +
			"intrinsic function, which **requires the `AWS::LanguageExtensions` transform** to be enabled on the " +
			"CloudFormation template in order to be used. AWS CDK exposes this intrinsic as `Fn.len`; the generated " +
			"CDKTN TypeScript binding for this provider function is `lengthOf` (`length` collides with the built-in " +
			"array/string property name in TypeScript/JavaScript).\n\n" +
			"`value` accepts a list, tuple, or set (CloudFormation's `Fn::Length` operates on the array produced by " +
			"a `Ref`, a nested intrinsic function such as `Fn::Split`, or a literal array). Any other underlying " +
			"type — a string, number, boolean, or map/object — is an error; the AWS documentation only documents " +
			"array inputs for this function, so this provider does not attempt to guess a length for non-array " +
			"values (e.g. treating a string's character count as its \"length\").",
		Parameters: []function.Parameter{
			function.DynamicParameter{
				Name:                "value",
				Description:         "The list, tuple, or set to count the elements of.",
				MarkdownDescription: "The list, tuple, or set to count the elements of.",
			},
		},
		Return: function.NumberReturn{},
	}
}

func (f LengthFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var value types.Dynamic

	resp.Error = req.Arguments.Get(ctx, &value)
	if resp.Error != nil {
		return
	}

	if value.IsUnknown() || value.IsUnderlyingValueUnknown() {
		return
	}

	if value.IsNull() || value.IsUnderlyingValueNull() {
		resp.Error = function.NewArgumentFuncError(0, "length: value must not be null")
		return
	}

	count, err := lengthCountElements(value.UnderlyingValue())
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf("length: %s", err))
		return
	}

	resp.Error = resp.Result.Set(ctx, types.NumberValue(new(big.Float).SetInt64(int64(count))))
}

// lengthCountElements returns the number of elements of a list-, tuple-, or
// set-shaped attr.Value, as found in the underlying value of the "value"
// dynamic parameter. It returns an error for any other underlying type, since
// CloudFormation's Fn::Length only documents array inputs.
func lengthCountElements(v attr.Value) (int, error) {
	switch val := v.(type) {
	case types.List:
		return len(val.Elements()), nil
	case types.Tuple:
		return len(val.Elements()), nil
	case types.Set:
		return len(val.Elements()), nil
	default:
		return 0, fmt.Errorf("value must be a list, tuple, or set, got %T", v)
	}
}
