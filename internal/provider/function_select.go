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

// Ensure SelectFunction satisfies the function.Function interface.
var _ function.Function = SelectFunction{}

// NewSelectFunction returns a new instance of the provider::cfncompat::select
// function.
func NewSelectFunction() function.Function {
	return SelectFunction{}
}

// SelectFunction implements CloudFormation's Fn::Select intrinsic function:
// it returns a single object from a list of objects by index.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-select.html
type SelectFunction struct{}

func (f SelectFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "select"
}

func (f SelectFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Select an object from a list of objects by index",
		Description: "Returns a single object from a list of objects by index. index must be from zero to N-1, where N is the number of elements in the array. objects must not be null and must not contain null entries.",
		MarkdownDescription: "Returns a single object from a list of objects by `index`.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Select`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-select.html) intrinsic function.\n\n" +
			"`index` must be a value from zero to N-1, where N represents the number of elements in `objects`. " +
			"`objects` must not be null, nor can it have null entries; CloudFormation documents both conditions as " +
			"resulting in a stack error, so this function returns an error rather than a partial/best-effort result.\n\n" +
			"`objects` accepts either a list or a tuple, so it can represent both CloudFormation's homogeneous lists " +
			"(e.g. a resolved `CommaDelimitedList` parameter) and heterogeneous tuples produced by other " +
			"`provider::cfncompat::*` functions. The return type is dynamic because tuple elements may be of any type.",
		Parameters: []function.Parameter{
			function.Int64Parameter{
				Name:                "index",
				Description:         "The index of the object to retrieve, from zero to N-1 where N is the number of elements in objects.",
				MarkdownDescription: "The index of the object to retrieve, from zero to N-1 where N is the number of elements in `objects`.",
			},
			function.DynamicParameter{
				Name:                "objects",
				Description:         "The list (or tuple) of objects to select from. Must not be null, nor can it have null entries.",
				MarkdownDescription: "The list (or tuple) of objects to select from. Must not be null, nor can it have null entries.",
			},
		},
		Return: function.DynamicReturn{},
	}
}

func (f SelectFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var index types.Int64
	var objects types.Dynamic

	resp.Error = req.Arguments.Get(ctx, &index, &objects)
	if resp.Error != nil {
		return
	}

	if index.IsUnknown() || objects.IsUnknown() || objects.IsUnderlyingValueUnknown() {
		return
	}

	if index.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "select: index must not be null")
		return
	}

	if objects.IsNull() || objects.IsUnderlyingValueNull() {
		resp.Error = function.NewArgumentFuncError(1, "select: objects must not be null")
		return
	}

	elements, err := selectElements(objects.UnderlyingValue())
	if err != nil {
		resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf("select: %s", err))
		return
	}

	idx := index.ValueInt64()

	for i, elem := range elements {
		if elem.IsNull() {
			resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf("select: objects[%d] must not be null", i))
			return
		}
	}

	if idx < 0 || idx >= int64(len(elements)) {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf(
			"select: index %d out of range for objects of length %d (must be between 0 and %d)",
			idx, len(elements), len(elements)-1,
		))
		return
	}

	resp.Error = resp.Result.Set(ctx, types.DynamicValue(elements[idx]))
}

// selectElements returns the ordered elements of a list-or-tuple-shaped
// attr.Value, as found in the underlying value of the "objects" dynamic
// parameter. It returns an error for any other underlying type.
func selectElements(v attr.Value) ([]attr.Value, error) {
	switch val := v.(type) {
	case types.List:
		return val.Elements(), nil
	case types.Tuple:
		return val.Elements(), nil
	default:
		return nil, fmt.Errorf("objects must be a list or tuple, got %T", v)
	}
}
