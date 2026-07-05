// Copyright (c) 2026 cdktn-io
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure JoinFunction satisfies the function.Function interface.
var _ function.Function = JoinFunction{}

// NewJoinFunction returns a new instance of the provider::cfncompat::join
// function.
func NewJoinFunction() function.Function {
	return JoinFunction{}
}

// JoinFunction implements CloudFormation's Fn::Join intrinsic function:
// it appends a set of values into a single value, separated by a delimiter.
//
// See: https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-join.html
type JoinFunction struct{}

func (f JoinFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "join"
}

func (f JoinFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Join a list of strings with a delimiter",
		Description: "Appends a set of values into a single value, separated by the specified delimiter. If the delimiter is the empty string, the values are concatenated with no delimiter.",
		MarkdownDescription: "Appends a set of values into a single value, separated by the specified `delimiter`. " +
			"If `delimiter` is the empty string, the values are concatenated with no delimiter.\n\n" +
			"Matches the semantics of CloudFormation's " +
			"[`Fn::Join`](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/intrinsic-function-reference-join.html) intrinsic function.\n\n" +
			"The delimiter occurs between fragments only; it never terminates the final value. " +
			"An empty `values` list returns the empty string. Every element of `values` must be a known, " +
			"non-null string (CloudFormation's `Fn::Join` list of values must not contain null entries).",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:                "delimiter",
				Description:         "The value to occur between fragments. Occurs between fragments only; does not terminate the final value.",
				MarkdownDescription: "The value to occur between fragments. Occurs between fragments only; does not terminate the final value.",
			},
			function.ListParameter{
				Name:                "values",
				ElementType:         types.StringType,
				Description:         "The list of string values to combine.",
				MarkdownDescription: "The list of string values to combine.",
			},
		},
		Return: function.StringReturn{},
	}
}

func (f JoinFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var delimiter types.String
	var values types.List

	resp.Error = req.Arguments.Get(ctx, &delimiter, &values)
	if resp.Error != nil {
		return
	}

	if delimiter.IsUnknown() {
		return
	}

	if delimiter.IsNull() {
		resp.Error = function.NewArgumentFuncError(0, "join: delimiter must not be null")
		return
	}

	if values.IsUnknown() {
		return
	}

	if values.IsNull() {
		resp.Error = function.NewArgumentFuncError(1, "join: values must not be null")
		return
	}

	elements := values.Elements()
	parts := make([]string, 0, len(elements))

	for i, elem := range elements {
		strVal, ok := elem.(types.String)
		if !ok {
			resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf("join: values[%d] has unexpected element type %T", i, elem))
			return
		}

		if strVal.IsUnknown() {
			return
		}

		if strVal.IsNull() {
			resp.Error = function.NewArgumentFuncError(1, fmt.Sprintf("join: values[%d] must not be null", i))
			return
		}

		parts = append(parts, strVal.ValueString())
	}

	resp.Error = resp.Result.Set(ctx, strings.Join(parts, delimiter.ValueString()))
}
